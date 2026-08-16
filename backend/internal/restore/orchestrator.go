package restore

import (
        "context"
        "encoding/json"
        "fmt"
        "log/slog"
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
        Application *k8s.ArgoRef `json:"application,omitempty"`
        Workload    *k8s.WorkloadRef `json:"workload,omitempty"`
        OriginalReplicas int32 `json:"originalReplicas"`
        // Global Argo pause: application-controller STS scaled to 0.
        ArgoControllerReplicas int32  `json:"argoControllerReplicas,omitempty"`
        ArgoPausedGlobally     bool   `json:"argoPausedGlobally,omitempty"`
        Step                   Step   `json:"step"`
        SnapshotID             string `json:"snapshotId"`
        SnapshotCR             string `json:"snapshotCR,omitempty"`
        PVCNamespace           string `json:"pvcNamespace"`
        PVCName                string `json:"pvcName"`
        RestoreCRName          string `json:"restoreCRName,omitempty"`
        StartedAt              time.Time  `json:"startedAt"`
        FinishedAt             *time.Time `json:"finishedAt,omitempty"`
        LastError              string     `json:"lastError,omitempty"`
        Actor                  string     `json:"actor,omitempty"`
        // ArgoSyncResumed: controller scaled back up (global pause cleared).
        ArgoSyncResumed *bool `json:"argoSyncResumed,omitempty"`
        BytesRecovered  int64 `json:"bytesRecovered,omitempty"`
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
        OnUpdate         func(State)

        mu   sync.Mutex
        jobs map[string]*State
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
        if o.Clients != nil && s.ArgoPausedGlobally {
                b, _ := json.Marshal(s)
                _ = o.Clients.SetArgoControllerStateAnnotation(context.Background(), o.ArgoNS, string(b))
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
        // Refuse if Argo is already paused by a prior restore.
        if raw, reps, err := o.Clients.GetArgoControllerPausedState(ctx, o.ArgoNS); err == nil {
                if raw != "" || reps == 0 {
                        return nil, fmt.Errorf("argo application-controller already paused (replicas=%d); resume or clear restore-gui state before starting another restore", reps)
                }
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
        if st.SnapshotID == "" && req.SnapshotName != "" {
                snap, err := o.Clients.GetSnapshot(ctx, req.SnapshotNamespace, req.SnapshotName)
                if err != nil {
                        return nil, fmt.Errorf("get snapshot: %w", err)
                }
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
                o.mu.Unlock()
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

        // Optional: record owning Application name for UI (not used for pause).
        if argo, _, err := o.Clients.ResolveArgoApp(ctx, wl, o.ArgoNS); err == nil && argo != nil {
                st.Application = argo
        }

        // 2. Pause ALL Argo reconciliation (scale application-controller to 0).
        st.Step = StepPausingArgo
        o.set(st)
        b, _ := json.Marshal(st)
        origCtrl, err := o.Clients.PauseArgoController(ctx, o.ArgoNS, string(b))
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

        // 3. Scale down workload
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

        // 5. Scale up workload
        st.Step = StepScalingUp
        o.set(st)
        if err := o.Clients.Scale(ctx, wl, st.OriginalReplicas); err != nil {
                runErr = fmt.Errorf("scale up: %w", err)
                return
        }
        // 6. Resume Argo controller — defer
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
        }
        o.mu.Unlock()
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
