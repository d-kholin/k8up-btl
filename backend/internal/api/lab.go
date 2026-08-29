package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/auth"
	"github.com/d-kholin/k8up-gui/internal/lab"
	"github.com/d-kholin/k8up-gui/internal/resticcmd"
)

// Restore Lab endpoints (docs/restore-lab.md). All lab mutations go through
// the lab.Manager, which owns the single-lab slot and only ever touches the
// lab namespace.

// WireLab attaches the lab manager and injects the restic-credential-owning
// closures (same pattern as the orchestrator wiring in NewServer).
func (s *Server) WireLab(m *lab.Manager) {
	s.Lab = m
	if m == nil {
		return
	}
	m.DumpSnapshot = func(ctx context.Context, snapNS, snapName, filePath string, w io.Writer) error {
		repo, snapID, err := s.repoEnvForSnapshot(ctx, snapNS, snapName)
		if err != nil {
			return err
		}
		return s.Restic.Dump(ctx, repo, snapID, filePath, w, resticcmd.DumpOptions{})
	}
	m.ResolveRestoreCommand = s.Orch.ResolveRestoreCommand
	m.OnUpdate = func(st lab.State) {
		b, _ := json.Marshal(map[string]any{"type": "lab", "data": st})
		s.broadcast(b)
	}
	m.OnLog = func(labID, line string) {
		b, _ := json.Marshal(map[string]any{"type": "lab-log", "labId": labID, "line": line})
		s.broadcast(b)
	}
}

func (s *Server) labEnabled() bool { return s.Lab != nil && s.Lab.Enabled() }

// handleLab returns the lab feature state, the active lab, and recent runs.
func (s *Server) handleLab(w http.ResponseWriter, _ *http.Request) {
	out := map[string]any{
		"enabled":   s.labEnabled(),
		"namespace": "",
		"labs":      []lab.State{},
	}
	if s.Lab != nil {
		out["namespace"] = s.Lab.LabNamespace
		labs := s.Lab.List()
		if labs == nil {
			labs = []lab.State{}
		}
		out["labs"] = labs
		if cur := s.Lab.Current(); cur != nil {
			out["current"] = cur
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLabPlan(w http.ResponseWriter, r *http.Request) {
	if !s.labEnabled() {
		http.Error(w, "restore lab is not enabled (RESTORE_LAB_NAMESPACE unset)", http.StatusServiceUnavailable)
		return
	}
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		http.Error(w, "namespace query parameter required", http.StatusBadRequest)
		return
	}
	var before *time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "before must be RFC3339", http.StatusBadRequest)
			return
		}
		before = &t
	}
	plan, err := s.Lab.Plan(r.Context(), ns, before)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleLabStart(w http.ResponseWriter, r *http.Request) {
	if !s.labEnabled() {
		http.Error(w, "restore lab is not enabled (RESTORE_LAB_NAMESPACE unset)", http.StatusServiceUnavailable)
		return
	}
	u, _ := auth.FromContext(r.Context())
	var req lab.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	req.Actor = u.Username
	st, err := s.Lab.Start(r.Context(), req)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	// No "started" drill audit row on purpose: drill entries are outcomes
	// (success/failed) so the verification panel never shows a run in
	// progress as the latest evidence.
	writeJSON(w, http.StatusAccepted, st)
}

func (s *Server) handleLabTeardown(w http.ResponseWriter, r *http.Request) {
	if s.Lab == nil {
		http.Error(w, "restore lab is not enabled", http.StatusServiceUnavailable)
		return
	}
	u, _ := auth.FromContext(r.Context())
	if err := s.Lab.Teardown(r.PathValue("id"), u.Username); err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "tearing_down"})
}

func (s *Server) handleLabExtend(w http.ResponseWriter, r *http.Request) {
	if s.Lab == nil {
		http.Error(w, "restore lab is not enabled", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Hours int `json:"hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	st, err := s.Lab.Extend(r.PathValue("id"), body.Hours)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleLabVerdict records the operator's pass/fail judgement plus note.
func (s *Server) handleLabVerdict(w http.ResponseWriter, r *http.Request) {
	if s.Lab == nil {
		http.Error(w, "restore lab is not enabled", http.StatusServiceUnavailable)
		return
	}
	u, _ := auth.FromContext(r.Context())
	var body struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if len(body.Note) > 2000 {
		http.Error(w, "note too long (max 2000 chars)", http.StatusBadRequest)
		return
	}
	st, err := s.Lab.SetVerdict(r.PathValue("id"), body.Status, body.Note, u.Username)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleLabLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	lines := []string{}
	if s.Lab != nil && s.Lab.Logs != nil {
		lines = s.Lab.Logs.Snapshot(id)
	}
	if len(lines) == 0 && s.Audit != nil {
		if persisted, err := s.Audit.GetLabLogs(r.Context(), id); err == nil && len(persisted) > 0 {
			lines = persisted
			if s.Lab != nil && s.Lab.Logs != nil {
				s.Lab.Logs.Seed(id, persisted)
			}
		}
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"labId": id, "lines": lines})
}

// handleLabVerified serves the per-namespace drill evidence for the
// "last verified restore" dashboard panel.
func (s *Server) handleLabVerified(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Audit.LatestDrillPerNamespace(r.Context())
	if err != nil {
		s.writeErr(w, err, http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []audit.DrillStatus{}
	}
	writeJSON(w, http.StatusOK, rows)
}
