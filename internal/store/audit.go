package store

import (
	"database/sql"
	"time"
)

type AuditEvent struct {
	ID           int64     `json:"id"`
	RequestID    string    `json:"request_id,omitempty"`
	ActorKind    string    `json:"actor_kind"`
	ActorID      *int64    `json:"actor_id,omitempty"`
	Action       string    `json:"action"`
	ResourceKind string    `json:"resource_kind,omitempty"`
	ResourceID   *int64    `json:"resource_id,omitempty"`
	Outcome      string    `json:"outcome"`
	StatusCode   int       `json:"status_code"`
	Category     string    `json:"category,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type AuditEventStore struct{ db *sql.DB }

func (s *AuditEventStore) Insert(event *AuditEvent) error {
	_, err := s.db.Exec(`INSERT INTO audit_events
        (request_id, actor_kind, actor_id, action, resource_kind, resource_id, outcome, status_code, category)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.RequestID, event.ActorKind, event.ActorID,
		event.Action, event.ResourceKind, event.ResourceID, event.Outcome, event.StatusCode, event.Category)
	return err
}

func (s *AuditEventStore) List(limit int, beforeID int64) ([]AuditEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, request_id, actor_kind, actor_id, action, resource_kind, resource_id,
        outcome, status_code, category, created_at FROM audit_events`
	args := []any{}
	if beforeID > 0 {
		query += ` WHERE id < ?`
		args = append(args, beforeID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		if err := rows.Scan(&event.ID, &event.RequestID, &event.ActorKind, &event.ActorID,
			&event.Action, &event.ResourceKind, &event.ResourceID, &event.Outcome,
			&event.StatusCode, &event.Category, scanTime(&event.CreatedAt)); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *AuditEventStore) Cleanup(now time.Time, retentionDays, maxRows int) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var total int64
	if retentionDays > 0 {
		cutoff := now.UTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339Nano)
		// SQLite's datetime('now') uses a space separator while Go's RFC3339
		// uses 'T'. A raw TEXT comparison therefore gives the wrong result for
		// rows from the normal insert path. julianday normalizes both forms.
		result, err := tx.Exec(`DELETE FROM audit_events WHERE julianday(created_at) < julianday(?)`, cutoff)
		if err != nil {
			return total, err
		}
		count, _ := result.RowsAffected()
		total += count
	}
	if maxRows > 0 {
		result, err := tx.Exec(`DELETE FROM audit_events WHERE id NOT IN
            (SELECT id FROM audit_events ORDER BY id DESC LIMIT ?)`, maxRows)
		if err != nil {
			return total, err
		}
		count, _ := result.RowsAffected()
		total += count
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}
