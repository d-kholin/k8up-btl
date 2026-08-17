package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/d-kholin/k8up-gui/internal/resticcmd"
)

// diffMaxChanges caps the change list in one response; the stats block still
// reflects the full diff.
const diffMaxChanges = 5000

// handleSnapshotDiff compares the snapshot in the path (target, newer) against
// ?base=<snapshot CR name> (older) in the same namespace and repository.
func (s *Server) handleSnapshotDiff(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	baseName := r.URL.Query().Get("base")
	if baseName == "" {
		http.Error(w, "base query parameter required (older snapshot name)", http.StatusBadRequest)
		return
	}
	if baseName == name {
		http.Error(w, "base and target snapshots are identical", http.StatusBadRequest)
		return
	}

	targetRepo, targetID, err := s.repoEnvForSnapshot(r.Context(), ns, name)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	baseRepo, baseID, err := s.repoEnvForSnapshot(r.Context(), ns, baseName)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	if repoURL(baseRepo) != repoURL(targetRepo) {
		s.writeErr(w, fmt.Errorf("snapshots live in different repositories and cannot be compared"), http.StatusBadRequest)
		return
	}

	res, err := s.Restic.Diff(r.Context(), targetRepo, baseID, targetID)
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}

	changes := res.Changes
	truncated := false
	if len(changes) > diffMaxChanges {
		changes = changes[:diffMaxChanges]
		truncated = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"base":         map[string]string{"name": baseName, "id": baseID},
		"target":       map[string]string{"name": name, "id": targetID},
		"changedFiles": res.ChangedFiles,
		"added":        res.Added,
		"removed":      res.Removed,
		"changes":      changes,
		"totalChanges": len(res.Changes),
		"truncated":    truncated,
	})
}

func repoURL(repo resticcmd.RepoEnv) string {
	for _, e := range repo.Env {
		if strings.HasPrefix(e, "RESTIC_REPOSITORY=") {
			return strings.TrimPrefix(e, "RESTIC_REPOSITORY=")
		}
	}
	return ""
}
