package store

import (
	"database/sql"
	"fmt"
	"strings"

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
