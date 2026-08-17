package audit

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// BackupEvent is one observed K8up job CR run (Backup/Check/Prune/Restore).
type BackupEvent struct {
	UID        string     `json:"uid"`
	Kind       string     `json:"kind"`
	Namespace  string     `json:"namespace"`
	Name       string     `json:"name"`
	Schedule   string     `json:"schedule,omitempty"`
	Status     string     `json:"status"` // running | succeeded | failed | unknown
	Message    string     `json:"message,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

func (s *Store) UpsertBackupEvent(ctx context.Context, e BackupEvent) error {
	var fin any
	if e.FinishedAt != nil {
		fin = e.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// finished_at is sticky: once a run is recorded as finished, later sweeps
	// (which may see the CR again with the same conditions) never move it.
	_, err := s.db.ExecContext(ctx, `
INSERT INTO backup_events (uid, kind, namespace, name, schedule, status, message, started_at, finished_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(uid) DO UPDATE SET
  status = excluded.status,
  message = excluded.message,
  schedule = excluded.schedule,
  finished_at = COALESCE(backup_events.finished_at, excluded.finished_at),
  updated_at = excluded.updated_at`,
		e.UID, e.Kind, e.Namespace, e.Name, nullStr(e.Schedule), e.Status, nullStr(e.Message),
		e.StartedAt.UTC().Format(time.RFC3339Nano), fin, now,
	)
	return err
}

// MarkVanishedRunning flags still-running events of a kind whose CR no longer
// exists in the cluster — the outcome is unknowable once K8up GC'd the CR.
func (s *Store) MarkVanishedRunning(ctx context.Context, kind string, presentUIDs []string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	q := `UPDATE backup_events SET status = 'unknown', finished_at = COALESCE(finished_at, ?), updated_at = ?
WHERE kind = ? AND status = 'running'`
	args := []any{now, now, kind}
	if len(presentUIDs) > 0 {
		q += ` AND uid NOT IN (?` + strings.Repeat(",?", len(presentUIDs)-1) + `)`
		for _, u := range presentUIDs {
			args = append(args, u)
		}
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) ListBackupEvents(ctx context.Context, since time.Time, kind string) ([]BackupEvent, error) {
	q := `SELECT uid, kind, namespace, name, schedule, status, message, started_at, finished_at
FROM backup_events WHERE started_at >= ?`
	args := []any{since.UTC().Format(time.RFC3339Nano)}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY started_at DESC LIMIT 20000`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BackupEvent
	for rows.Next() {
		var e BackupEvent
		var started string
		var schedule, message, finished sql.NullString
		if err := rows.Scan(&e.UID, &e.Kind, &e.Namespace, &e.Name, &schedule, &e.Status, &message, &started, &finished); err != nil {
			return nil, err
		}
		e.Schedule = schedule.String
		e.Message = message.String
		e.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		if finished.Valid {
			if t, err := time.Parse(time.RFC3339Nano, finished.String); err == nil {
				e.FinishedAt = &t
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) PruneBackupEventsOlderThan(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention).Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `DELETE FROM backup_events WHERE started_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
