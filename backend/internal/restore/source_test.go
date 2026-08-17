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

func TestDetectEngine(t *testing.T) {
	cases := map[string]string{
		`sh -c 'pg_dump --clean'`:   "postgres",
		`pg_dumpall -U postgres`:    "postgres-all",
		`mariadb-dump -u root db`:   "mariadb",
		`mysqldump --all-databases`: "mysql",
		`tar czf - /data`:           "",
	}
	for in, want := range cases {
		if got := DetectEngine(in); got != want {
			t.Errorf("DetectEngine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveRestoreCommand(t *testing.T) {
	cases := map[string]string{
		// Real homelab annotations: env-assignment prefix must be preserved
		// verbatim (psql reads the same PG* vars pg_dump does).
		`sh -c 'PGDATABASE="$POSTGRES_USER" PGUSER="$POSTGRES_USER" PGPASSWORD="$POSTGRES_PASSWORD" pg_dump --clean'`:   `sh -c 'PGDATABASE="$POSTGRES_USER" PGUSER="$POSTGRES_USER" PGPASSWORD="$POSTGRES_PASSWORD" psql -v ON_ERROR_STOP=1'`,
		`sh -c 'PGDATABASE=dawarich_development PGUSER="$POSTGRES_USER" PGPASSWORD="$POSTGRES_PASSWORD" pg_dump --clean'`: `sh -c 'PGDATABASE=dawarich_development PGUSER="$POSTGRES_USER" PGPASSWORD="$POSTGRES_PASSWORD" psql -v ON_ERROR_STOP=1'`,
		`sh -c 'PGUSER=postgres pg_dumpall --clean'`: `sh -c 'PGUSER=postgres psql -v ON_ERROR_STOP=1 -d postgres'`,
		// mariadb/mysql: binary swap, dump-only flags stripped, creds + db kept.
		`sh -c 'mariadb-dump -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" --single-transaction "$MARIADB_DATABASE"'`: `sh -c 'mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"'`,
		`mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --databases app`:                                             `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" app`,
		`tar czf - /data`: ``,
	}
	for in, want := range cases {
		if got := DeriveRestoreCommand(in); got != want {
			t.Errorf("DeriveRestoreCommand(%q)\n  got  %q\n  want %q", in, got, want)
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
