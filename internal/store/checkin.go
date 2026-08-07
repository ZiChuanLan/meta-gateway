package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

type CheckinLogFilter struct {
	CredentialID *int64
	SiteID       *int64
	Status       string
	Source       string
	Limit        int
}

type CheckinLogStore struct{ db *sql.DB }

func (s *CheckinLogStore) Create(v *domain.CheckinLog) error {
	_, err := s.db.Exec(`INSERT INTO checkin_logs (site_id, credential_id, source, status, category, message, reward, latency_ms, ran_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, v.SiteID, v.CredentialID, v.Source, v.Status, v.Category, v.Message, v.Reward, v.LatencyMs, v.RanAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	if err != nil {
		return fmt.Errorf("checkin log create: %w", err)
	}
	return nil
}

func (s *CheckinLogStore) List(f CheckinLogFilter) ([]domain.CheckinLog, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	where := []string{"1=1"}
	args := []any{}
	if f.CredentialID != nil {
		where = append(where, "credential_id=?")
		args = append(args, *f.CredentialID)
	}
	if f.SiteID != nil {
		where = append(where, "site_id=?")
		args = append(args, *f.SiteID)
	}
	if f.Status != "" {
		where = append(where, "status=?")
		args = append(args, f.Status)
	}
	if f.Source != "" {
		where = append(where, "source=?")
		args = append(args, f.Source)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT id, site_id, credential_id, source, status, category, message, reward, latency_ms, ran_at FROM checkin_logs WHERE `+strings.Join(where, " AND ")+` ORDER BY ran_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("checkin log list: %w", err)
	}
	defer rows.Close()
	var out []domain.CheckinLog
	for rows.Next() {
		var v domain.CheckinLog
		if err := rows.Scan(&v.ID, &v.SiteID, &v.CredentialID, &v.Source, &v.Status, &v.Category, &v.Message, &v.Reward, &v.LatencyMs, scanTime(&v.RanAt)); err != nil {
			return nil, fmt.Errorf("checkin log scan: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// LastScheduledRunAt returns the most recent completed scheduled check-in
// batch timestamp (from the durable batch marker), falling back to the newest
// scheduled log row for installations created before the marker existed. The
// scheduler uses it to detect a missed daily tick after a restart; an
// interrupted batch never writes a marker, so the next start catches up.
func (s *CheckinLogStore) LastScheduledRunAt() (time.Time, error) {
	var raw string
	err := s.db.QueryRow(`SELECT completed_at FROM checkin_batch_state ORDER BY id DESC LIMIT 1`).Scan(&raw)
	if err == nil {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, raw); parseErr == nil {
			return parsed, nil
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, fmt.Errorf("checkin batch state: %w", err)
	}
	logs, err := s.List(CheckinLogFilter{Source: "scheduled", Limit: 1})
	if err != nil {
		return time.Time{}, err
	}
	if len(logs) == 0 {
		return time.Time{}, nil
	}
	return logs[0].RanAt, nil
}

// RecordBatchCompleted persists a durable marker for a fully finished
// scheduled batch so restarts do not re-run today's check-in.
func (s *CheckinLogStore) RecordBatchCompleted(completedAt time.Time) error {
	_, err := s.db.Exec(`INSERT INTO checkin_batch_state (completed_at) VALUES (?)`, completedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("checkin batch state insert: %w", err)
	}
	return nil
}
