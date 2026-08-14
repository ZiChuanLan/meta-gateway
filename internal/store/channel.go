package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// ChannelStore provides CRUD operations for channels.
type ChannelStore struct {
	db *sql.DB
}

func scanChannel(scanner interface {
	Scan(dest ...any) error
}, r *domain.Channel) error {
	var stableFirst int
	if err := scanner.Scan(
		&r.ID,
		&r.SiteID,
		&r.CredentialID,
		&r.Name,
		&r.BaseURL,
		&r.ModelsCSV,
		&r.GroupName,
		&r.Priority,
		&r.Weight,
		&r.Status,
		&r.TypeHint,
		&r.MaxReasoningEffort,
		&r.PayloadRules,
		&r.MaxConcurrent,
		&r.ProxyURL,
		&r.HeaderOverride,
		&r.SystemPrompt,
		&r.RetryConfig,
		&r.Tags,
		&r.ConsecutiveFailures,
		&stableFirst,
		&r.StableFirstRequests,
		scanTime(&r.CreatedAt),
		scanTime(&r.UpdatedAt),
	); err != nil {
		return err
	}
	r.StableFirst = stableFirst != 0
	return nil
}

func (s *ChannelStore) List() ([]domain.Channel, error) {
	rows, err := s.db.Query(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, max_reasoning_effort, payload_rules, max_concurrent, proxy_url, header_override, system_prompt, retry_config, tags, consecutive_failures, stable_first, stable_first_requests, created_at, updated_at FROM channels ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("channel list: %w", err)
	}
	defer rows.Close()

	var result []domain.Channel
	for rows.Next() {
		var r domain.Channel
		if err := scanChannel(rows, &r); err != nil {
			return nil, fmt.Errorf("channel scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ListOverviews returns the operational state needed by the channel workspace.
func (s *ChannelStore) ListOverviews(now time.Time) ([]domain.ChannelOverview, error) {
	rows, err := s.db.Query(`SELECT
		c.id, c.site_id, c.credential_id, c.name, c.base_url, c.models_csv, c.group_name,
		c.priority, c.weight, c.status, c.type_hint, c.header_override, c.system_prompt, c.retry_config, c.stable_first, c.created_at, c.updated_at,
		COALESCE(cred.kind, ''),
		CASE WHEN EXISTS (
			SELECT 1 FROM credentials user_checkin
			WHERE user_checkin.site_id = c.site_id
			  AND user_checkin.status = 'enabled'
			  AND user_checkin.secret_enc <> ''
			  AND lower(user_checkin.kind) IN ('access_token', 'session')
			  AND user_checkin.checkin_enabled = 1
		) THEN 1 ELSE 0 END,
		CASE WHEN EXISTS (
			SELECT 1 FROM credentials user_cred
			WHERE user_cred.site_id = c.site_id
			  AND user_cred.status = 'enabled'
			  AND user_cred.secret_enc <> ''
			  AND lower(user_cred.kind) IN ('access_token', 'session')
		) THEN 1 ELSE 0 END,
		CASE WHEN EXISTS (
			SELECT 1 FROM credentials user_id_cred
			WHERE user_id_cred.site_id = c.site_id
			  AND user_id_cred.status = 'enabled'
			  AND user_id_cred.secret_enc <> ''
			  AND lower(user_id_cred.kind) IN ('access_token', 'session')
			  AND json_valid(user_id_cred.meta_json)
			  AND CAST(json_extract(user_id_cred.meta_json, '$.platform_user_id') AS INTEGER) > 0
		) THEN 1 ELSE 0 END,
		CASE WHEN EXISTS (
			SELECT 1 FROM credentials key_cred
			WHERE key_cred.site_id = c.site_id
			  AND key_cred.status = 'enabled'
			  AND key_cred.secret_enc <> ''
			  AND lower(key_cred.kind) = 'api_key'
		) OR (cred.id IS NOT NULL AND cred.status = 'enabled' AND cred.secret_enc <> '' AND lower(cred.kind) = 'api_key')
		THEN 1 ELSE 0 END,
		CASE WHEN site.id IS NOT NULL AND site.status = 'enabled' THEN 1 ELSE 0 END,
		CASE WHEN (
			cred.id IS NOT NULL AND cred.status = 'enabled' AND cred.secret_enc <> ''
			AND cred.site_id = c.site_id AND lower(cred.kind) = 'api_key'
		) OR EXISTS (
			SELECT 1 FROM credentials pool_cred
			WHERE pool_cred.site_id = c.site_id
			  AND pool_cred.status = 'enabled'
			  AND pool_cred.secret_enc <> ''
			  AND lower(pool_cred.kind) = 'api_key'
		) THEN 1 ELSE 0 END,
		(SELECT COUNT(DISTINCT rm.route_id) FROM route_members rm WHERE rm.channel_id = c.id),
		(SELECT dm.checked_at FROM discovered_models dm WHERE dm.channel_id = c.id ORDER BY dm.checked_at DESC, dm.id DESC LIMIT 1),
		COALESCE((SELECT dm.latency_ms FROM discovered_models dm WHERE dm.channel_id = c.id ORDER BY dm.checked_at DESC, dm.id DESC LIMIT 1), 0),
		COALESCE((SELECT dm.source FROM discovered_models dm WHERE dm.channel_id = c.id ORDER BY dm.checked_at DESC, dm.id DESC LIMIT 1), ''),
		(SELECT COUNT(*) FROM route_members rm WHERE rm.channel_id = c.id),
		(SELECT COUNT(*) FROM route_members rm WHERE rm.channel_id = c.id AND rm.enabled = 1),
		(SELECT COUNT(*) FROM route_members rm WHERE rm.channel_id = c.id AND rm.enabled = 1 AND rm.cooldown_until > ?),
		COALESCE((SELECT SUM(rm.fail_count) FROM route_members rm WHERE rm.channel_id = c.id), 0),
		COALESCE((SELECT rm.last_error FROM route_members rm WHERE rm.channel_id = c.id AND rm.last_error <> '' ORDER BY rm.updated_at DESC, rm.id DESC LIMIT 1), ''),
		c.last_probe_at,
		COALESCE(c.last_probe_ok, 0),
		COALESCE(c.last_probe_error, ''),
		c.last_ping_at,
		COALESCE(c.last_ping_ok, 0),
		COALESCE(c.last_ping_error, ''),
		COALESCE(c.last_ping_ms, 0),
		COALESCE(site.platform, '')
		FROM channels c
		LEFT JOIN sites site ON site.id = c.site_id
		LEFT JOIN credentials cred ON cred.id = c.credential_id
		ORDER BY c.priority DESC, c.id`,
		now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("channel overview list: %w", err)
	}
	defer rows.Close()

	var result []domain.ChannelOverview
	for rows.Next() {
		var overview domain.ChannelOverview
		var checkinEnabled, hasUserCredential, hasPlatformUserID, hasAPIKey, siteUsable, credentialUsable, lastProbeOK, stableFirst, lastPingOK int
		if err := rows.Scan(
			&overview.Channel.ID,
			&overview.Channel.SiteID,
			&overview.Channel.CredentialID,
			&overview.Channel.Name,
			&overview.Channel.BaseURL,
			&overview.Channel.ModelsCSV,
			&overview.Channel.GroupName,
			&overview.Channel.Priority,
			&overview.Channel.Weight,
			&overview.Channel.Status,
			&overview.Channel.TypeHint,
			&overview.Channel.HeaderOverride,
			&overview.Channel.SystemPrompt,
			&overview.Channel.RetryConfig,
			&stableFirst,
			scanTime(&overview.Channel.CreatedAt),
			scanTime(&overview.Channel.UpdatedAt),
			&overview.CredentialKind,
			&checkinEnabled,
			&hasUserCredential,
			&hasPlatformUserID,
			&hasAPIKey,
			&siteUsable,
			&credentialUsable,
			&overview.ModelCount,
			scanNullTime(&overview.LastCheckedAt),
			&overview.LastLatencyMs,
			&overview.DiscoverySource,
			&overview.RouteCount,
			&overview.EnabledMemberCount,
			&overview.CoolingMemberCount,
			&overview.FailureCount,
			&overview.LastError,
			scanNullTime(&overview.LastProbeAt),
			&lastProbeOK,
			&overview.LastProbeError,
			scanNullTime(&overview.LastPingAt),
			&lastPingOK,
			&overview.LastPingError,
			&overview.LastPingMs,
			&overview.SitePlatform,
		); err != nil {
			return nil, fmt.Errorf("channel overview scan: %w", err)
		}
		overview.CheckinEnabled = checkinEnabled != 0
		overview.HasUserCredential = hasUserCredential != 0
		overview.HasPlatformUserID = hasPlatformUserID != 0
		overview.LastProbeOK = lastProbeOK != 0
		overview.LastPingOK = lastPingOK != 0
		overview.HasAPIKey = hasAPIKey != 0
		overview.SiteUsable = siteUsable != 0
		overview.CredentialUsable = credentialUsable != 0
		overview.Channel.StableFirst = stableFirst != 0
		overview.HealthState = DeriveHealthState(overview)
		overview.HealthReason = DeriveHealthReason(overview)
		overview.ConnectivityState = DeriveConnectivityState(overview)
		result = append(result, overview)
	}
	return result, rows.Err()
}

// ListEnabled returns all enabled channels.
func (s *ChannelStore) ListEnabled() ([]domain.Channel, error) {
	rows, err := s.db.Query(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, max_reasoning_effort, payload_rules, max_concurrent, proxy_url, header_override, system_prompt, retry_config, tags, consecutive_failures, stable_first, stable_first_requests, created_at, updated_at FROM channels WHERE status = ? ORDER BY priority, id`, domain.StatusEnabled)
	if err != nil {
		return nil, fmt.Errorf("channel list enabled: %w", err)
	}
	defer rows.Close()

	var result []domain.Channel
	for rows.Next() {
		var r domain.Channel
		if err := scanChannel(rows, &r); err != nil {
			return nil, fmt.Errorf("channel scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ListAutoDisabled returns channels currently parked by the auto-disable
// circuit (recovery-probe candidates).
func (s *ChannelStore) ListAutoDisabled() ([]domain.Channel, error) {
	rows, err := s.db.Query(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, max_reasoning_effort, payload_rules, max_concurrent, proxy_url, header_override, system_prompt, retry_config, tags, consecutive_failures, stable_first, stable_first_requests, created_at, updated_at FROM channels WHERE status = ? ORDER BY id`, domain.StatusAutoDisabled)
	if err != nil {
		return nil, fmt.Errorf("channel list auto disabled: %w", err)
	}
	defer rows.Close()

	var result []domain.Channel
	for rows.Next() {
		var r domain.Channel
		if err := scanChannel(rows, &r); err != nil {
			return nil, fmt.Errorf("channel scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ListProbeable returns channels eligible for connectivity probing: enabled
// channels plus auto-disabled ones (passive recovery candidates). Manually
// disabled channels are never probed — manual intent wins.
func (s *ChannelStore) ListProbeable() ([]domain.Channel, error) {
	rows, err := s.db.Query(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, max_reasoning_effort, payload_rules, max_concurrent, proxy_url, header_override, system_prompt, retry_config, tags, consecutive_failures, stable_first, stable_first_requests, created_at, updated_at FROM channels WHERE status IN (?, ?) ORDER BY priority, id`, domain.StatusEnabled, domain.StatusAutoDisabled)
	if err != nil {
		return nil, fmt.Errorf("channel list probeable: %w", err)
	}
	defer rows.Close()

	var result []domain.Channel
	for rows.Next() {
		var r domain.Channel
		if err := scanChannel(rows, &r); err != nil {
			return nil, fmt.Errorf("channel scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *ChannelStore) GetByID(id int64) (*domain.Channel, error) {
	row := s.db.QueryRow(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, max_reasoning_effort, payload_rules, max_concurrent, proxy_url, header_override, system_prompt, retry_config, tags, consecutive_failures, stable_first, stable_first_requests, created_at, updated_at FROM channels WHERE id = ?`, id)
	var r domain.Channel
	if err := scanChannel(row, &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("channel get: %w", err)
	}
	return &r, nil
}

func (s *ChannelStore) Create(c *domain.Channel) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO channels (site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, max_reasoning_effort, payload_rules, max_concurrent, proxy_url, header_override, system_prompt, retry_config, tags, stable_first) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.SiteID, c.CredentialID, c.Name, c.BaseURL, c.ModelsCSV, c.GroupName, c.Priority, c.Weight, c.Status, c.TypeHint, c.MaxReasoningEffort, c.PayloadRules, c.MaxConcurrent, c.ProxyURL, c.HeaderOverride, c.SystemPrompt, c.RetryConfig, normalizeTags(c.Tags), boolInt(c.StableFirst))
	if err != nil {
		return 0, fmt.Errorf("channel create: %w", err)
	}
	return res.LastInsertId()
}

func (s *ChannelStore) Update(c *domain.Channel) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("channel update begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE channels SET site_id=?, credential_id=?, name=?, base_url=?, models_csv=?, group_name=?, priority=?, weight=?, status=?, type_hint=?, max_reasoning_effort=?, payload_rules=?, max_concurrent=?, proxy_url=?, header_override=?, system_prompt=?, retry_config=?, tags=?, stable_first=?, updated_at=datetime('now') WHERE id=?`,
		c.SiteID, c.CredentialID, c.Name, c.BaseURL, c.ModelsCSV, c.GroupName, c.Priority, c.Weight, c.Status, c.TypeHint, c.MaxReasoningEffort, c.PayloadRules, c.MaxConcurrent, c.ProxyURL, c.HeaderOverride, c.SystemPrompt, c.RetryConfig, normalizeTags(c.Tags), boolInt(c.StableFirst), c.ID); err != nil {
		return fmt.Errorf("channel update: %w", err)
	}
	if _, err = tx.Exec(`UPDATE route_members SET priority=?, weight=?, updated_at=datetime('now')
		WHERE channel_id=? AND auto=1 AND manual_override=0`,
		c.Priority, c.Weight, c.ID); err != nil {
		return fmt.Errorf("channel update automatic members: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("channel update commit: %w", err)
	}
	return nil
}

func (s *ChannelStore) Delete(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("channel delete begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Discovered models + route_members cascade via FK.
	// Empty routes without remaining members are removed to avoid ghost models.
	if _, err := tx.Exec(`DELETE FROM channels WHERE id = ?`, id); err != nil {
		return fmt.Errorf("channel delete: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM routes WHERE id NOT IN (SELECT DISTINCT route_id FROM route_members)`); err != nil {
		return fmt.Errorf("channel delete empty routes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channel delete commit: %w", err)
	}
	return nil
}

// RecordProbeSuccess marks the channel as recently verified by discovery/probe.
// AutoDisable marks a channel auto_disabled unless it is already manually
// disabled (manual intent wins). No-op when the channel is enabled.
func (s *ChannelStore) AutoDisable(channelID int64) error {
	res, err := s.db.Exec(`UPDATE channels SET status = ?, consecutive_failures = 0 WHERE id = ? AND status = ?`,
		domain.StatusAutoDisabled, channelID, domain.StatusEnabled)
	if err != nil {
		return fmt.Errorf("channel auto disable: %w", err)
	}
	// Also clear per-member cooldowns so recovery probing is not blocked.
	affected, _ := res.RowsAffected()
	if affected > 0 {
		if _, err := s.db.Exec(`UPDATE route_members SET cooldown_until = NULL WHERE channel_id = ?`, channelID); err != nil {
			return fmt.Errorf("channel auto disable clear cooldown: %w", err)
		}
	}
	return nil
}

// RecoverAutoDisabled restores an auto-disabled channel and its automatically
// parked route members to enabled. It returns true when the channel was
// actually transitioned (was auto-disabled).
func (s *ChannelStore) RecoverAutoDisabled(channelID int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE channels SET status = ? WHERE id = ? AND status = ?`,
		domain.StatusEnabled, channelID, domain.StatusAutoDisabled)
	if err != nil {
		return false, fmt.Errorf("channel recover: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if _, err := s.db.Exec(`UPDATE route_members SET enabled = CASE WHEN fail_count > 0 THEN 1 ELSE enabled END, fail_count = 0, cooldown_until = NULL, last_error = '' WHERE channel_id = ?`, channelID); err != nil {
			return false, fmt.Errorf("channel recover clear health: %w", err)
		}
	}
	return n > 0, nil
}

// RecordRelayFailure increments the channel consecutive-failure counter and
// returns the new value.
func (s *ChannelStore) RecordRelayFailure(channelID int64) (int, error) {
	var next int
	if err := s.db.QueryRow(`UPDATE channels SET consecutive_failures = consecutive_failures + 1 WHERE id = ? RETURNING consecutive_failures`, channelID).Scan(&next); err != nil {
		// SQLite supports RETURNING since 3.35; fall back for older builds.
		if _, err2 := s.db.Exec(`UPDATE channels SET consecutive_failures = consecutive_failures + 1 WHERE id = ?`, channelID); err2 != nil {
			return 0, fmt.Errorf("channel relay failure: %w", err2)
		}
		_ = s.db.QueryRow(`SELECT consecutive_failures FROM channels WHERE id = ?`, channelID).Scan(&next)
	}
	return next, nil
}

// RecordRelaySuccess resets the channel consecutive-failure counter.
func (s *ChannelStore) RecordRelaySuccess(channelID int64) error {
	if _, err := s.db.Exec(`UPDATE channels SET consecutive_failures = 0 WHERE id = ?`, channelID); err != nil {
		return fmt.Errorf("channel relay success: %w", err)
	}
	return nil
}

// RecordGraySuccess counts a successful relay attempt on a stable-first
// (grayscale) channel. When the counter reaches promoteAfter with no
// consecutive failures, the channel is promoted (grayscale mark cleared) and
// promoted=true is returned. Non-grayscale channels are a no-op.
func (s *ChannelStore) RecordGraySuccess(channelID int64, promoteAfter int) (bool, error) {
	if promoteAfter <= 0 {
		return false, nil
	}
	res, err := s.db.Exec(`UPDATE channels SET stable_first_requests = stable_first_requests + 1 WHERE id = ? AND stable_first = 1`, channelID)
	if err != nil {
		return false, fmt.Errorf("channel gray success: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil // not marked (or already promoted)
	}
	var count, failures int
	if err := s.db.QueryRow(`SELECT stable_first_requests, consecutive_failures FROM channels WHERE id = ?`, channelID).Scan(&count, &failures); err != nil {
		return false, fmt.Errorf("channel gray read: %w", err)
	}
	if count >= promoteAfter && failures == 0 {
		if _, err := s.db.Exec(`UPDATE channels SET stable_first = 0, stable_first_requests = 0 WHERE id = ?`, channelID); err != nil {
			return false, fmt.Errorf("channel gray promote: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// DeriveHealthState computes the five-state channel health machine
// (Metapi-inspired): disabled > unhealthy > degraded > healthy > unknown.
func DeriveHealthState(overview domain.ChannelOverview) string {
	switch overview.Channel.Status {
	case domain.StatusDisabled:
		return domain.HealthStateDisabled
	case domain.StatusAutoDisabled:
		return domain.HealthStateUnhealthy
	}
	if !overview.LastProbeOK {
		// No probe record at all → not yet evaluated.
		if overview.LastProbeAt == nil && overview.FailureCount == 0 {
			return domain.HealthStateUnknown
		}
		return domain.HealthStateUnhealthy
	}
	if overview.LastProbeError == "probe_slow" || overview.FailureCount > 0 || overview.CoolingMemberCount > 0 {
		return domain.HealthStateDegraded
	}
	return domain.HealthStateHealthy
}

// DeriveHealthReason explains the business-health verdict without exposing
// secrets or raw upstream error text. The UI combines this stable category
// with overview counters to provide an actionable explanation.
func DeriveHealthReason(overview domain.ChannelOverview) string {
	switch overview.Channel.Status {
	case domain.StatusDisabled:
		return "manual_disabled"
	case domain.StatusAutoDisabled:
		return "auto_disabled"
	}
	if !overview.LastProbeOK {
		if overview.LastProbeAt == nil && overview.FailureCount == 0 {
			return "not_checked"
		}
		switch overview.LastProbeError {
		case "upstream_unauthorized", "account_banned":
			return "authentication_failed"
		case "user_token_not_for_models":
			return "credential_scope"
		case "credential_unavailable":
			return "credential_unavailable"
		case "invalid_base_url":
			return "invalid_base_url"
		default:
			return "probe_failed"
		}
	}
	if overview.LastProbeError == "probe_slow" {
		return "probe_slow"
	}
	if overview.CoolingMemberCount > 0 {
		return "route_cooling"
	}
	if overview.FailureCount > 0 {
		return "route_failures"
	}
	return "probe_ok"
}

// DeriveConnectivityState computes the network-layer verdict from the latest
// persisted Ping. A false boolean without a timestamp is the legacy zero
// value, not an observed failure.
func DeriveConnectivityState(overview domain.ChannelOverview) string {
	if overview.LastPingAt == nil {
		return domain.ConnectivityStateUnknown
	}
	if overview.LastPingOK {
		return domain.ConnectivityStateReachable
	}
	return domain.ConnectivityStateUnreachable
}

func (s *ChannelStore) RecordProbeSuccess(channelID int64, at time.Time) error {
	return s.RecordProbeSuccessWithVerdict(channelID, at, "")
}

// RecordProbeSuccessWithVerdict records a successful business probe and an
// optional non-error verdict such as probe_slow. Keeping the verdict beside
// the probe timestamp lets the overview API expose health-sweep degradation
// consistently with manual probes.
func (s *ChannelStore) RecordProbeSuccessWithVerdict(channelID int64, at time.Time, verdict string) error {
	_, err := s.db.Exec(`UPDATE channels SET last_probe_at=?, last_probe_ok=1, last_probe_error=?, updated_at=datetime('now') WHERE id=?`,
		at.UTC().Format(time.RFC3339Nano), verdict, channelID)
	if err != nil {
		return fmt.Errorf("channel probe success: %w", err)
	}
	return nil
}

// RecordRateLimited parks a channel until the given time (429 verdict); the
// channel is excluded from routing while parked.
func (s *ChannelStore) RecordRateLimited(channelID int64, until time.Time) error {
	_, err := s.db.Exec(`UPDATE channels SET rate_limited_until = ?, updated_at = datetime('now') WHERE id = ?`,
		until.UTC().Format(time.RFC3339Nano), channelID)
	if err != nil {
		return fmt.Errorf("channel rate limit: %w", err)
	}
	return nil
}

// ClearRateLimit lifts a channel's rate-limit pause (probe success or admin).
func (s *ChannelStore) ClearRateLimit(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channels SET rate_limited_until = NULL, updated_at = datetime('now') WHERE id = ?`, channelID)
	if err != nil {
		return fmt.Errorf("channel clear rate limit: %w", err)
	}
	return nil
}

// RecordProbeFailure stores a redacted discovery/probe failure category for UI health.
func (s *ChannelStore) RecordProbeFailure(channelID int64, at time.Time, category string) error {
	if category == "" {
		category = "upstream_failure"
	}
	_, err := s.db.Exec(`UPDATE channels SET last_probe_at=?, last_probe_ok=0, last_probe_error=?, updated_at=datetime('now') WHERE id=?`,
		at.UTC().Format(time.RFC3339Nano), category, channelID)
	if err != nil {
		return fmt.Errorf("channel probe failure: %w", err)
	}
	return nil
}

// RecordPingSuccess stores a successful connectivity ping (network reachability).
func (s *ChannelStore) RecordPingSuccess(channelID int64, at time.Time, latencyMs int) error {
	_, err := s.db.Exec(`UPDATE channels SET last_ping_at=?, last_ping_ok=1, last_ping_error='', last_ping_ms=?, updated_at=datetime('now') WHERE id=?`,
		at.UTC().Format(time.RFC3339Nano), latencyMs, channelID)
	if err != nil {
		return fmt.Errorf("channel ping success: %w", err)
	}
	return nil
}

// RecordPingFailure stores a failed connectivity ping with a redacted category.
func (s *ChannelStore) RecordPingFailure(channelID int64, at time.Time, category string) error {
	if category == "" {
		category = "unreachable"
	}
	_, err := s.db.Exec(`UPDATE channels SET last_ping_at=?, last_ping_ok=0, last_ping_error=?, last_ping_ms=0, updated_at=datetime('now') WHERE id=?`,
		at.UTC().Format(time.RFC3339Nano), category, channelID)
	if err != nil {
		return fmt.Errorf("channel ping failure: %w", err)
	}
	return nil
}

// normalizeTags trims and canonicalizes a comma-separated tag list.
func normalizeTags(raw string) string {
	seen := make(map[string]struct{})
	var out []string
	for _, part := range strings.Split(raw, ",") {
		tag := strings.TrimSpace(part)
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return strings.Join(out, ",")
}

// UpdateByTag applies a partial bulk update to every channel carrying the tag.
// Only non-nil fields are applied. Returns the number of affected channels.
func (s *ChannelStore) UpdateByTag(tag string, fields domain.ChannelPatch) (int64, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return 0, fmt.Errorf("channel bulk: empty tag")
	}
	sets := []string{}
	args := []any{}
	// Tags match via a comma-anchored pattern so "prod" does not hit
	// "production": stored tags are canonical comma-joined.
	if fields.Priority != nil {
		sets = append(sets, "priority = ?")
		args = append(args, *fields.Priority)
	}
	if fields.Weight != nil {
		sets = append(sets, "weight = ?")
		args = append(args, *fields.Weight)
	}
	if fields.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *fields.Status)
	}
	if fields.ModelsCSV != nil {
		sets = append(sets, "models_csv = ?")
		args = append(args, *fields.ModelsCSV)
	}
	if fields.GroupName != nil {
		sets = append(sets, "group_name = ?")
		args = append(args, *fields.GroupName)
	}
	if fields.RetryConfig != nil {
		sets = append(sets, "retry_config = ?")
		args = append(args, *fields.RetryConfig)
	}
	if fields.SystemPrompt != nil {
		sets = append(sets, "system_prompt = ?")
		args = append(args, *fields.SystemPrompt)
	}
	if fields.HeaderOverride != nil {
		sets = append(sets, "header_override = ?")
		args = append(args, *fields.HeaderOverride)
	}
	if len(sets) == 0 {
		return 0, nil
	}
	sets = append(sets, "updated_at = datetime('now')")
	query := `UPDATE channels SET ` + strings.Join(sets, ", ") +
		` WHERE (',' || tags || ',') LIKE ?`
	args = append(args, "%,"+tag+",%")
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("channel bulk update: %w", err)
	}
	return res.RowsAffected()
}
