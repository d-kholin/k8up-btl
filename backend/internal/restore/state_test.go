package restore_test

import (
        "encoding/json"
        "testing"

        "github.com/d-kholin/k8up-gui/internal/restore"
)

func TestStateJSONRoundTrip(t *testing.T) {
        st := restore.State{
                RestoreID:        "abc",
                Step:             restore.StepScalingDown,
                SnapshotID:       "snap1",
                PVCNamespace:     "app",
                PVCName:          "data",
                OriginalReplicas: 2,
        }
        b, err := json.Marshal(st)
        if err != nil {
                t.Fatal(err)
        }
        var out restore.State
        if err := json.Unmarshal(b, &out); err != nil {
                t.Fatal(err)
        }
        if out.RestoreID != st.RestoreID || out.OriginalReplicas != 2 || out.Step != restore.StepScalingDown {
                t.Fatalf("roundtrip mismatch: %+v", out)
        }
}
