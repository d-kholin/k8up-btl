package lab

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func snap(name, id, date string, paths ...string) unstructured.Unstructured {
	ps := make([]any, 0, len(paths))
	for _, p := range paths {
		ps = append(ps, p)
	}
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "k8up.io/v1",
		"kind":       "Snapshot",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"id":    id,
			"date":  date,
			"paths": ps,
		},
	}}
}

func TestPlanFromSnapshots(t *testing.T) {
	items := []unstructured.Unstructured{
		snap("old-a", "id-old-a", "2026-08-01T00:00:00Z", "/data/mealie-data-encrypted"),
		snap("new-a", "id-new-a", "2026-08-20T00:00:00Z", "/data/mealie-data-encrypted"),
		snap("only-b", "id-b", "2026-08-10T00:00:00Z", "/data/mealie-media-encrypted"),
		snap("dump-old", "id-dump-old", "2026-08-05T00:00:00Z", "/mealie-postgres.sql"),
		snap("dump-new", "id-dump-new", "2026-08-21T00:00:00Z", "/mealie-postgres.sql"),
	}
	pvcs, dump := planFromSnapshots(items, nil)

	if len(pvcs) != 2 {
		t.Fatalf("want 2 PVC plans, got %d: %+v", len(pvcs), pvcs)
	}
	// Sorted by PVC name.
	if pvcs[0].PVCName != "mealie-data-encrypted" || pvcs[0].SnapshotName != "new-a" || pvcs[0].SnapshotID != "id-new-a" {
		t.Fatalf("pvc[0] should be the newest mealie-data snapshot, got %+v", pvcs[0])
	}
	if pvcs[1].PVCName != "mealie-media-encrypted" || pvcs[1].SnapshotName != "only-b" {
		t.Fatalf("pvc[1] wrong: %+v", pvcs[1])
	}
	if dump == nil || dump.SnapshotName != "dump-new" || dump.Path != "/mealie-postgres.sql" {
		t.Fatalf("dump should be the newest .sql snapshot, got %+v", dump)
	}
}

func TestPlanFromSnapshotsNoDump(t *testing.T) {
	items := []unstructured.Unstructured{
		snap("a", "id-a", "2026-08-01T00:00:00Z", "/data/pvc-a"),
	}
	pvcs, dump := planFromSnapshots(items, nil)
	if dump != nil {
		t.Fatalf("want no dump, got %+v", dump)
	}
	if len(pvcs) != 1 || pvcs[0].PVCName != "pvc-a" {
		t.Fatalf("unexpected pvcs: %+v", pvcs)
	}
}

func TestPlanFromSnapshotsRestorePoint(t *testing.T) {
	items := []unstructured.Unstructured{
		snap("old-a", "id-old-a", "2026-08-01T00:00:00Z", "/data/pvc-a"),
		snap("new-a", "id-new-a", "2026-08-20T00:00:00Z", "/data/pvc-a"),
		snap("dump-old", "id-dump-old", "2026-08-05T00:00:00Z", "/x-postgres.sql"),
		snap("dump-new", "id-dump-new", "2026-08-21T00:00:00Z", "/x-postgres.sql"),
	}
	// Cutoff between the old and new snapshots → the old pair is chosen.
	before, _ := time.Parse(time.RFC3339, "2026-08-10T00:00:00Z")
	pvcs, dump := planFromSnapshots(items, &before)
	if len(pvcs) != 1 || pvcs[0].SnapshotName != "old-a" {
		t.Fatalf("want old-a at restore point, got %+v", pvcs)
	}
	if dump == nil || dump.SnapshotName != "dump-old" {
		t.Fatalf("want dump-old at restore point, got %+v", dump)
	}

	// A cutoff equal to a snapshot's own time must include that snapshot.
	exact, _ := time.Parse(time.RFC3339, "2026-08-20T00:00:00Z")
	pvcs, _ = planFromSnapshots(items, &exact)
	if len(pvcs) != 1 || pvcs[0].SnapshotName != "new-a" {
		t.Fatalf("cutoff at own timestamp must include the snapshot, got %+v", pvcs)
	}

	// Restore points: distinct times, newest first.
	pts := restorePoints(items, 60)
	if len(pts) != 4 || !pts[0].After(pts[3]) {
		t.Fatalf("unexpected restore points: %v", pts)
	}
}
