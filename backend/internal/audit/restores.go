package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const maxPersistedLogLines = 2000

// UpsertRestoreJob persists full restore job state JSON (survives pod restart).
func (s *Store) UpsertRestoreJob(ctx context.Context, restoreID string, stateJSON []byte) error {
	if restoreID == "" {
		return fmt.Errorf("restore id required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO restore_jobs (restore_id, state_json, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(restore_id) DO UPDATE SET
  state_json = excluded.state_json,
  updated_at = excluded.updated_at
`, restoreID, string(stateJSON), now)
	return err
}

// ListRestoreJobs returns newest-first state JSON blobs.
func (s *Store) ListRestoreJobs(ctx context.Context, limit int) ([][]byte, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT state_json FROM restore_jobs
ORDER BY updated_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, []byte(raw))
	}
	return out, rows.Err()
}

// AppendRestoreLog appends one log line and trims the buffer.
func (s *Store) AppendRestoreLog(ctx context.Context, restoreID, line string) error {
	if restoreID == "" {
		return fmt.Errorf("restore id required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var logsRaw sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT logs_json FROM restore_jobs WHERE restore_id = ?`, restoreID).Scan(&logsRaw)
	if err == sql.ErrNoRows {
		// Job row may not exist yet; create a stub so logs aren't lost.
		now := time.Now().UTC().Format(time.RFC3339Nano)
		stub, _ := json.Marshal(map[string]any{"restoreId": restoreID, "step": "queued"})
		if _, err := tx.ExecContext(ctx, `
INSERT INTO restore_jobs (restore_id, state_json, logs_json, updated_at)
VALUES (?, ?, ?, ?)`, restoreID, string(stub), "[]", now); err != nil {
			return err
		}
		logsRaw = sql.NullString{String: "[]", Valid: true}
	} else if err != nil {
		return err
	}

	var lines []string
	if logsRaw.Valid && logsRaw.String != "" {
		_ = json.Unmarshal([]byte(logsRaw.String), &lines)
	}
	lines = append(lines, line)
	if len(lines) > maxPersistedLogLines {
		lines = lines[len(lines)-maxPersistedLogLines:]
	}
	b, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
UPDATE restore_jobs SET logs_json = ?, updated_at = ? WHERE restore_id = ?`,
		string(b), now, restoreID); err != nil {
		return err
	}
	return tx.Commit()
}

// GetRestoreLogs returns persisted log lines for a restore job.
func (s *Store) GetRestoreLogs(ctx context.Context, restoreID string) ([]string, error) {
	var logsRaw sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT logs_json FROM restore_jobs WHERE restore_id = ?`, restoreID).Scan(&logsRaw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !logsRaw.Valid || logsRaw.String == "" {
		return []string{}, nil
	}
	var lines []string
	if err := json.Unmarshal([]byte(logsRaw.String), &lines); err != nil {
		return nil, err
	}
	return lines, nil
}

// ReplaceRestoreLogs overwrites the full log buffer (used on hydrate).
func (s *Store) ReplaceRestoreLogs(ctx context.Context, restoreID string, lines []string) error {
	if restoreID == "" {
		return fmt.Errorf("restore id required")
	}
	if len(lines) > maxPersistedLogLines {
		lines = lines[len(lines)-maxPersistedLogLines:]
	}
	b, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
UPDATE restore_jobs SET logs_json = ?, updated_at = ? WHERE restore_id = ?`,
		string(b), now, restoreID)
	return err
}
