package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// CallPolicies resolves the effective call policy for a set of channels in one
// query. channel > site > default, mirroring domain.ResolveCallPolicy.
//
// It is a separate query rather than a column on the routing projection on
// purpose: the relay forwards whatever body the caller built and never needs to
// know the policy, so the hot path stays free of it.
func (s *ChannelStore) CallPolicies(channelIDs []int64) (map[int64]string, error) {
	result := make(map[int64]string, len(channelIDs))
	if len(channelIDs) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(channelIDs)), ",")
	args := make([]any, 0, len(channelIDs))
	for _, id := range channelIDs {
		args = append(args, id)
	}
	rows, err := s.db.Query(`SELECT c.id, COALESCE(NULLIF(c.call_policy, ''), COALESCE(site.call_policy, ''))
		FROM channels c LEFT JOIN sites site ON site.id = c.site_id
		WHERE c.id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("call policies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var policy string
		if err := rows.Scan(&id, &policy); err != nil {
			return nil, fmt.Errorf("call policy scan: %w", err)
		}
		result[id] = domain.NormalizeCallPolicy(policy)
	}
	return result, rows.Err()
}

// CallPolicyFor resolves one channel's policy (channel > site > default).
func (s *ChannelStore) CallPolicyFor(channelID int64) (string, error) {
	policies, err := s.CallPolicies([]int64{channelID})
	if err != nil {
		return domain.CallPolicyAllowProbe, err
	}
	if policy, ok := policies[channelID]; ok {
		return policy, nil
	}
	return domain.CallPolicyAllowProbe, nil
}

// MarkRealCall records that the upstream received a chat request on this
// channel.
//
// It is called from the relay path, so it stays one UPDATE with no transaction
// of its own. Two deliberate choices live at the call site, not here: it is
// written for failures too (a site's ban counts requests that arrived, and a 429
// definitely arrived), and it is never written for a request the upstream never
// saw (a local rejection or a client cancel before the send).
func (s *ChannelStore) MarkRealCall(channelID int64, at time.Time) error {
	if channelID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	if _, err := s.db.Exec(`UPDATE channels SET last_real_call_at = ? WHERE id = ? AND (last_real_call_at IS NULL OR last_real_call_at < ?)`,
		at.UTC().Format(time.RFC3339Nano), channelID, at.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("mark real call: %w", err)
	}
	return nil
}

// KeepaliveTargets resolves every enabled channel's keepalive window from the
// channel > site > global chain, grouped by credential.
//
// globalIdleDays is the fallback window for a site that has not been given one
// (0 = none configured, and the target is not callable).
func (s *ChannelStore) KeepaliveTargets(globalIdleDays int) ([]domain.KeepaliveTarget, error) {
	rows, err := s.db.Query(`SELECT
		c.id, c.name, COALESCE(c.site_id, 0), COALESCE(site.name, ''), COALESCE(c.credential_id, 0),
		COALESCE(NULLIF(c.call_policy, ''), COALESCE(site.call_policy, '')),
		CASE WHEN c.keepalive_enabled IS NULL THEN COALESCE(site.keepalive_enabled, 0) ELSE c.keepalive_enabled END,
		CASE WHEN c.keepalive_idle_days > 0 THEN c.keepalive_idle_days ELSE COALESCE(site.keepalive_idle_days, 0) END,
		COALESCE(site.keepalive_safety_margin_days, 2),
		COALESCE(site.keepalive_model, ''), COALESCE(site.keepalive_prompt, ''),
		COALESCE(site.keepalive_max_tokens, 0), COALESCE(site.keepalive_daily_cap, 0),
		COALESCE(site.keepalive_quiet_hours, ''),
		c.models_csv, c.last_real_call_at, c.created_at
		FROM channels c
		LEFT JOIN sites site ON site.id = c.site_id
		WHERE c.status = ?
		ORDER BY c.id`, domain.StatusEnabled)
	if err != nil {
		return nil, fmt.Errorf("keepalive targets: %w", err)
	}
	defer rows.Close()

	var targets []domain.KeepaliveTarget
	for rows.Next() {
		var t domain.KeepaliveTarget
		var policy string
		var enabled int
		var idleDays, margin, maxTokens, dailyCap int
		var model, prompt, quiet, modelsCSV string
		var lastCall sql.NullString
		if err := rows.Scan(&t.ChannelID, &t.ChannelName, &t.SiteID, &t.SiteName, &t.CredentialID,
			&policy, &enabled, &idleDays, &margin, &model, &prompt, &maxTokens, &dailyCap, &quiet,
			&modelsCSV, &lastCall, scanTime(&t.CreatedAt)); err != nil {
			return nil, fmt.Errorf("keepalive target scan: %w", err)
		}
		t.Policy = domain.NormalizeCallPolicy(policy)
		t.Config = domain.KeepaliveConfig{
			Enabled:          enabled != 0,
			IdleDays:         idleDays,
			SafetyMarginDays: margin,
			Prompt:           prompt,
			MaxTokens:        maxTokens,
			DailyCap:         dailyCap,
			QuietHours:       quiet,
		}
		if t.Config.IdleDays <= 0 {
			t.Config.IdleDays = globalIdleDays
		}
		// Which model to call decides whether a call can happen at all, but not
		// whether it can be dispatched: a keepalive goes straight to the channel,
		// so the model does not have to be one any route serves. The site's own
		// choice wins; otherwise the channel calls the first model it advertised.
		t.SiteModel = strings.TrimSpace(model)
		t.Candidates = splitCSV(modelsCSV)
		t.Model = t.SiteModel
		if t.Model == "" && len(t.Candidates) > 0 {
			t.Model = t.Candidates[0]
		}
		if t.Model == "" {
			t.SkipReason = "no_usable_model"
		}
		if parsed, ok := parseStoredTime(lastCall.String); ok {
			t.LastCallAt = &parsed
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachCredentialIdle(targets)
}

// attachCredentialIdle folds per-channel rows into one row per credential: the
// idle age is the credential's (the ban window counts calls to the account), and
// the send still happens on one concrete channel.
func (s *ChannelStore) attachCredentialIdle(targets []domain.KeepaliveTarget) ([]domain.KeepaliveTarget, error) {
	// The newest real call across the group, plus a channel to send from.
	groupLast := make(map[int64]time.Time)
	for _, t := range targets {
		if t.LastCallAt == nil {
			continue
		}
		if current, ok := groupLast[t.CredentialID]; !ok || current.Before(*t.LastCallAt) {
			groupLast[t.CredentialID] = *t.LastCallAt
		}
	}
	seen := make(map[int64]bool)
	merged := targets[:0]
	for _, t := range targets {
		if t.CredentialID != 0 {
			if last, ok := groupLast[t.CredentialID]; ok {
				copied := last
				t.LastCallAt = &copied
			}
			if seen[t.CredentialID] {
				continue
			}
			seen[t.CredentialID] = true
		}
		merged = append(merged, t)
	}

	// Today's successful sends, per credential, for the daily cap.
	sends, err := s.keepaliveSendsToday()
	if err != nil {
		return nil, err
	}
	for i := range merged {
		if merged[i].CredentialID != 0 {
			merged[i].SendsToday = sends[merged[i].CredentialID]
		}
	}
	return merged, nil
}

// keepaliveSendsToday counts successful keepalive calls per credential since
// UTC midnight. keepalive_events.created_at uses the same
// "YYYY-MM-DD HH:MM:SS" spelling as proxy_logs, so the day comparison is a
// string prefix rather than a date function over a non-standard format.
func (s *ChannelStore) keepaliveSendsToday() (map[int64]int, error) {
	day := time.Now().UTC().Format("2006-01-02")
	rows, err := s.db.Query(`SELECT credential_id, COUNT(*) FROM keepalive_events
		WHERE ok = 1 AND credential_id > 0 AND substr(created_at, 1, 10) = ?
		GROUP BY credential_id`, day)
	if err != nil {
		return nil, fmt.Errorf("keepalive sends today: %w", err)
	}
	defer rows.Close()
	result := make(map[int64]int)
	for rows.Next() {
		var credentialID int64
		var count int
		if err := rows.Scan(&credentialID, &count); err != nil {
			return nil, fmt.Errorf("keepalive send count scan: %w", err)
		}
		result[credentialID] = count
	}
	return result, rows.Err()
}

// RecordKeepaliveEvent appends one keepalive call to the footprint log.
func (s *ChannelStore) RecordKeepaliveEvent(event domain.KeepaliveEvent) error {
	_, err := s.db.Exec(`INSERT INTO keepalive_events
		(credential_id, site_id, site_name, channel_id, channel_name, model, form, reason, ok, status_code, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.CredentialID, event.SiteID, event.SiteName, event.ChannelID, event.ChannelName, event.Model,
		event.Form, event.Reason, boolInt(event.OK), event.StatusCode, event.Error)
	if err != nil {
		return fmt.Errorf("keepalive event: %w", err)
	}
	return nil
}

// RecentKeepaliveEvents returns the newest events, for the console's footprint
// view: what was sent, when, and why.
func (s *ChannelStore) RecentKeepaliveEvents(limit int) ([]domain.KeepaliveEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, credential_id, site_id, site_name, channel_id, channel_name, model,
			form, reason, ok, status_code, error, created_at
		FROM keepalive_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("keepalive events: %w", err)
	}
	defer rows.Close()
	var result []domain.KeepaliveEvent
	for rows.Next() {
		var event domain.KeepaliveEvent
		var ok int
		if err := rows.Scan(&event.ID, &event.CredentialID, &event.SiteID, &event.SiteName, &event.ChannelID,
			&event.ChannelName, &event.Model, &event.Form, &event.Reason, &ok, &event.StatusCode,
			&event.Error, scanTime(&event.CreatedAt)); err != nil {
			return nil, fmt.Errorf("keepalive event scan: %w", err)
		}
		event.OK = ok != 0
		result = append(result, event)
	}
	return result, rows.Err()
}

// parseStoredTime reads the two spellings this schema uses for timestamps
// (RFC3339Nano on the columns written by Go, SQLite's datetime() elsewhere).
func parseStoredTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}
