package api

import (
	"testing"

	"github.com/d-kholin/k8up-gui/internal/config"
	"github.com/d-kholin/k8up-gui/internal/restore"
)

// Go's ServeMux panics at registration time on ambiguously overlapping
// patterns (e.g. /lab/plan/{ns} vs /lab/{id}/logs). Building the handler in a
// test catches that class of bug before it takes the server down at startup.
func TestHandlerRegistersWithoutConflicts(t *testing.T) {
	orch := restore.NewOrchestrator(nil, "argocd", 0, 0, nil, nil)
	s := NewServer(config.Config{}, nil, orch, nil, nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Handler() panicked: %v", r)
		}
	}()
	if s.Handler() == nil {
		t.Fatal("nil handler")
	}
}
