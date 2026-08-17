package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/k8s"
)

type Step string

const (
	StepQueued       Step = "queued"
	StepPausingArgo  Step = "pausing_argo"
	StepScalingDown  Step = "scaling_down"
	StepRestoring    Step = "restoring"
	StepScalingUp    Step = "scaling_up"
	StepResumingArgo Step = "resuming_argo"
	StepDone         Step = "done"
	StepFailed       Step = "failed"
)

// State is durable (controller STS annotation) + in-memory job status.
type State struct {
	RestoreID string `json:"restoreId"`
	// Application is informational (owning Argo app if resolved).
	Application      *k8s.ArgoRef     `json:"application,omitempty"`
	Workload         *k8s.WorkloadRef `json:"workload,omitempty"`
	OriginalReplicas int32            `json:"originalReplicas"`
	// Global Argo pause: application-controller STS scaled to 0.
	ArgoControllerReplicas int32      `json:"argoControllerReplicas,omitempty"`
	ArgoPausedGlobally     bool       `json:"argoPausedGlobally,omitempty"`
	Step                   Step       `json:"step"`
	SnapshotID             string     `json:"snapshotId"`
	SnapshotCR             string     `json:"snapshotCR,omitempty"`
	SnapshotNamespace      string     `json:"snapshotNamespace,omitempty"`
	PVCNamespace           string     `json:"pvcNamespace"`
	PVCName                string     `json:"pvcName"`
	RestoreCRName          string     `json:"restoreCRName,omitempty"`
	StartedAt              time.Time  `json:"startedAt"`
	FinishedAt             *time.Time `json:"finishedAt,omitempty"`
	LastError              string     `json:"lastError,omitempty"`
	Actor                  string     `json:"actor,omitempty"`
	// ArgoSyncResumed: controller scaled back up (global pause cleared).
	ArgoSyncResumed *bool `json:"argoSyncResumed,omitempty"`
	BytesRecovered  int64 `json:"bytesRecovered,omitempty"`
	// CancelRequested is set by Cancel; Cancelled marks the terminal state as
	// operator-aborted rather than failed on its own.
	CancelRequested bool `json:"cancelRequested,omitempty"`
	Cancelled       bool `json:"cancelled,omitempty"`
	// ProgressPercent is best-effort, parsed from restic job log lines.
	ProgressPercent *float64 `json:"progressPercent,omitempty"`
}

type Request struct {
	SnapshotNamespace string `json:"snapshotNamespace"`
	SnapshotName      string `json:"snapshotName"`
	SnapshotID        string `json:"snapshotId"`
	PVCNamespace      string `json:"pvcNamespace"`
	PVCName           string `json:"pvcName"`
	Actor             string `json:"actor"`
}

type Orchestrator struct {
	Clients          *k8s.Clients
	ArgoNS           string
	ScaleDownTimeout time.Duration
	RestoreTimeout   time.Duration
	Audit            *audit.Store
	Log              *slog.Logger
	Logs             *LogHub
	OnUpdate         func(State)
	OnLog            func(restoreID, line string)
	// SnapshotSize resolves the snapshot's restore-size in bytes (wired by the
	// API server, which owns restic credential resolution). Best-effort.
	SnapshotSize func(ctx context.Context, snapNS, snapName, snapID string) (int64, error)

	mu   sync.Mutex
	jobs map[string]*State
	// cancels holds the per-run cancel funcs for in-flight restores.
	cancels map[string]context.CancelFunc
	// activeID is the restore currently holding the global Argo pause. Only one
	// restore may run at a time; Start fails fast while it is set.
	activeID string
}

func NewOrchestrator(c *k8s.Clients, argoNS string, scaleTO, restoreTO time.Duration, a *audit.Store, log *slog.Logger) *Orchestrator {
	if log == nil {
		log = slog.Default()
	}
	return &Orchestrator{
		Clients:          c,
		ArgoNS:           argoNS,
		ScaleDownTimeout: scaleTO,
		RestoreTimeout:   restoreTO,
		Audit:            a,
		Log:              log,
		Logs:             NewLogHub(),
		jobs:             map[string]*State{},
		cancels:          map[string]context.CancelFunc{},
	}
}

func (o *Orchestrator) Get(id string) (*State, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.jobs[id]
	if !ok {
		return nil, false
	}
	cp := *s
	return &cp, true
}

func (o *Orchestrator) List() []State {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]State, 0, len(o.jobs))
	for _, s := range o.jobs {
		out = append(out, *s)
	}
	// Newest first for Restores page.
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

func (o *Orchestrator) publish(s *State) {
	if o.OnUpdate != nil {
		cp := *s
		o.OnUpdate(cp)
	}
}

func (o *Orchestrator) persist(s *State) {
	if o.Audit == nil || s == nil || s.RestoreID == "" {
		return
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := o.Audit.UpsertRestoreJob(context.Background(), s.RestoreID, b); err != nil && o.Log != nil {
		o.Log.Warn("persist restore job", "restoreId", s.RestoreID, "err", err)
	}
}

func (o *Orchestrator) set(s *State) {
	o.mu.Lock()
	o.jobs[s.RestoreID] = s
	o.mu.Unlock()
	o.persist(s)
	o.publish(s)
	if o.Clients != nil && s.ArgoPausedGlobally {
		b, _ := json.Marshal(s)
		_ = o.Clients.SetArgoControllerStateAnnotation(context.Background(), o.ArgoNS, string(b))
	}
}

// LoadPersisted hydrates in-memory jobs + log buffers from SQLite (/data PVC).
func (o *Orchestrator) LoadPersisted(ctx context.Context) error {
	if o.Audit == nil {
		return nil
	}
	blobs, err := o.Audit.ListRestoreJobs(ctx, 200)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, raw := range blobs {
		var st State
		if err := json.Unmarshal(raw, &st); err != nil || st.RestoreID == "" {
			continue
		}
		// Don't clobber a fresher in-memory entry (e.g. from ScanInterrupted).
		if cur, ok := o.jobs[st.RestoreID]; ok {
			// Prefer annotation-backed interrupted state if still mid-flight.
			if cur.ArgoPausedGlobally && st.Step != StepDone && st.Step != StepFailed {
				continue
			}
		}
		cp := st
		// Incomplete jobs after process death are marked failed unless Argo pause scan rewrote them.
		if cp.Step != StepDone && cp.Step != StepFailed && !cp.ArgoPausedGlobally {
			if cp.LastError == "" {
				cp.LastError = "restore interrupted by process restart"
			}
			cp.Step = StepFailed
			now := time.Now().UTC()
			cp.FinishedAt = &now
			// write back corrected state
			if b, err := json.Marshal(&cp); err == nil {
				_ = o.Audit.UpsertRestoreJob(ctx, cp.RestoreID, b)
			}
		}
		o.jobs[cp.RestoreID] = &cp
		if lines, err := o.Audit.GetRestoreLogs(ctx, cp.RestoreID); err == nil && len(lines) > 0 && o.Logs != nil {
			o.Logs.Seed(cp.RestoreID, lines)
		}
	}
	return nil
}

// Start begins async restore orchestration and returns the job id immediately.
func (o *Orchestrator) Start(ctx context.Context, req Request) (*State, error) {
	if o.Clients == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}
	if req.PVCNamespace == "" || req.PVCName == "" {
		return nil, fmt.Errorf("pvcNamespace and pvcName are required")
	}
	if req.SnapshotNamespace == "" || req.SnapshotName == "" {
		return nil, fmt.Errorf("snapshotNamespace and snapshotName are required")
	}
	// A snapshot may only be restored into the namespace it was taken from.
	if req.PVCNamespace != req.SnapshotNamespace {
		return nil, fmt.Errorf("restore target namespace %q does not match snapshot namespace %q; snapshots may only be restored to their source PVC", req.PVCNamespace, req.SnapshotNamespace)
	}
	// Refuse if Argo is already paused by a prior restore.
	if raw, reps, err := o.Clients.GetArgoControllerPausedState(ctx, o.ArgoNS); err == nil {
		if raw != "" || reps == 0 {
			return nil, fmt.Errorf("argo application-controller already paused (replicas=%d); resume or clear restore-gui state before starting another restore", reps)
		}
	}
	id := uuid.NewString()
	st := &State{
		RestoreID:         id,
		Step:              StepQueued,
		SnapshotID:        req.SnapshotID,
		SnapshotCR:        req.SnapshotName,
		SnapshotNamespace: req.SnapshotNamespace,
		PVCNamespace:      req.PVCNamespace,
		PVCName:           req.PVCName,
		StartedAt:         time.Now().UTC(),
		Actor:             req.Actor,
	}
	snap, err := o.Clients.GetSnapshot(ctx, req.SnapshotNamespace, req.SnapshotName)
	if err != nil {
		return nil, fmt.Errorf("get snapshot: %w", err)
	}
	if st.SnapshotID == "" {
		if id, ok, _ := unstructuredString(snap.Object, "spec", "id"); ok {
			st.SnapshotID = id
		} else if id, ok, _ := unstructuredString(snap.Object, "status", "id"); ok {
			st.SnapshotID = id
		} else if id, ok, _ := unstructuredString(snap.Object, "spec", "snapshot"); ok {
			st.SnapshotID = id
		}
		if st.SnapshotID == "" {
			st.SnapshotID = req.SnapshotName
		}
	}
	// The target PVC must be one the snapshot actually backed up (derived from
	// spec.paths). This blocks restoring snapshot A onto PVC B by retyping.
	candidates := SourcePVCCandidates(snapshotPaths(snap.Object))
	if len(candidates) == 0 {
		return nil, fmt.Errorf("cannot determine source PVC from snapshot %s/%s (no usable spec.paths); refusing restore to an unverified target", req.SnapshotNamespace, req.SnapshotName)
	}
	if !containsString(candidates, req.PVCName) {
		return nil, fmt.Errorf("PVC %q is not a source of snapshot %s/%s (backed up: %s); snapshots may only be restored to the PVC they were taken from", req.PVCName, req.SnapshotNamespace, req.SnapshotName, strings.Join(candidates, ", "))
	}

	// Claim the single-restore slot atomically; the Argo pre-check above is
	// advisory only (it cannot prevent two concurrent Starts on its own).
	o.mu.Lock()
	if o.activeID != "" {
		active := o.activeID
		o.mu.Unlock()
		return nil, fmt.Errorf("restore %s already in progress; only one restore may run at a time", active)
	}
	o.activeID = id
	o.mu.Unlock()

	o.set(st)
	go o.run(st)
	return st, nil
}

func (o *Orchestrator) run(st *State) {
	// ctx is for cleanup and must survive cancellation; runCtx drives the
	// forward steps and is what Cancel aborts.
	ctx := context.Background()
	runCtx, cancelRun := context.WithCancel(ctx)
	o.mu.Lock()
	o.cancels[st.RestoreID] = cancelRun
	o.mu.Unlock()

	var runErr error
	argoPaused := false
	scaledDown := false

	defer func() {
		cancelRun()
		o.mu.Lock()
		delete(o.cancels, st.RestoreID)
		cancelled := st.CancelRequested
		o.mu.Unlock()

		// Some client-go paths don't wrap context.Canceled; a requested cancel
		// with any error is treated as the cancel taking effect.
		if cancelled && runErr != nil {
			st.Cancelled = true
			st.LastError = "restore cancelled by operator"
			// Stop the in-cluster restic job: deleting the Restore CR cascades to
			// its Job via owner references.
			if st.RestoreCRName != "" {
				if err := o.Clients.DeleteRestore(ctx, st.PVCNamespace, st.RestoreCRName); err != nil {
					o.Log.Warn("delete restore CR after cancel", "err", err)
					o.emitLog(st.RestoreID, fmt.Sprintf("··· could not delete Restore CR %s: %v", st.RestoreCRName, err))
				} else {
					o.emitLog(st.RestoreID, fmt.Sprintf("··· deleted Restore CR %s/%s", st.PVCNamespace, st.RestoreCRName))
				}
			}
			// Bring the workload back; the PVC may hold a partial restore — the
			// operator chose to abort and the log says so.
			if scaledDown && st.Workload != nil {
				if err := o.Clients.Scale(ctx, st.Workload, st.OriginalReplicas); err != nil {
					o.Log.Error("scale up after cancel", "err", err)
					st.LastError = joinErr(st.LastError, fmt.Errorf("scale up after cancel: %w", err))
				} else {
					o.emitLog(st.RestoreID, fmt.Sprintf("··· scaled %s %s/%s back to %d replica(s) — WARNING: volume may contain a partial restore", st.Workload.Kind, st.Workload.Namespace, st.Workload.Name, st.OriginalReplicas))
				}
			}
		}

		// CRITICAL: always resume Argo controller if we paused it.
		if argoPaused {
			st.Step = StepResumingArgo
			o.set(st)
			if err := o.Clients.ResumeArgoController(ctx, o.ArgoNS, st.ArgoControllerReplicas); err != nil {
				o.Log.Error("failed to resume argo controller", "err", err)
				st.LastError = joinErr(st.LastError, fmt.Errorf("resume argo controller: %w", err))
				resumed := false
				st.ArgoSyncResumed = &resumed
				b, _ := json.Marshal(st)
				_ = o.Clients.SetArgoControllerStateAnnotation(ctx, o.ArgoNS, string(b))
			} else {
				resumed := true
				st.ArgoSyncResumed = &resumed
				st.ArgoPausedGlobally = false
				argoPaused = false
				_ = o.Clients.SetArgoControllerStateAnnotation(ctx, o.ArgoNS, "")
			}
		}

		now := time.Now().UTC()
		st.FinishedAt = &now
		if runErr != nil {
			st.Step = StepFailed
			if st.LastError == "" {
				st.LastError = runErr.Error()
			}
		} else if st.Step != StepFailed {
			st.Step = StepDone
		}
		o.mu.Lock()
		o.jobs[st.RestoreID] = st
		if o.activeID == st.RestoreID {
			o.activeID = ""
		}
		o.mu.Unlock()
		o.persist(st)
		// Keep last log snapshot durable at finish.
		if o.Audit != nil && o.Logs != nil {
			if lines := o.Logs.Snapshot(st.RestoreID); len(lines) > 0 {
				_ = o.Audit.ReplaceRestoreLogs(context.Background(), st.RestoreID, lines)
			}
		}
		o.publish(st)

		if o.Audit != nil {
			status := "success"
			if st.Step == StepFailed {
				status = "failed"
			}
			var pausedPtr *bool
			if st.ArgoSyncResumed != nil {
				p := !*st.ArgoSyncResumed
				pausedPtr = &p
			}
			argoName := "argocd/" + k8s.ArgoApplicationControllerSTS
			if st.Application != nil {
				argoName = st.Application.Namespace + "/" + st.Application.Name
			}
			_, _ = o.Audit.Insert(ctx, audit.Entry{
				Kind:       "restore",
				Actor:      st.Actor,
				Namespace:  st.PVCNamespace,
				Snapshot:   st.SnapshotID,
				PVC:        st.PVCName,
				Status:     status,
				Detail:     st.LastError,
				RestoreID:  st.RestoreID,
				ArgoApp:    argoName,
				ArgoPaused: pausedPtr,
				Bytes:      st.BytesRecovered,
			})
		}
	}()

	// 1. Resolve ownership
	wl, err := o.Clients.ResolvePVCOwner(runCtx, st.PVCNamespace, st.PVCName)
	if err != nil {
		runErr = err
		return
	}
	st.Workload = wl
	replicas, err := o.Clients.GetReplicas(runCtx, wl)
	if err != nil {
		runErr = err
		return
	}
	st.OriginalReplicas = replicas

	// Optional: record owning Application name for UI (not used for pause).
	if argo, _, err := o.Clients.ResolveArgoApp(runCtx, wl, o.ArgoNS); err == nil && argo != nil {
		st.Application = argo
	}

	// 2. Pause ALL Argo reconciliation (scale application-controller to 0).
	st.Step = StepPausingArgo
	o.set(st)
	o.emitLog(st.RestoreID, "pausing Argo CD application-controller…")
	b, _ := json.Marshal(st)
	origCtrl, err := o.Clients.PauseArgoController(runCtx, o.ArgoNS, string(b))
	if err != nil {
		runErr = fmt.Errorf("pause argo controller: %w", err)
		return
	}
	if origCtrl <= 0 {
		origCtrl = 1
	}
	st.ArgoControllerReplicas = origCtrl
	st.ArgoPausedGlobally = true
	argoPaused = true
	o.set(st)
	o.Log.Info("argo application-controller paused", "originalReplicas", origCtrl)
	o.emitLog(st.RestoreID, fmt.Sprintf("Argo controller paused (was %d replica(s))", origCtrl))

	// 3. Scale down workload
	st.Step = StepScalingDown
	o.set(st)
	o.emitLog(st.RestoreID, fmt.Sprintf("scaling down %s %s/%s → 0", wl.Kind, wl.Namespace, wl.Name))
	scaleCtx, cancel := context.WithTimeout(runCtx, o.ScaleDownTimeout)
	defer cancel()
	if err := o.Clients.Scale(scaleCtx, wl, 0); err != nil {
		runErr = fmt.Errorf("scale down: %w", err)
		return
	}
	scaledDown = true
	if err := o.Clients.WaitPodsGone(scaleCtx, wl); err != nil {
		runErr = fmt.Errorf("wait pods gone: %w", err)
		return
	}
	o.emitLog(st.RestoreID, "workload pods terminated")

	// 4. Restore CR
	st.Step = StepRestoring
	crName := fmt.Sprintf("gui-restore-%s", st.RestoreID[:8])
	st.RestoreCRName = crName
	o.set(st)
	if _, err := o.Clients.CreateRestoreCR(runCtx, st.PVCNamespace, crName, st.SnapshotID, st.PVCName, nil); err != nil {
		runErr = fmt.Errorf("create restore cr: %w", err)
		return
	}
	o.emitLog(st.RestoreID, fmt.Sprintf("created Restore CR %s/%s — following job logs…", st.PVCNamespace, crName))

	restoreCtx, cancelR := context.WithTimeout(runCtx, o.RestoreTimeout)
	defer cancelR()
	// Stream job logs in parallel with status wait.
	go o.followRestoreLogs(restoreCtx, st.RestoreID, st.PVCNamespace, crName)
	if err := o.waitRestoreComplete(restoreCtx, st.PVCNamespace, crName); err != nil {
		runErr = fmt.Errorf("restore: %w", err)
		return
	}
	o.emitLog(st.RestoreID, "··· restore job finished successfully")

	// Best-effort bytes recovered = snapshot restore-size.
	if o.SnapshotSize != nil && st.SnapshotCR != "" {
		sizeCtx, cancelS := context.WithTimeout(ctx, 60*time.Second)
		if n, err := o.SnapshotSize(sizeCtx, st.SnapshotNamespace, st.SnapshotCR, st.SnapshotID); err == nil && n > 0 {
			st.BytesRecovered = n
		} else if err != nil {
			o.Log.Warn("resolve snapshot restore-size", "err", err)
		}
		cancelS()
	}

	// 5. Scale up workload
	st.Step = StepScalingUp
	o.set(st)
	if err := o.Clients.Scale(ctx, wl, st.OriginalReplicas); err != nil {
		runErr = fmt.Errorf("scale up: %w", err)
		return
	}
	// 6. Resume Argo controller — defer
}

func (o *Orchestrator) emitLog(restoreID, line string) {
	if o.Logs != nil {
		o.Logs.Append(restoreID, line)
	}
	if o.Audit != nil {
		// Best-effort durable log line (async would be nicer; keep simple/correct).
		_ = o.Audit.AppendRestoreLog(context.Background(), restoreID, line)
	}
	if o.OnLog != nil {
		o.OnLog(restoreID, line)
	}
}

func (o *Orchestrator) followRestoreLogs(ctx context.Context, restoreID, ns, crName string) {
	if o.Clients == nil {
		return
	}
	err := o.Clients.FollowRestoreJobLogs(ctx, ns, crName, func(line string) {
		o.emitLog(restoreID, line)
		o.noteProgress(restoreID, line)
	})
	if err != nil && ctx.Err() == nil {
		o.emitLog(restoreID, fmt.Sprintf("··· log follow ended: %v", err))
		o.Log.Warn("restore log follow ended", "restoreId", restoreID, "err", err)
	}
}

func (o *Orchestrator) waitRestoreComplete(ctx context.Context, ns, name string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		obj, err := o.Clients.GetRestore(ctx, ns, name)
		if err != nil {
			return err
		}

		// Prefer status.finished — K8up sets this when the job ends (success or fail).
		finished, finFound, _ := nestedBool(obj.Object, "status", "finished")
		if finFound && finished {
			// Completed condition: reason Failed vs Succeeded
			if conds, found, _ := nestedSlice(obj.Object, "status", "conditions"); found {
				for _, c := range conds {
					m, ok := c.(map[string]any)
					if !ok {
						continue
					}
					t, _ := m["type"].(string)
					st, _ := m["status"].(string)
					reason, _ := m["reason"].(string)
					msg, _ := m["message"].(string)
					if t == "Completed" && st == "True" {
						if reason == "Failed" || reason == "Error" {
							return fmt.Errorf("restore failed: %s", msg)
						}
						// Succeeded or Finished without failure
						return nil
					}
					if t == "Failed" && st == "True" {
						return fmt.Errorf("restore failed: %s", msg)
					}
				}
			}
			// finished but no clear condition — treat as success only if no Failed job message
			return nil
		}

		// In-progress signals: Ready=True / Progressing=True are NOT success.
		if conds, found, _ := nestedSlice(obj.Object, "status", "conditions"); found {
			for _, c := range conds {
				m, ok := c.(map[string]any)
				if !ok {
					continue
				}
				t, _ := m["type"].(string)
				st, _ := m["status"].(string)
				reason, _ := m["reason"].(string)
				msg, _ := m["message"].(string)
				if t == "Completed" && st == "True" && (reason == "Failed" || reason == "Error") {
					return fmt.Errorf("restore failed: %s", msg)
				}
				if t == "Failed" && st == "True" {
					return fmt.Errorf("restore failed: %s", msg)
				}
				// Progressing False + Finished without finished flag yet
				if t == "Progressing" && st == "False" && reason == "Finished" {
					if strings.Contains(strings.ToLower(msg), "failed") {
						return fmt.Errorf("restore failed: %s", msg)
					}
				}
			}
		}

		t := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func nestedBool(obj map[string]any, fields ...string) (bool, bool, error) {
	var cur any = obj
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return false, false, nil
		}
		cur, ok = m[f]
		if !ok {
			return false, false, nil
		}
	}
	b, ok := cur.(bool)
	return b, ok, nil
}

// ManualResumeArgo scales the application-controller back up (global unstick).
func (o *Orchestrator) ManualResumeArgo(ctx context.Context, _appNS, _appName string) error {
	raw, reps, err := o.Clients.GetArgoControllerPausedState(ctx, o.ArgoNS)
	if err != nil {
		return err
	}
	orig := int32(1)
	if raw != "" {
		var st State
		if err := json.Unmarshal([]byte(raw), &st); err == nil && st.ArgoControllerReplicas > 0 {
			orig = st.ArgoControllerReplicas
		}
	}
	if reps > 0 && raw == "" {
		// already running and no lock
		return nil
	}
	if err := o.Clients.ResumeArgoController(ctx, o.ArgoNS, orig); err != nil {
		return err
	}
	if o.Audit != nil {
		_, _ = o.Audit.Insert(ctx, audit.Entry{
			Kind:    "system",
			Actor:   "operator",
			Status:  "resumed_argo_controller",
			ArgoApp: o.ArgoNS + "/" + k8s.ArgoApplicationControllerSTS,
			Detail:  "manual resume",
		})
	}
	return nil
}

// ScanInterrupted loads global pause state from the controller STS annotation.
func (o *Orchestrator) ScanInterrupted(ctx context.Context) ([]State, error) {
	if o.Clients == nil {
		return nil, nil
	}
	raw, reps, err := o.Clients.GetArgoControllerPausedState(ctx, o.ArgoNS)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		// Controller at 0 without our annotation — still surface a warning.
		if reps == 0 {
			st := State{
				RestoreID:          "unknown",
				Step:               StepFailed,
				ArgoPausedGlobally: true,
				LastError:          "argocd-application-controller replicas=0 but no restore-gui state; Argo is fully paused",
			}
			resumed := false
			st.ArgoSyncResumed = &resumed
			return []State{st}, nil
		}
		return nil, nil
	}
	var st State
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		st = State{
			RestoreID: "unknown",
			Step:      StepFailed,
			LastError: "corrupt restore state on argo controller: " + err.Error(),
		}
	}
	st.ArgoPausedGlobally = reps == 0
	if st.Step != StepDone {
		resumed := reps > 0
		st.ArgoSyncResumed = &resumed
		if reps == 0 && st.LastError == "" {
			st.LastError = "restore interrupted; Argo application-controller still scaled to 0"
		}
		if reps > 0 {
			// Controller already up — stale annotation only.
			st.LastError = "stale restore annotation on controller but Argo is running; clear annotation"
			_ = o.Clients.SetArgoControllerStateAnnotation(ctx, o.ArgoNS, "")
			return nil, nil
		}
	}
	o.mu.Lock()
	if st.RestoreID != "" {
		cp := st
		o.jobs[st.RestoreID] = &cp
		o.mu.Unlock()
		o.persist(&cp)
	} else {
		o.mu.Unlock()
	}
	return []State{st}, nil
}

func unstructuredString(obj map[string]any, fields ...string) (string, bool, error) {
	var cur any = obj
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false, nil
		}
		cur, ok = m[f]
		if !ok {
			return "", false, nil
		}
	}
	s, ok := cur.(string)
	return s, ok, nil
}

func nestedSlice(obj map[string]any, fields ...string) ([]any, bool, error) {
	var cur any = obj
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		cur, ok = m[f]
		if !ok {
			return nil, false, nil
		}
	}
	s, ok := cur.([]any)
	return s, ok, nil
}

func joinErr(prev string, err error) string {
	if prev == "" {
		return err.Error()
	}
	return prev + "; " + err.Error()
}

// SourcePVCCandidates derives the PVC names a snapshot was taken from out of
// its spec.paths (K8up mounts each PVC at /data/<pvcName> in the backup pod).
// File-like entries (e.g. application-level .sql dumps) are skipped — those
// are not PVC backups and cannot be restored onto a volume.
func SourcePVCCandidates(paths []string) []string {
	var out []string
	for _, p := range paths {
		segs := strings.Split(strings.Trim(p, "/"), "/")
		if len(segs) == 0 {
			continue
		}
		base := segs[len(segs)-1]
		if base == "" || strings.HasSuffix(base, ".sql") {
			continue
		}
		if !containsString(out, base) {
			out = append(out, base)
		}
	}
	return out
}

func snapshotPaths(obj map[string]any) []string {
	raw, ok, _ := nestedAny(obj, "spec", "paths")
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func nestedAny(obj map[string]any, fields ...string) (any, bool, error) {
	var cur any = obj
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		cur, ok = m[f]
		if !ok {
			return nil, false, nil
		}
	}
	return cur, true, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Cancel aborts an in-flight restore. The run loop's context is cancelled; its
// cleanup path deletes the Restore CR, scales the workload back up, and resumes
// the Argo controller (the same defer that runs on any failure).
func (o *Orchestrator) Cancel(id, actor string) error {
	o.mu.Lock()
	st, ok := o.jobs[id]
	cancel := o.cancels[id]
	if !ok {
		o.mu.Unlock()
		return fmt.Errorf("restore %s not found", id)
	}
	if st.Step == StepDone || st.Step == StepFailed {
		o.mu.Unlock()
		return fmt.Errorf("restore %s already finished", id)
	}
	if cancel == nil {
		o.mu.Unlock()
		return fmt.Errorf("restore %s is not cancellable (no active run)", id)
	}
	if st.CancelRequested {
		o.mu.Unlock()
		return nil
	}
	st.CancelRequested = true
	cp := *st
	o.mu.Unlock()
	o.persist(&cp)
	o.publish(&cp)
	o.emitLog(id, fmt.Sprintf("··· cancel requested by %s — aborting restore", actor))
	cancel()
	if o.Audit != nil {
		_, _ = o.Audit.Insert(context.Background(), audit.Entry{
			Kind:      "restore",
			Actor:     actor,
			Namespace: cp.PVCNamespace,
			PVC:       cp.PVCName,
			Snapshot:  cp.SnapshotID,
			RestoreID: id,
			Status:    "cancel_requested",
		})
	}
	return nil
}

var progressRe = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)\s*%`)

// noteProgress parses best-effort completion percentages out of restic job log
// lines and publishes them (throttled to whole-percent changes).
func (o *Orchestrator) noteProgress(restoreID, line string) {
	m := progressRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	pct, err := strconv.ParseFloat(m[1], 64)
	if err != nil || pct < 0 || pct > 100 {
		return
	}
	o.mu.Lock()
	st, ok := o.jobs[restoreID]
	if !ok || st.Step != StepRestoring {
		o.mu.Unlock()
		return
	}
	prev := -1.0
	if st.ProgressPercent != nil {
		prev = *st.ProgressPercent
	}
	// Progress can only move forward; skip sub-percent noise.
	if pct < prev || (pct-prev) < 1 {
		o.mu.Unlock()
		return
	}
	st.ProgressPercent = &pct
	cp := *st
	o.mu.Unlock()
	o.publish(&cp)
}
