package audit

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Entry struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"` // restore | download | backup | check | system
	Actor      string    `json:"actor"`
	At         time.Time `json:"at"`
	Namespace  string    `json:"namespace,omitempty"`
	Snapshot   string    `json:"snapshot,omitempty"`
	PVC        string    `json:"pvc,omitempty"`
	Path       string    `json:"path,omitempty"`
	Status     string    `json:"status,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Bytes      int64     `json:"bytes,omitempty"`
	RestoreID  string    `json:"restoreId,omitempty"`
	ArgoApp    string    `json:"argoApp,omitempty"`
	ArgoPaused *bool     `json:"argoPaused,omitempty"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  actor TEXT NOT NULL,
  at TEXT NOT NULL,
  namespace TEXT,
  snapshot TEXT,
  pvc TEXT,
  path TEXT,
  status TEXT,
  detail TEXT,
  bytes INTEGER DEFAULT 0,
  restore_id TEXT,
  argo_app TEXT,
  argo_paused INTEGER
);
CREATE INDEX IF NOT EXISTS idx_audit_at ON audit(at);
CREATE INDEX IF NOT EXISTS idx_audit_kind ON audit(kind);

-- Durable GUI restore jobs (state + log buffer). Survives pod restarts on /data PVC.
CREATE TABLE IF NOT EXISTS restore_jobs (
  restore_id TEXT PRIMARY KEY,
  state_json TEXT NOT NULL,
  logs_json TEXT,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_restore_jobs_updated ON restore_jobs(updated_at);

-- Durable outcome history of K8up job CRs (Backup/Check/Prune/Restore).
-- K8up garbage-collects finished CRs per keepJobs; this table is what lets the
-- GUI answer "what ran last month and did it succeed".
CREATE TABLE IF NOT EXISTS backup_events (
  uid TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  namespace TEXT NOT NULL,
  name TEXT NOT NULL,
  schedule TEXT,
  status TEXT NOT NULL,
  message TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_backup_events_started ON backup_events(started_at);
CREATE INDEX IF NOT EXISTS idx_backup_events_kind ON backup_events(kind);

-- Small key/value store for runtime settings (e.g. notification overrides).
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`)
	return err
}

func (s *Store) Insert(ctx context.Context, e Entry) (int64, error) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	var paused any
	if e.ArgoPaused != nil {
		if *e.ArgoPaused {
			paused = 1
		} else {
			paused = 0
		}
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO audit (kind, actor, at, namespace, snapshot, pvc, path, status, detail, bytes, restore_id, argo_app, argo_paused)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Kind, e.Actor, e.At.UTC().Format(time.RFC3339Nano),
		nullStr(e.Namespace), nullStr(e.Snapshot), nullStr(e.PVC), nullStr(e.Path),
		nullStr(e.Status), nullStr(e.Detail), e.Bytes, nullStr(e.RestoreID), nullStr(e.ArgoApp), paused,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type ListFilter struct {
	Kind   string
	Actor  string
	Status string
	Since  time.Time
	Until  time.Time
	Limit  int
	Offset int
}

func (f ListFilter) where() (string, []any) {
	var conds []string
	var args []any
	if f.Kind != "" {
		conds = append(conds, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.Actor != "" {
		conds = append(conds, "actor = ?")
		args = append(args, f.Actor)
	}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if !f.Since.IsZero() {
		conds = append(conds, "at >= ?")
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}
	if !f.Until.IsZero() {
		conds = append(conds, "at <= ?")
		args = append(args, f.Until.UTC().Format(time.RFC3339Nano))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (s *Store) List(ctx context.Context, f ListFilter) ([]Entry, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 100000 {
		f.Limit = 100000
	}
	q := `SELECT id, kind, actor, at, namespace, snapshot, pvc, path, status, detail, bytes, restore_id, argo_app, argo_paused
FROM audit`
	where, args := f.where()
	q += where
	q += ` ORDER BY at DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		var at string
		var ns, snap, pvc, path, status, detail, rid, argo sql.NullString
		var paused sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Kind, &e.Actor, &at, &ns, &snap, &pvc, &path, &status, &detail, &e.Bytes, &rid, &argo, &paused); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		e.Namespace = ns.String
		e.Snapshot = snap.String
		e.PVC = pvc.String
		e.Path = path.String
		e.Status = status.String
		e.Detail = detail.String
		e.RestoreID = rid.String
		e.ArgoApp = argo.String
		if paused.Valid {
			b := paused.Int64 == 1
			e.ArgoPaused = &b
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Count returns the total rows matching the filter (ignores Limit/Offset).
func (s *Store) Count(ctx context.Context, f ListFilter) (int64, error) {
	where, args := f.where()
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`+where, args...).Scan(&n)
	return n, err
}

// Summary aggregates the counters the dashboard shows, in SQL, over the full
// retention window — never over a truncated page.
type Summary struct {
	RestoreOK     int64  `json:"restoreOk"`
	RestoreFail   int64  `json:"restoreFail"`
	DownloadBytes int64  `json:"downloadBytes"`
	LatestRestore *Entry `json:"latestRestore,omitempty"`
}

func (s *Store) Summary(ctx context.Context) (*Summary, error) {
	out := &Summary{}
	err := s.db.QueryRowContext(ctx, `
SELECT
  COALESCE(SUM(CASE WHEN kind='restore' AND status='success' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN kind='restore' AND status='failed' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN kind='download' THEN bytes ELSE 0 END), 0)
FROM audit`).Scan(&out.RestoreOK, &out.RestoreFail, &out.DownloadBytes)
	if err != nil {
		return nil, err
	}
	latest, err := s.List(ctx, ListFilter{Kind: "restore", Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(latest) > 0 {
		out.LatestRestore = &latest[0]
	}
	return out, nil
}

// CountBackupEvents returns totals keyed "kind|status" for /metrics.
func (s *Store) CountBackupEvents(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, status, COUNT(*) FROM backup_events GROUP BY kind, status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var kind, status string
		var n int64
		if err := rows.Scan(&kind, &status, &n); err != nil {
			return nil, err
		}
		out[kind+"|"+status] = n
	}
	return out, rows.Err()
}

// GetSetting returns "" when the key does not exist.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) PruneOlderThan(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention).Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit WHERE at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func BoolPtr(b bool) *bool { return &b }

func FormatErr(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
