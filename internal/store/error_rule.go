package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrorRuleAction values for ErrorPassRule.Action.
const (
	ErrorRulePassthrough = "passthrough"
	ErrorRuleRewrite     = "rewrite"
	ErrorRuleIgnoreMonitor = "ignore_monitor"
)

// ErrorPassRule is one configurable upstream-error handling rule.
type ErrorPassRule struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	StatusCode int    `json:"status_code"` // 0 = any 4xx
	Keyword    string `json:"keyword"`     // error-body substring, case-insensitive; empty = any
	ModelGlob  string `json:"model_glob"`  // glob; empty = all models
	ChannelID  int64  `json:"channel_id"`  // 0 = all channels
	Action     string `json:"action"`      // passthrough | rewrite | ignore_monitor
	RewriteTo  int    `json:"rewrite_to"`  // target status when action=rewrite
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// ErrorPassRuleStore owns the error passthrough rule table.
type ErrorPassRuleStore struct {
	db *sql.DB
}

// List returns all rules, newest first.
func (s *ErrorPassRuleStore) List() ([]ErrorPassRule, error) {
	rows, err := s.db.Query(`SELECT id, name, status_code, keyword, model_glob, channel_id, action, rewrite_to, enabled, created_at, updated_at FROM error_passthrough_rules ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("error rules list: %w", err)
	}
	defer rows.Close()
	out := []ErrorPassRule{}
	for rows.Next() {
		var r ErrorPassRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.StatusCode, &r.Keyword, &r.ModelGlob, &r.ChannelID, &r.Action, &r.RewriteTo, &enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("error rules scan: %w", err)
		}
		r.Enabled = enabled == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListEnabled returns only enabled rules, read live on each request so
// edits apply without a reload (hot reload by construction).
func (s *ErrorPassRuleStore) ListEnabled() ([]ErrorPassRule, error) {
	rows, err := s.db.Query(`SELECT id, name, status_code, keyword, model_glob, channel_id, action, rewrite_to, enabled, created_at, updated_at FROM error_passthrough_rules WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("error rules enabled list: %w", err)
	}
	defer rows.Close()
	out := []ErrorPassRule{}
	for rows.Next() {
		var r ErrorPassRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.StatusCode, &r.Keyword, &r.ModelGlob, &r.ChannelID, &r.Action, &r.RewriteTo, &enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("error rules scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Create inserts a rule and returns its id.
func (s *ErrorPassRuleStore) Create(r *ErrorPassRule) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`INSERT INTO error_passthrough_rules (name, status_code, keyword, model_glob, channel_id, action, rewrite_to, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Name, r.StatusCode, r.Keyword, r.ModelGlob, r.ChannelID, r.Action, r.RewriteTo, boolInt(r.Enabled), now, now)
	if err != nil {
		return 0, fmt.Errorf("error rule create: %w", err)
	}
	return res.LastInsertId()
}

// Update replaces a rule's fields (id stays).
func (s *ErrorPassRuleStore) Update(r *ErrorPassRule) error {
	res, err := s.db.Exec(`UPDATE error_passthrough_rules SET name=?, status_code=?, keyword=?, model_glob=?, channel_id=?, action=?, rewrite_to=?, enabled=?, updated_at=? WHERE id=?`,
		r.Name, r.StatusCode, r.Keyword, r.ModelGlob, r.ChannelID, r.Action, r.RewriteTo, boolInt(r.Enabled), time.Now().UTC().Format(time.RFC3339Nano), r.ID)
	if err != nil {
		return fmt.Errorf("error rule update: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return errors.New("error rule not found")
	}
	return nil
}

// Delete removes a rule.
func (s *ErrorPassRuleStore) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM error_passthrough_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("error rule delete: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return errors.New("error rule not found")
	}
	return nil
}

// matchErrorRuleGlob supports * and ? like the payload-rule glob.
func matchErrorRuleGlob(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	pattern = strings.ToLower(pattern)
	value = strings.ToLower(value)
	var p, si, star, mark int
	star = -1
	for si < len(value) {
		if p < len(pattern) && (pattern[p] == '?' || pattern[p] == value[si]) {
			p++
			si++
		} else if p < len(pattern) && pattern[p] == '*' {
			star = p
			mark = si
			p++
		} else if star >= 0 {
			p = star + 1
			mark++
			si = mark
		} else {
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// MatchErrorRule finds the first enabled rule that applies to the upstream
// error (status + body text) for this model/channel. Nil = no rule.
func (s *ErrorPassRuleStore) MatchErrorRule(status int, bodyText, model string, channelID int64) (*ErrorPassRule, error) {
	if status < 400 || status > 499 {
		return nil, nil
	}
	rules, err := s.ListEnabled()
	if err != nil {
		return nil, err
	}
	for i := range rules {
		r := &rules[i]
		if r.StatusCode != 0 && r.StatusCode != status {
			continue
		}
		if r.Keyword != "" && !strings.Contains(strings.ToLower(bodyText), strings.ToLower(r.Keyword)) {
			continue
		}
		if !matchErrorRuleGlob(r.ModelGlob, model) {
			continue
		}
		if r.ChannelID != 0 && r.ChannelID != channelID {
			continue
		}
		return r, nil
	}
	return nil, nil
}
