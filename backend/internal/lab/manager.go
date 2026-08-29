// Package lab orchestrates the Restore Lab (docs/restore-lab.md): restore
// snapshots into an isolated, network-locked namespace and — for promoted
// apps — deploy the app there from its git source so restores can be tested
// against a running instance. Labs never touch a source namespace, never
// pause Argo, and are torn down (PVCs and retained PVs included) after a TTL.
package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/k8s"
	"github.com/d-kholin/k8up-gui/internal/restore"
)

type Step string

const (
	StepQueued       Step = "queued"
	StepCreatingPVCs Step = "creating_pvcs"
	StepRestoring    Step = "restoring"
	StepDeploying    Step = "deploying"
	StepRestoringDB  Step = "restoring_db"
	StepInspecting   Step = "inspecting"
	StepReady        Step = "ready"
	StepTearingDown  Step = "tearing_down"
	StepDeleted      Step = "deleted"
	StepFailed       Step = "failed"
)

const (
	TierData = "data"
	TierApp  = "app"
)

// PVCPart is one snapshot→PVC restore inside a lab run.
type PVCPart struct {
	PVCName       string `json:"pvcName"`
	SnapshotName  string `json:"snapshotName"`
	SnapshotID    string `json:"snapshotId"`
	RestoreCRName string `json:"restoreCRName,omitempty"`
	Status        string `json:"status,omitempty"` // pending | running | done | failed
}

// State is a lab run, durable in SQLite.
type State struct {
	LabID           string     `json:"labId"`
	Tier            string     `json:"tier"` // data | app
	SourceNamespace string     `json:"sourceNamespace"`
	LabNamespace    string     `json:"labNamespace"`
	AppName         string     `json:"appName,omitempty"`      // source Argo Application
	CloneAppName    string     `json:"cloneAppName,omitempty"` // lab-<ns>
	Step            Step       `json:"step"`
	PVCs            []PVCPart  `json:"pvcs,omitempty"`
	DumpSnapshot    string     `json:"dumpSnapshot,omitempty"`
	DumpSnapshotID  string     `json:"dumpSnapshotId,omitempty"`
	DumpPath        string     `json:"dumpPath,omitempty"`
	DBPod           string     `json:"dbPod,omitempty"`
	RestoreCommand  string     `json:"restoreCommand,omitempty"`
	InspectService  string     `json:"inspectService,omitempty"`
	Health          string     `json:"health,omitempty"`
	StartedAt       time.Time  `json:"startedAt"`
	ReadyAt         *time.Time `json:"readyAt,omitempty"`
	HealthyAt       *time.Time `json:"healthyAt,omitempty"` // clone Synced+Healthy — the RTO actual
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"` // teardown complete (or failed terminal)
	LastError       string     `json:"lastError,omitempty"`
	Actor           string     `json:"actor,omitempty"`
	TornDownBy      string     `json:"tornDownBy,omitempty"` // username or "ttl"
}

// Active reports whether the lab still occupies the single-lab slot: only a
// fully deleted lab frees it (a failed lab must be torn down first).
func (s *State) Active() bool { return s.Step != StepDeleted }

type Request struct {
	SourceNamespace string `json:"sourceNamespace"`
	Tier            string `json:"tier"`
	// Data tier: the one snapshot/PVC to restore.
	SnapshotName string `json:"snapshotName,omitempty"`
	PVCName      string `json:"pvcName,omitempty"`
	// TTLHours overrides the default lab lifetime (0 = default).
	TTLHours int    `json:"ttlHours,omitempty"`
	Actor    string `json:"-"`
}

type Manager struct {
	Clients       *k8s.Clients
	Audit         *audit.Store
	Log           *slog.Logger
	Logs          *restore.LogHub
	LabNamespace  string
	ArgoNamespace string
	Project       string
	InspectImage  string
	DefaultTTL    time.Duration
	// RestoreTimeout bounds each Restore CR; DeployTimeout the clone
	// Application reaching Healthy; TeardownTimeout the cascade delete.
	RestoreTimeout  time.Duration
	DeployTimeout   time.Duration
	TeardownTimeout time.Duration

	// DumpSnapshot streams one file out of a snapshot (wired by the API
	// server, which owns restic credential resolution).
	DumpSnapshot func(ctx context.Context, snapNS, snapName, path string, w io.Writer) error
	// ResolveRestoreCommand picks the git-sourced command the SQL leg pipes
	// into (wired to the restore orchestrator's resolver).
	ResolveRestoreCommand func(db *k8s.DBPod) (cmd, source string, err error)
	OnUpdate              func(State)
	OnLog                 func(labID, line string)

	mu   sync.Mutex
	jobs map[string]*State
}

func NewManager(c *k8s.Clients, store *audit.Store, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		Clients:         c,
		Audit:           store,
		Log:             log,
		Logs:            restore.NewLogHub(),
		DefaultTTL:      24 * time.Hour,
		RestoreTimeout:  2 * time.Hour,
		DeployTimeout:   20 * time.Minute,
		TeardownTimeout: 15 * time.Minute,
		jobs:            map[string]*State{},
	}
}

func (m *Manager) Enabled() bool { return m != nil && m.LabNamespace != "" && m.Clients != nil }

func (m *Manager) Get(id string) (*State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	cp := *s
	return &cp, true
}

// List returns labs newest-first.
func (m *Manager) List() []State {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]State, 0, len(m.jobs))
	for _, s := range m.jobs {
		out = append(out, *s)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].StartedAt.After(out[i].StartedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Current returns the lab occupying the single-lab slot, if any.
func (m *Manager) Current() *State {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.jobs {
		if s.Active() {
			cp := *s
			return &cp
		}
	}
	return nil
}

func (m *Manager) set(s *State) {
	m.mu.Lock()
	m.jobs[s.LabID] = s
	cp := *s
	m.mu.Unlock()
	m.persist(&cp)
	if m.OnUpdate != nil {
		m.OnUpdate(cp)
	}
}

func (m *Manager) persist(s *State) {
	if m.Audit == nil || s.LabID == "" {
		return
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := m.Audit.UpsertLabJob(context.Background(), s.LabID, b); err != nil {
		m.Log.Warn("persist lab job", "labId", s.LabID, "err", err)
	}
}

func (m *Manager) emitLog(labID, line string) {
	if m.Logs != nil {
		m.Logs.Append(labID, line)
	}
	if m.Audit != nil {
		_ = m.Audit.AppendLabLog(context.Background(), labID, line)
	}
	if m.OnLog != nil {
		m.OnLog(labID, line)
	}
}

// LoadPersisted hydrates lab runs from SQLite. Runs that were mid-flight when
// the process died are marked failed — their cluster resources may exist, so
// they are NOT auto-torn-down; the operator (or the TTL loop for ready labs)
// drives teardown.
func (m *Manager) LoadPersisted(ctx context.Context) error {
	if m.Audit == nil {
		return nil
	}
	blobs, err := m.Audit.ListLabJobs(ctx, 100)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, raw := range blobs {
		var st State
		if err := json.Unmarshal(raw, &st); err != nil || st.LabID == "" {
			continue
		}
		switch st.Step {
		case StepReady, StepDeleted, StepFailed:
			// keep as-is
		default:
			st.LastError = "lab interrupted by process restart — tear it down and start again"
			st.Step = StepFailed
			if b, err := json.Marshal(&st); err == nil {
				_ = m.Audit.UpsertLabJob(ctx, st.LabID, b)
			}
		}
		cp := st
		m.jobs[st.LabID] = &cp
		if lines, err := m.Audit.GetLabLogs(ctx, st.LabID); err == nil && len(lines) > 0 && m.Logs != nil {
			m.Logs.Seed(st.LabID, lines)
		}
	}
	return nil
}

// Run drives TTL expiry; call as a goroutine (Recorder pattern).
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now().UTC()
		for _, st := range m.List() {
			if st.Step == StepReady && st.ExpiresAt != nil && now.After(*st.ExpiresAt) {
				m.Log.Info("lab TTL expired; tearing down", "labId", st.LabID, "source", st.SourceNamespace)
				if err := m.Teardown(st.LabID, "ttl"); err != nil {
					m.Log.Warn("ttl teardown", "labId", st.LabID, "err", err)
				}
			}
		}
	}
}

// Extend pushes a ready lab's expiry out by n hours.
func (m *Manager) Extend(id string, hours int) (*State, error) {
	if hours <= 0 || hours > 24*7 {
		return nil, fmt.Errorf("hours must be 1..168")
	}
	m.mu.Lock()
	st, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("lab %s not found", id)
	}
	if st.Step != StepReady || st.ExpiresAt == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("lab %s is not ready (step %s); only ready labs can be extended", id, st.Step)
	}
	exp := st.ExpiresAt.Add(time.Duration(hours) * time.Hour)
	st.ExpiresAt = &exp
	cp := *st
	m.mu.Unlock()
	m.set(&cp)
	m.emitLog(id, fmt.Sprintf("··· TTL extended by %dh — new expiry %s", hours, exp.Format(time.RFC3339)))
	return &cp, nil
}

// Start validates a lab request, claims the single-lab slot, and provisions
// asynchronously.
func (m *Manager) Start(ctx context.Context, req Request) (*State, error) {
	if !m.Enabled() {
		return nil, fmt.Errorf("restore lab is not enabled (set RESTORE_LAB_NAMESPACE and deploy deploy/k8s/restore-lab)")
	}
	if req.SourceNamespace == "" {
		return nil, fmt.Errorf("sourceNamespace is required")
	}
	if req.SourceNamespace == m.LabNamespace {
		return nil, fmt.Errorf("source namespace cannot be the lab namespace")
	}
	if req.Tier != TierData && req.Tier != TierApp {
		return nil, fmt.Errorf("tier must be %q or %q", TierData, TierApp)
	}
	if ok, err := m.Clients.NamespaceExists(ctx, m.LabNamespace); err != nil {
		return nil, fmt.Errorf("check lab namespace: %w", err)
	} else if !ok {
		return nil, fmt.Errorf("lab namespace %q does not exist — deploy deploy/k8s/restore-lab first", m.LabNamespace)
	}

	ttl := m.DefaultTTL
	if req.TTLHours > 0 {
		if req.TTLHours > 24*7 {
			return nil, fmt.Errorf("ttlHours must be 1..168")
		}
		ttl = time.Duration(req.TTLHours) * time.Hour
	}

	id := uuid.NewString()
	st := &State{
		LabID:           id,
		Tier:            req.Tier,
		SourceNamespace: req.SourceNamespace,
		LabNamespace:    m.LabNamespace,
		Step:            StepQueued,
		StartedAt:       time.Now().UTC(),
		Actor:           req.Actor,
	}

	var plan *Plan
	switch req.Tier {
	case TierData:
		if req.SnapshotName == "" || req.PVCName == "" {
			return nil, fmt.Errorf("data-tier labs need snapshotName and pvcName")
		}
		snap, err := m.Clients.GetSnapshot(ctx, req.SourceNamespace, req.SnapshotName)
		if err != nil {
			return nil, fmt.Errorf("get snapshot: %w", err)
		}
		paths := snapshotPaths(snap.Object)
		candidates := restore.SourcePVCCandidates(paths)
		if !contains(candidates, req.PVCName) {
			return nil, fmt.Errorf("PVC %q is not a source of snapshot %s/%s", req.PVCName, req.SourceNamespace, req.SnapshotName)
		}
		st.PVCs = []PVCPart{{
			PVCName:      req.PVCName,
			SnapshotName: req.SnapshotName,
			SnapshotID:   snapshotID(snap.Object, req.SnapshotName),
			Status:       "pending",
		}}
	case TierApp:
		var err error
		plan, err = m.Plan(ctx, req.SourceNamespace)
		if err != nil {
			return nil, err
		}
		if plan.App == nil {
			return nil, fmt.Errorf("app lab unavailable for %s: %s", req.SourceNamespace, plan.AppError)
		}
		st.AppName = plan.App.Name
		st.CloneAppName = "lab-" + req.SourceNamespace
		for _, p := range plan.PVCs {
			if !p.SourceExists {
				continue
			}
			st.PVCs = append(st.PVCs, PVCPart{PVCName: p.PVCName, SnapshotName: p.SnapshotName, SnapshotID: p.SnapshotID, Status: "pending"})
		}
		if plan.Dump != nil {
			st.DumpSnapshot = plan.Dump.SnapshotName
			st.DumpSnapshotID = plan.Dump.SnapshotID
			st.DumpPath = plan.Dump.Path
			if m.DumpSnapshot == nil || m.ResolveRestoreCommand == nil {
				return nil, fmt.Errorf("SQL dump replay is not configured on this server")
			}
		}
		if len(st.PVCs) == 0 && st.DumpSnapshot == "" {
			return nil, fmt.Errorf("nothing restorable found for %s", req.SourceNamespace)
		}
	}

	// Claim the single-lab slot atomically.
	m.mu.Lock()
	for _, other := range m.jobs {
		if other.Active() {
			m.mu.Unlock()
			return nil, fmt.Errorf("lab %s (%s, step %s) is still active; one lab at a time — tear it down first", other.LabID, other.SourceNamespace, other.Step)
		}
	}
	m.jobs[id] = st
	m.mu.Unlock()

	exp := st.StartedAt.Add(ttl)
	st.ExpiresAt = &exp
	m.set(st)
	go m.run(st)
	return st, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
