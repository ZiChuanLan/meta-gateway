package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PromptGuardRule is one sensitive-content regex rule applied to request
// bodies before forwarding.
type PromptGuardRule struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Pattern         string `json:"pattern"`
	Action          string `json:"action"` // mask | reject | exclude
	Replacement     string `json:"replacement,omitempty"`
	ExcludeChannels string `json:"exclude_channels,omitempty"`
	ChannelScope    int64  `json:"channel_scope"` // 0 = all channels
	Enabled         bool   `json:"enabled"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// PromptGuardStore owns the prompt_guard_rules table.
type PromptGuardStore struct {
	db *sql.DB
}

func scanPromptGuardRule(row interface{ Scan(...any) error }) (*PromptGuardRule, error) {
	var r PromptGuardRule
	var enabled int
	if err := row.Scan(&r.ID, &r.Name, &r.Pattern, &r.Action, &r.Replacement,
		&r.ExcludeChannels, &r.ChannelScope, &enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Enabled = enabled == 1
	return &r, nil
}

const promptGuardColumns = `id, name, pattern, action, replacement, exclude_channels, channel_scope, enabled, created_at, updated_at`

// List returns all rules (enabled first, then id).
func (s *PromptGuardStore) List() ([]PromptGuardRule, error) {
	rows, err := s.db.Query(`SELECT ` + promptGuardColumns + ` FROM prompt_guard_rules ORDER BY enabled DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("prompt guard list: %w", err)
	}
	defer rows.Close()
	out := []PromptGuardRule{}
	for rows.Next() {
		r, err := scanPromptGuardRule(rows)
		if err != nil {
			return nil, fmt.Errorf("prompt guard scan: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListEnabled returns only enabled rules.
func (s *PromptGuardStore) ListEnabled() ([]PromptGuardRule, error) {
	rows, err := s.db.Query(`SELECT ` + promptGuardColumns + ` FROM prompt_guard_rules WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("prompt guard enabled: %w", err)
	}
	defer rows.Close()
	out := []PromptGuardRule{}
	for rows.Next() {
		r, err := scanPromptGuardRule(rows)
		if err != nil {
			return nil, fmt.Errorf("prompt guard scan: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// Get returns one rule by id.
func (s *PromptGuardStore) Get(id int64) (*PromptGuardRule, error) {
	row := s.db.QueryRow(`SELECT `+promptGuardColumns+` FROM prompt_guard_rules WHERE id = ?`, id)
	r, err := scanPromptGuardRule(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("prompt guard get: %w", err)
	}
	return r, nil
}

// Upsert inserts or updates a rule. Create when ID == 0.
func (s *PromptGuardStore) Upsert(r *PromptGuardRule) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r.Name = strings.TrimSpace(r.Name)
	r.Pattern = strings.TrimSpace(r.Pattern)
	r.Action = strings.ToLower(strings.TrimSpace(r.Action))
	if r.Action == "" {
		r.Action = "mask"
	}
	if r.Replacement == "" {
		r.Replacement = "[REDACTED]"
	}
	r.ExcludeChannels = strings.TrimSpace(r.ExcludeChannels)
	if r.ChannelScope < 0 {
		r.ChannelScope = 0
	}
	if r.ID == 0 {
		res, err := s.db.Exec(
			`INSERT INTO prompt_guard_rules (name, pattern, action, replacement, exclude_channels, channel_scope, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Name, r.Pattern, r.Action, r.Replacement, r.ExcludeChannels, r.ChannelScope, boolInt(r.Enabled), now, now,
		)
		if err != nil {
			return fmt.Errorf("prompt guard create: %w", err)
		}
		id, _ := res.LastInsertId()
		r.ID = id
		return nil
	}
	if _, err := s.db.Exec(
		`UPDATE prompt_guard_rules SET name=?, pattern=?, action=?, replacement=?, exclude_channels=?, channel_scope=?, enabled=?, updated_at=? WHERE id=?`,
		r.Name, r.Pattern, r.Action, r.Replacement, r.ExcludeChannels, r.ChannelScope, boolInt(r.Enabled), now, r.ID,
	); err != nil {
		return fmt.Errorf("prompt guard update: %w", err)
	}
	return nil
}

// Delete removes a rule (missing is not an error).
func (s *PromptGuardStore) Delete(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM prompt_guard_rules WHERE id = ?`, id); err != nil {
		return fmt.Errorf("prompt guard delete: %w", err)
	}
	return nil
}
