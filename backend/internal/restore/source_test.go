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

func TestDumpFilePath(t *testing.T) {
	if got := DumpFilePath([]string{"/data/app", "/default-mealie-db.sql"}); got != "/default-mealie-db.sql" {
		t.Fatalf("DumpFilePath = %q", got)
	}
	if got := DumpFilePath([]string{"/data/app"}); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestSuggestRestoreCommand(t *testing.T) {
	cases := map[string]string{
		`sh -c 'PGPASSWORD=$POSTGRES_PASSWORD pg_dump -U $POSTGRES_USER $POSTGRES_DB'`: `psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"`,
		`pg_dumpall -U postgres`:            `psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" postgres`,
		`mariadb-dump -u root db`:           `mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"`,
		`mysqldump --all-databases`:         `mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"`,
		`tar czf - /data`:                   ``,
	}
	for in, want := range cases {
		if got := SuggestRestoreCommand(in); got != want {
			t.Errorf("SuggestRestoreCommand(%q) = %q, want %q", in, got, want)
		}
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
