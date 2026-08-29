package lab

import (
	"context"
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/d-kholin/k8up-gui/internal/k8s"
	"github.com/d-kholin/k8up-gui/internal/restore"
)

// Plan is what an app-lab run would do, shown in the confirm dialog before
// anything is created.
type Plan struct {
	SourceNamespace string      `json:"sourceNamespace"`
	App             *PlanApp    `json:"app,omitempty"`
	AppError        string      `json:"appError,omitempty"`
	PVCs            []PlanPVC   `json:"pvcs"`
	Dump            *PlanDump   `json:"dump,omitempty"`
	Warnings        []string    `json:"warnings,omitempty"`
}

type PlanApp struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	LabPath        string `json:"labPath,omitempty"`
	TargetRevision string `json:"targetRevision,omitempty"`
	RepoURL        string `json:"repoURL,omitempty"`
}

type PlanPVC struct {
	PVCName      string    `json:"pvcName"`
	SnapshotName string    `json:"snapshotName"`
	SnapshotID   string    `json:"snapshotId"`
	Date         time.Time `json:"date"`
	SourceExists bool      `json:"sourceExists"`
}

type PlanDump struct {
	SnapshotName string    `json:"snapshotName"`
	SnapshotID   string    `json:"snapshotId"`
	Path         string    `json:"path"`
	Date         time.Time `json:"date"`
}

// Plan computes the newest restore point per source PVC plus the newest SQL
// dump snapshot, and resolves the Argo Application the clone would come from.
func (m *Manager) Plan(ctx context.Context, sourceNS string) (*Plan, error) {
	if sourceNS == m.LabNamespace {
		return nil, fmt.Errorf("cannot plan a lab for the lab namespace itself")
	}
	snaps, err := m.Clients.ListResource(ctx, k8s.GVRSnapshot, sourceNS)
	if err != nil {
		return nil, fmt.Errorf("list snapshots in %s: %w", sourceNS, err)
	}
	plan := &Plan{SourceNamespace: sourceNS}
	pvcs, dump := planFromSnapshots(snaps.Items)
	plan.Dump = dump

	for i := range pvcs {
		p := &pvcs[i]
		if _, err := m.Clients.Typed.CoreV1().PersistentVolumeClaims(sourceNS).Get(ctx, p.PVCName, metav1.GetOptions{}); err == nil {
			p.SourceExists = true
		} else {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("snapshot %s references PVC %q which no longer exists in %s — skipped (its size/class cannot be derived)", p.SnapshotName, p.PVCName, sourceNS))
		}
	}
	plan.PVCs = pvcs

	if len(plan.PVCs) == 0 && plan.Dump == nil {
		return nil, fmt.Errorf("namespace %s has no restorable snapshots", sourceNS)
	}

	app, err := m.Clients.FindApplicationForNamespace(ctx, m.ArgoNamespace, sourceNS)
	if err != nil {
		plan.AppError = err.Error()
		return plan, nil
	}
	src, _, _ := unstructured.NestedMap(app.Object, "spec", "source")
	pa := &PlanApp{Name: app.GetName()}
	if src != nil {
		pa.Path, _ = src["path"].(string)
		pa.TargetRevision, _ = src["targetRevision"].(string)
		pa.RepoURL, _ = src["repoURL"].(string)
	}
	pa.LabPath = app.GetAnnotations()[k8s.AnnLabPath]
	if src == nil || pa.Path == "" {
		plan.AppError = fmt.Sprintf("application %s is not a single-source git-path app; only the data tier is available", app.GetName())
	} else {
		plan.App = pa
	}
	return plan, nil
}

// planFromSnapshots groups a namespace's Snapshot CRs into the newest
// restore point per source PVC plus the newest application-level SQL dump.
func planFromSnapshots(items []unstructured.Unstructured) ([]PlanPVC, *PlanDump) {
	latestByPVC := map[string]PlanPVC{}
	var dump *PlanDump
	for i := range items {
		obj := items[i].Object
		name := items[i].GetName()
		id := snapshotID(obj, name)
		date := snapshotDate(&items[i])
		paths := snapshotPaths(obj)

		if p := restore.DumpFilePath(paths); p != "" {
			if dump == nil || date.After(dump.Date) {
				dump = &PlanDump{SnapshotName: name, SnapshotID: id, Path: p, Date: date}
			}
			continue
		}
		for _, pvc := range restore.SourcePVCCandidates(paths) {
			cur, ok := latestByPVC[pvc]
			if !ok || date.After(cur.Date) {
				latestByPVC[pvc] = PlanPVC{PVCName: pvc, SnapshotName: name, SnapshotID: id, Date: date}
			}
		}
	}
	out := make([]PlanPVC, 0, len(latestByPVC))
	for _, p := range latestByPVC {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PVCName < out[j].PVCName })
	return out, dump
}

func snapshotDate(obj *unstructured.Unstructured) time.Time {
	if s, _, _ := unstructured.NestedString(obj.Object, "spec", "date"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return obj.GetCreationTimestamp().Time
}

func snapshotID(obj map[string]any, fallback string) string {
	for _, fields := range [][]string{{"spec", "id"}, {"status", "id"}, {"spec", "snapshot"}} {
		if s, found, _ := unstructured.NestedString(obj, fields...); found && s != "" {
			return s
		}
	}
	return fallback
}

func snapshotPaths(obj map[string]any) []string {
	raw, found, _ := unstructured.NestedStringSlice(obj, "spec", "paths")
	if !found {
		return nil
	}
	return raw
}
