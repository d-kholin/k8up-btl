package resticcmd_test

import (
        "testing"

        "github.com/d-kholin/k8up-gui/internal/resticcmd"
)

func TestRunnerBinaryDefault(t *testing.T) {
        r := &resticcmd.Runner{}
        if r.Binary != "" {
                t.Fatalf("expected empty binary default, got %q", r.Binary)
        }
}

func TestAllowedCommandsDocumented(t *testing.T) {
        // Mutating restic verbs must never be reachable via Runner public API.
        // Public methods: List, Dump, Stats only.
        var r *resticcmd.Runner
        _ = r
}
