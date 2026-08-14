package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// AlertRule is one configurable metric→webhook alert rule.
type AlertRule struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	Metric          string  `json:"metric"`
	Operator        string  `json:"operator"`
	Threshold       float64 `json:"threshold"`
	WindowSeconds   int     `json:"window_seconds"`
	SustainedSeconds int    `json:"sustained_seconds"`
	CooldownSeconds int     `json:"cooldown_seconds"`
	Level           string  `json:"level"`
	Enabled         bool    `json:"enabled"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// AlertRuleStore owns the alert_rules table.
type AlertRuleStore struct {
	db *sql.DB
}

func scanAlertRule(row interface{ Scan(...any) error }) (*AlertRule, error) {
	var r AlertRule
	var enabled int
	if err := row.Scan(&r.ID, &r.Name, &r.Metric, &r.Operator, &r.Threshold,
		&r.WindowSeconds, &r.SustainedSeconds, &r.CooldownSeconds, &r.Level, &enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Enabled = enabled == 1
	return &r, nil
}

const alertRuleColumns = `id, name, metric, operator, threshold, window_seconds, sustained_seconds, cooldown_seconds, level, enabled, created_at, updated_at`

// List returns all rules (enabled first, then id).
func (s *AlertRuleStore) List() ([]AlertRule, error) {
	rows, err := s.db.Query(`SELECT ` + alertRuleColumns + ` FROM alert_rules ORDER BY enabled DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("alert rules list: %w", err)
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		r, err := scanAlertRule(rows)
		if err != nil {
			return nil, fmt.Errorf("alert rules scan: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListEnabled returns only enabled rules.
func (s *AlertRuleStore) ListEnabled() ([]AlertRule, error) {
	rows, err := s.db.Query(`SELECT `+alertRuleColumns+` FROM alert_rules WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("alert rules enabled: %w", err)
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		r, err := scanAlertRule(rows)
		if err != nil {
			return nil, fmt.Errorf("alert rules scan: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// Get returns one rule by id.
func (s *AlertRuleStore) Get(id int64) (*AlertRule, error) {
	row := s.db.QueryRow(`SELECT `+alertRuleColumns+` FROM alert_rules WHERE id = ?`, id)
	r, err := scanAlertRule(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("alert rule get: %w", err)
	}
	return r, nil
}

// Upsert inserts or updates a rule. Create when ID == 0.
func (s *AlertRuleStore) Upsert(r *AlertRule) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r.Name = strings.TrimSpace(r.Name)
	r.Metric = strings.TrimSpace(r.Metric)
	r.Operator = strings.TrimSpace(r.Operator)
	r.Level = strings.TrimSpace(r.Level)
	if r.Level == "" {
		r.Level = "warning"
	}
	if r.WindowSeconds <= 0 {
		r.WindowSeconds = 3600
	}
	if r.SustainedSeconds < 0 {
		r.SustainedSeconds = 0
	}
	if r.CooldownSeconds <= 0 {
		r.CooldownSeconds = 900
	}
	if r.ID == 0 {
		res, err := s.db.Exec(
			`INSERT INTO alert_rules (name, metric, operator, threshold, window_seconds, sustained_seconds, cooldown_seconds, level, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Name, r.Metric, r.Operator, r.Threshold, r.WindowSeconds, r.SustainedSeconds, r.CooldownSeconds, r.Level, boolInt(r.Enabled), now, now,
		)
		if err != nil {
			return fmt.Errorf("alert rule create: %w", err)
		}
		id, _ := res.LastInsertId()
		r.ID = id
		return nil
	}
	if _, err := s.db.Exec(
		`UPDATE alert_rules SET name=?, metric=?, operator=?, threshold=?, window_seconds=?, sustained_seconds=?, cooldown_seconds=?, level=?, enabled=?, updated_at=? WHERE id=?`,
		r.Name, r.Metric, r.Operator, r.Threshold, r.WindowSeconds, r.SustainedSeconds, r.CooldownSeconds, r.Level, boolInt(r.Enabled), now, r.ID,
	); err != nil {
		return fmt.Errorf("alert rule update: %w", err)
	}
	return nil
}

// Delete removes a rule (missing is not an error).
func (s *AlertRuleStore) Delete(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM alert_rules WHERE id = ?`, id); err != nil {
		return fmt.Errorf("alert rule delete: %w", err)
	}
	return nil
}
