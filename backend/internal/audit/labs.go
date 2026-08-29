package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Restore Lab persistence: full state JSON + a bounded log buffer per lab run
// (mirrors restores.go), and the drill-evidence queries the dashboard's
// "last verified restore" panel is built on.

// UpsertLabJob persists full lab state JSON (survives pod restart).
func (s *Store) UpsertLabJob(ctx context.Context, labID string, stateJSON []byte) error {
	if labID == "" {
		return fmt.Errorf("lab id required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO labs (lab_id, state_json, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(lab_id) DO UPDATE SET
  state_json = excluded.state_json,
  updated_at = excluded.updated_at
`, labID, string(stateJSON), now)
	return err
}

// ListLabJobs returns newest-first state JSON blobs.
func (s *Store) ListLabJobs(ctx context.Context, limit int) ([][]byte, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT state_json FROM labs
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

// AppendLabLog appends one log line and trims the buffer.
func (s *Store) AppendLabLog(ctx context.Context, labID, line string) error {
	if labID == "" {
		return fmt.Errorf("lab id required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var logsRaw sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT logs_json FROM labs WHERE lab_id = ?`, labID).Scan(&logsRaw)
	if err == sql.ErrNoRows {
		// Lab row may not exist yet; create a stub so logs aren't lost.
		now := time.Now().UTC().Format(time.RFC3339Nano)
		stub, _ := json.Marshal(map[string]any{"labId": labID, "step": "queued"})
		if _, err := tx.ExecContext(ctx, `
INSERT INTO labs (lab_id, state_json, logs_json, updated_at)
VALUES (?, ?, ?, ?)`, labID, string(stub), "[]", now); err != nil {
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
UPDATE labs SET logs_json = ?, updated_at = ? WHERE lab_id = ?`,
		string(b), now, labID); err != nil {
		return err
	}
	return tx.Commit()
}

// GetLabLogs returns persisted log lines for a lab run.
func (s *Store) GetLabLogs(ctx context.Context, labID string) ([]string, error) {
	var logsRaw sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT logs_json FROM labs WHERE lab_id = ?`, labID).Scan(&logsRaw)
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

// DrillStatus is one namespace's restore-verification evidence: its most
// recent drill and its most recent successful drill.
type DrillStatus struct {
	Namespace     string     `json:"namespace"`
	LastAt        time.Time  `json:"lastAt"`
	LastStatus    string     `json:"lastStatus"`
	LastLabID     string     `json:"lastLabId,omitempty"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
}

// LatestDrillPerNamespace aggregates drill audit entries into per-namespace
// evidence rows.
func (s *Store) LatestDrillPerNamespace(ctx context.Context) ([]DrillStatus, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT a.namespace, a.status, a.at, a.restore_id
FROM audit a
WHERE a.kind = 'drill' AND a.namespace IS NOT NULL
  AND a.id = (SELECT MAX(id) FROM audit WHERE kind = 'drill' AND namespace = a.namespace)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byNS := map[string]*DrillStatus{}
	var order []string
	for rows.Next() {
		var ns string
		var status, at, labID sql.NullString
		if err := rows.Scan(&ns, &status, &at, &labID); err != nil {
			return nil, err
		}
		d := &DrillStatus{Namespace: ns, LastStatus: status.String, LastLabID: labID.String}
		d.LastAt, _ = time.Parse(time.RFC3339Nano, at.String)
		byNS[ns] = d
		order = append(order, ns)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 'success' = the lab reached ready; 'passed' = the operator's verdict.
	srows, err := s.db.QueryContext(ctx, `
SELECT namespace, MAX(at) FROM audit
WHERE kind = 'drill' AND status IN ('success', 'passed') AND namespace IS NOT NULL
GROUP BY namespace`)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var ns string
		var at sql.NullString
		if err := srows.Scan(&ns, &at); err != nil {
			return nil, err
		}
		if d, ok := byNS[ns]; ok && at.Valid {
			if t, err := time.Parse(time.RFC3339Nano, at.String); err == nil {
				d.LastSuccessAt = &t
			}
		}
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}

	out := make([]DrillStatus, 0, len(order))
	for _, ns := range order {
		out = append(out, *byNS[ns])
	}
	return out, nil
}
