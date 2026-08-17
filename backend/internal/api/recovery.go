package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/d-kholin/k8up-gui/internal/auth"
	"github.com/d-kholin/k8up-gui/internal/k8s"
	"github.com/d-kholin/k8up-gui/internal/restore"
)

// SQL dump recovery API: a read-only preflight that tells the UI everything a
// recovery would do (DB pod, git-defined command, workloads to stop, nearby
// PVC snapshots for point-in-time), and the start endpoint.

type recoveryDBCandidate struct {
	PodName           string                 `json:"podName"`
	Container         string                 `json:"container"`
	Workload          *k8s.WorkloadRef       `json:"workload,omitempty"`
	AppGroup          string                 `json:"appGroup,omitempty"`
	BackupCommand     string                 `json:"backupCommand"`
	RestoreCommand    string                 `json:"restoreCommand,omitempty"`
	CommandSource     string                 `json:"commandSource,omitempty"`
	CommandError      string                 `json:"commandError,omitempty"`
	WorkloadsToStop   []k8s.ScalableWorkload `json:"workloadsToStop"`
	// QuiesceGrouping: annotation | argo-app:<name> | namespace
	QuiesceGrouping string `json:"quiesceGrouping,omitempty"`
	QuiesceWarning  string `json:"quiesceWarning,omitempty"`
	HasRestoreCommand bool `json:"hasRestoreCommand"`
}

type recoveryPVCOption struct {
	PVCName      string    `json:"pvcName"`
	SnapshotName string    `json:"snapshotName"`
	SnapshotID   string    `json:"snapshotId"`
	Date         time.Time `json:"date"`
	DeltaSeconds int64     `json:"deltaSeconds"`
}

// handleRecoveryPlan builds the preflight for one dump snapshot.
func (s *Server) handleRecoveryPlan(w http.ResponseWriter, r *http.Request) {
	if s.K8s == nil {
		http.Error(w, "kubernetes client not configured", http.StatusServiceUnavailable)
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	ctx := r.Context()

	snap, err := s.K8s.GetSnapshot(ctx, ns, name)
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	paths := snapshotPathsOf(snap)
	dumpPath := restore.DumpFilePath(paths)
	if dumpPath == "" {
		http.Error(w, "snapshot has no .sql path; use a plain PVC restore", http.StatusBadRequest)
		return
	}
	dumpDate := snapshotDateOf(snap)

	pods, err := s.K8s.ListBackupCommandPods(ctx, ns)
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	candidates := make([]recoveryDBCandidate, 0, len(pods))
	for i := range pods {
		p := pods[i]
		c := recoveryDBCandidate{
			PodName:         p.PodName,
			Container:       p.Container,
			Workload:        p.Workload,
			AppGroup:        p.AppGroup,
			BackupCommand:   p.BackupCommand,
			WorkloadsToStop: []k8s.ScalableWorkload{},
		}
		if cmd, source, err := s.Orch.ResolveRestoreCommand(&p); err == nil {
			c.RestoreCommand = cmd
			c.CommandSource = source
			c.HasRestoreCommand = true
		} else {
			c.CommandError = err.Error()
		}
		if set, grouping, err := s.K8s.QuiesceSet(ctx, &p); err == nil {
			if set != nil {
				c.WorkloadsToStop = set
			}
			c.QuiesceGrouping = grouping
			if grouping == "namespace" && len(set) > 0 {
				c.QuiesceWarning = "no Argo app grouping found — ALL other workloads in namespace " + ns + " will be stopped; scope with the " + k8s.AnnQuiesceWorkloads + " annotation if this namespace hosts multiple apps"
			}
		} else {
			c.QuiesceWarning = err.Error()
		}
		candidates = append(candidates, c)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"snapshot": map[string]any{
			"namespace": ns,
			"name":      name,
			"dumpPath":  dumpPath,
			"date":      dumpDate,
		},
		"dbCandidates": candidates,
		"bestGuess":    bestDBPodGuess(dumpPath, candidates),
		"pvcOptions":   s.nearbyPVCSnapshots(ctx, ns, name, dumpDate),
	})
}

// bestDBPodGuess matches the dump filename against pod/workload/instance names
// so the dialog can preselect the right database in shared namespaces.
func bestDBPodGuess(dumpPath string, candidates []recoveryDBCandidate) string {
	if len(candidates) == 1 {
		return candidates[0].PodName
	}
	base := strings.TrimSuffix(path.Base(dumpPath), ".sql")
	base = strings.ToLower(base)
	bestScore := 0
	best := ""
	for _, c := range candidates {
		names := []string{c.PodName, c.AppGroup}
		if c.Workload != nil {
			names = append(names, c.Workload.Name)
		}
		for _, n := range names {
			n = strings.ToLower(n)
			if n == "" {
				continue
			}
			score := 0
			if strings.Contains(base, n) || strings.Contains(n, base) {
				score = len(n)
			}
			if score > bestScore {
				bestScore = score
				best = c.PodName
			}
		}
	}
	return best
}

// nearbyPVCSnapshots picks, per source PVC in the namespace, the snapshot
// closest in time to the dump — the point-in-time companions.
func (s *Server) nearbyPVCSnapshots(ctx context.Context, ns, dumpSnapName string, dumpDate time.Time) []recoveryPVCOption {
	out := []recoveryPVCOption{}
	list, err := s.K8s.ListResource(ctx, k8s.GVRSnapshot, ns)
	if err != nil {
		return out
	}
	type bestPick struct {
		opt   recoveryPVCOption
		delta float64
	}
	best := map[string]bestPick{}
	for i := range list.Items {
		item := &list.Items[i]
		if item.GetName() == dumpSnapName {
			continue
		}
		paths := snapshotPathsOf(item)
		if restore.DumpFilePath(paths) != "" {
			continue // another dump, not a PVC snapshot
		}
		candidates := restore.SourcePVCCandidates(paths)
		if len(candidates) == 0 {
			continue
		}
		date := snapshotDateOf(item)
		if date.IsZero() {
			continue
		}
		delta := math.Abs(date.Sub(dumpDate).Seconds())
		id, _, _ := unstructured.NestedString(item.Object, "spec", "id")
		for _, pvc := range candidates {
			cur, ok := best[pvc]
			if ok && cur.delta <= delta {
				continue
			}
			best[pvc] = bestPick{
				opt: recoveryPVCOption{
					PVCName:      pvc,
					SnapshotName: item.GetName(),
					SnapshotID:   id,
					Date:         date,
					DeltaSeconds: int64(date.Sub(dumpDate).Seconds()),
				},
				delta: delta,
			}
		}
	}
	for _, b := range best {
		out = append(out, b.opt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PVCName < out[j].PVCName })
	return out
}

func (s *Server) handleStartRecovery(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	var req restore.RecoveryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	req.Actor = u.Username
	if s.K8s == nil {
		http.Error(w, "kubernetes client not configured", http.StatusServiceUnavailable)
		return
	}
	st, err := s.Orch.StartRecovery(r.Context(), req)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func snapshotPathsOf(obj *unstructured.Unstructured) []string {
	paths, _, _ := unstructured.NestedStringSlice(obj.Object, "spec", "paths")
	return paths
}

func snapshotDateOf(obj *unstructured.Unstructured) time.Time {
	for _, fields := range [][]string{{"spec", "date"}, {"metadata", "creationTimestamp"}} {
		if v, ok, _ := unstructured.NestedString(obj.Object, fields...); ok && v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}
