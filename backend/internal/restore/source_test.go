package restore

import (
	"reflect"
	"testing"
)

func TestSourcePVCCandidates(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  []string
	}{
		{"typical k8up mount", []string{"/data/gitea-data"}, []string{"gitea-data"}},
		{"trailing slash", []string{"/data/pg-data/"}, []string{"pg-data"}},
		{"multiple pvcs", []string{"/data/media", "/data/config"}, []string{"media", "config"}},
		{"skips sql dumps", []string{"/data/db.sql", "/data/app-data"}, []string{"app-data"}},
		{"only sql dump", []string{"/data/nextcloud-postgresql.sql"}, nil},
		{"dedupes", []string{"/data/vol", "/other/vol"}, []string{"vol"}},
		{"empty", nil, nil},
		{"root only", []string{"/"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SourcePVCCandidates(tc.paths)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SourcePVCCandidates(%v) = %v, want %v", tc.paths, got, tc.want)
			}
		})
	}
}

func TestSnapshotPaths(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"paths": []any{"/data/a", "/data/b", 42},
		},
	}
	got := snapshotPaths(obj)
	want := []string{"/data/a", "/data/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshotPaths = %v, want %v", got, want)
	}
	if snapshotPaths(map[string]any{}) != nil {
		t.Fatal("expected nil for missing paths")
	}
}
