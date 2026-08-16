package restore

import (
        "context"
        "encoding/json"
        "fmt"
        "log/slog"
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

// State is durable (annotation) + in-memory job status.
type State struct {
        RestoreID          string         `json:"restoreId"`
        Application        *k8s.ArgoRef   `json:"application,omitempty"`
        OriginalSyncPolicy map[string]any `json:"originalSyncPolicy,omitempty"`
        Workload           *k8s.WorkloadRef `json:"workload,omitempty"`
        OriginalReplicas   int32          `json:"originalReplicas"`
        Step               Step           `json:"step"`
        SnapshotID         string         `json:"snapshotId"`
        SnapshotCR         string         `json:"snapshotCR,omitempty"`
        PVCNamespace       string         `json:"pvcNamespace"`
        PVCName            string         `json:"pvcName"`
        RestoreCRName      string         `json:"restoreCRName,omitempty"`
        StartedAt          time.Time      `json:"startedAt"`
        FinishedAt         *time.Time     `json:"finishedAt,omitempty"`
        LastError          string         `json:"lastError,omitempty"`
        Actor              string         `json:"actor,omitempty"`
        ArgoSyncResumed    *bool          `json:"argoSyncResumed,omitempty"`
        BytesRecovered     int64          `json:"bytesRecovered,omitempty"`
}

type Request struct {
        SnapshotNamespace string `json:"snapshotNamespace"`
        SnapshotName      string `json:"snapshotName"` // Snapshot CR name
        SnapshotID        string `json:"snapshotId"`   // restic id if known
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
        OnUpdate         func(State)

        mu    sync.Mutex
        jobs  map[string]*State
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
                jobs:             map[string]*State{},
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
        return out
}

func (o *Orchestrator) publish(s *State) {
        if o.OnUpdate != nil {
                cp := *s
                o.OnUpdate(cp)
        }
}

func (o *Orchestrator) set(s *State) {
        o.mu.Lock()
        o.jobs[s.RestoreID] = s
        o.mu.Unlock()
        o.publish(s)
        if o.Clients != nil && s.Application != nil {
                b, _ := json.Marshal(s)
                // best-effort durable update
                _ = o.Clients.SetPausedStateAnnotation(context.Background(), s.Application, string(b))
        }
}

// Start begins async restore orchestration and returns the job id immediately.
func (o *Orchestrator) Start(ctx context.Context, req Request) (*State, error) {
        if o.Clients == nil {
                return nil, fmt.Errorf("kubernetes client not configured")
        }
        if req.PVCNamespace == "" || req.PVCName == "" {
                return nil, fmt.Errorf("pvcNamespace and pvcName are required")
        }
        id := uuid.NewString()
        st := &State{
                RestoreID:    id,
                Step:         StepQueued,
                SnapshotID:   req.SnapshotID,
                SnapshotCR:   req.SnapshotName,
                PVCNamespace: req.PVCNamespace,
                PVCName:      req.PVCName,
                StartedAt:    time.Now().UTC(),
                Actor:        req.Actor,
        }
        // Resolve snapshot id from CR if needed
        if st.SnapshotID == "" && req.SnapshotName != "" {
                snap, err := o.Clients.GetSnapshot(ctx, req.SnapshotNamespace, req.SnapshotName)
                if err != nil {
                        return nil, fmt.Errorf("get snapshot: %w", err)
                }
                if id, ok, _ := unstructuredString(snap.Object, "spec", "id"); ok {
                        st.SnapshotID = id
                } else if id, ok, _ := unstructuredString(snap.Object, "status", "id"); ok {
                        st.SnapshotID = id
                } else {
                        // K8up Snapshot often uses status.snapshot or metadata
                        if id, ok, _ := unstructuredString(snap.Object, "spec", "snapshot"); ok {
                                st.SnapshotID = id
                        }
                }
                if st.SnapshotID == "" {
                        // fall back to CR name — operator-specific
                        st.SnapshotID = req.SnapshotName
                }
        }
        if st.SnapshotID == "" {
                return nil, fmt.Errorf("snapshotId or snapshotName required")
        }

        o.set(st)
        go o.run(st)
        return st, nil
}

func (o *Orchestrator) run(st *State) {
        ctx := context.Background()
        var runErr error
        argoPaused := false

        defer func() {
                // CRITICAL: always attempt Argo resume if we paused.
                if st.Application != nil && argoPaused {
                        st.Step = StepResumingArgo
                        o.set(st)
                        if err := o.Clients.ResumeArgoSync(ctx, st.Application, st.OriginalSyncPolicy); err != nil {
                                o.Log.Error("failed to resume argo sync", "app", st.Application.Name, "err", err)
                                st.LastError = joinErr(st.LastError, fmt.Errorf("resume argo: %w", err))
                                resumed := false
                                st.ArgoSyncResumed = &resumed
                        } else {
                                resumed := true
                                st.ArgoSyncResumed = &resumed
                                argoPaused = false
                        }
                } else if st.Application == nil {
                        // n/a
                } else if !argoPaused {
                        resumed := true
                        st.ArgoSyncResumed = &resumed
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
                o.mu.Unlock()
                o.publish(st)

                // Clear annotation on success/resume; keep on failed-still-paused
                if st.Application != nil {
                        if st.ArgoSyncResumed != nil && *st.ArgoSyncResumed {
                                _ = o.Clients.SetPausedStateAnnotation(ctx, st.Application, "")
                        } else {
                                b, _ := json.Marshal(st)
                                _ = o.Clients.SetPausedStateAnnotation(ctx, st.Application, string(b))
                        }
                }

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
                        argoName := ""
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
        wl, err := o.Clients.ResolvePVCOwner(ctx, st.PVCNamespace, st.PVCName)
        if err != nil {
                runErr = err
                return
        }
        st.Workload = wl
        replicas, err := o.Clients.GetReplicas(ctx, wl)
        if err != nil {
                runErr = err
                return
        }
        st.OriginalReplicas = replicas

        argo, policy, err := o.Clients.ResolveArgoApp(ctx, wl, o.ArgoNS)
        if err != nil && argo == nil {
                runErr = err
                return
        }
        // If argo ref found but get failed, abort rather than scale without pause ability
        if err != nil && argo != nil {
                runErr = err
                return
        }
        if argo != nil {
                st.Application = argo
                st.OriginalSyncPolicy = policy
                st.Step = StepPausingArgo
                o.set(st)
                b, _ := json.Marshal(st)
                // PauseArgoSync also writes annotation
                orig, err := o.Clients.PauseArgoSync(ctx, argo, string(b))
                if err != nil {
                        runErr = fmt.Errorf("pause argo: %w", err)
                        return
                }
                if st.OriginalSyncPolicy == nil {
                        st.OriginalSyncPolicy = orig
                }
                argoPaused = true
                o.set(st)
        }

        // 3. Scale down
        st.Step = StepScalingDown
        o.set(st)
        scaleCtx, cancel := context.WithTimeout(ctx, o.ScaleDownTimeout)
        defer cancel()
        if err := o.Clients.Scale(scaleCtx, wl, 0); err != nil {
                runErr = fmt.Errorf("scale down: %w", err)
                return
        }
        if err := o.Clients.WaitPodsGone(scaleCtx, wl); err != nil {
                runErr = fmt.Errorf("wait pods gone: %w", err)
                return
        }

        // 4. Restore CR
        st.Step = StepRestoring
        crName := fmt.Sprintf("gui-restore-%s", st.RestoreID[:8])
        st.RestoreCRName = crName
        o.set(st)

        // backend: leave empty to inherit from operator defaults when possible;
        // production clusters usually need backend from Schedule — refined later.
        if _, err := o.Clients.CreateRestoreCR(ctx, st.PVCNamespace, crName, st.SnapshotID, st.PVCName, nil); err != nil {
                runErr = fmt.Errorf("create restore cr: %w", err)
                return
        }

        restoreCtx, cancelR := context.WithTimeout(ctx, o.RestoreTimeout)
        defer cancelR()
        if err := o.waitRestoreComplete(restoreCtx, st.PVCNamespace, crName); err != nil {
                runErr = fmt.Errorf("restore: %w", err)
                return
        }

        // 5. Scale up
        st.Step = StepScalingUp
        o.set(st)
        if err := o.Clients.Scale(ctx, wl, st.OriginalReplicas); err != nil {
                runErr = fmt.Errorf("scale up: %w", err)
                return
        }
        // step 6 handled in defer
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
                // K8up conditions vary by version; check common fields
                if conds, found, _ := nestedSlice(obj.Object, "status", "conditions"); found {
                        for _, c := range conds {
                                m, ok := c.(map[string]any)
                                if !ok {
                                        continue
                                }
                                t, _ := m["type"].(string)
                                st, _ := m["status"].(string)
                                if (t == "Completed" || t == "Ready") && st == "True" {
                                        return nil
                                }
                                if t == "Failed" && st == "True" {
                                        msg, _ := m["message"].(string)
                                        return fmt.Errorf("restore failed: %s", msg)
                                }
                        }
                }
                if prog, ok, _ := unstructuredString(obj.Object, "status", "phase"); ok {
                        switch prog {
                        case "Completed", "Succeeded":
                                return nil
                        case "Failed", "Error":
                                return fmt.Errorf("restore phase %s", prog)
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

// ManualResumeArgo clears a stuck pause using durable annotation or provided policy.
func (o *Orchestrator) ManualResumeArgo(ctx context.Context, appNS, appName string) error {
        ref := &k8s.ArgoRef{Namespace: appNS, Name: appName}
        apps, err := o.Clients.ListInterruptedRestores(ctx, appNS)
        if err != nil {
                return err
        }
        var policy map[string]any
        for i := range apps {
                if apps[i].GetName() != appName {
                        continue
                }
                raw := apps[i].GetAnnotations()[k8s.PausedStateAnnotation]
                var st State
                if err := json.Unmarshal([]byte(raw), &st); err == nil {
                        policy = st.OriginalSyncPolicy
                }
        }
        if err := o.Clients.ResumeArgoSync(ctx, ref, policy); err != nil {
                return err
        }
        if o.Audit != nil {
                _, _ = o.Audit.Insert(ctx, audit.Entry{
                        Kind:    "system",
                        Actor:   "operator",
                        Status:  "resumed_argo",
                        ArgoApp: appNS + "/" + appName,
                        Detail:  "manual resume",
                })
        }
        return nil
}

// ScanInterrupted loads annotation state into memory on startup.
func (o *Orchestrator) ScanInterrupted(ctx context.Context) ([]State, error) {
        apps, err := o.Clients.ListInterruptedRestores(ctx, o.ArgoNS)
        if err != nil {
                return nil, err
        }
        var out []State
        for i := range apps {
                raw := apps[i].GetAnnotations()[k8s.PausedStateAnnotation]
                var st State
                if err := json.Unmarshal([]byte(raw), &st); err != nil {
                        st = State{
                                RestoreID: "unknown",
                                Step:      StepFailed,
                                LastError: "corrupt paused-state annotation",
                                Application: &k8s.ArgoRef{
                                        Namespace: apps[i].GetNamespace(),
                                        Name:      apps[i].GetName(),
                                },
                        }
                }
                if st.Application == nil {
                        st.Application = &k8s.ArgoRef{Namespace: apps[i].GetNamespace(), Name: apps[i].GetName()}
                }
                // mark as interrupted for UI
                if st.Step != StepDone {
                        resumed := false
                        st.ArgoSyncResumed = &resumed
                        if st.LastError == "" {
                                st.LastError = "restore was interrupted; Argo sync still paused"
                        }
                }
                o.mu.Lock()
                if st.RestoreID != "" {
                        o.jobs[st.RestoreID] = &st
                }
                o.mu.Unlock()
                out = append(out, st)
        }
        return out, nil
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
