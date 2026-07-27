package store

import (
	"database/sql"
	"fmt"
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
	return scanner.Scan(
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
		scanTime(&r.CreatedAt),
		scanTime(&r.UpdatedAt),
	)
}

func (s *ChannelStore) List() ([]domain.Channel, error) {
	rows, err := s.db.Query(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, created_at, updated_at FROM channels ORDER BY id`)
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
		c.priority, c.weight, c.status, c.type_hint, c.created_at, c.updated_at,
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
		(SELECT COUNT(*) FROM discovered_models dm WHERE dm.channel_id = c.id AND dm.available = 1),
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
		COALESCE(c.last_probe_error, '')
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
		var checkinEnabled, hasUserCredential, hasPlatformUserID, hasAPIKey, siteUsable, credentialUsable, lastProbeOK int
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
		); err != nil {
			return nil, fmt.Errorf("channel overview scan: %w", err)
		}
		overview.CheckinEnabled = checkinEnabled != 0
		overview.HasUserCredential = hasUserCredential != 0
		overview.HasPlatformUserID = hasPlatformUserID != 0
		overview.LastProbeOK = lastProbeOK != 0
		overview.HasAPIKey = hasAPIKey != 0
		overview.SiteUsable = siteUsable != 0
		overview.CredentialUsable = credentialUsable != 0
		result = append(result, overview)
	}
	return result, rows.Err()
}

// ListEnabled returns all enabled channels.
func (s *ChannelStore) ListEnabled() ([]domain.Channel, error) {
	rows, err := s.db.Query(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, created_at, updated_at FROM channels WHERE status = ? ORDER BY priority, id`, domain.StatusEnabled)
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

func (s *ChannelStore) GetByID(id int64) (*domain.Channel, error) {
	row := s.db.QueryRow(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, created_at, updated_at FROM channels WHERE id = ?`, id)
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
	res, err := s.db.Exec(`INSERT INTO channels (site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.SiteID, c.CredentialID, c.Name, c.BaseURL, c.ModelsCSV, c.GroupName, c.Priority, c.Weight, c.Status, c.TypeHint)
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
	if _, err = tx.Exec(`UPDATE channels SET site_id=?, credential_id=?, name=?, base_url=?, models_csv=?, group_name=?, priority=?, weight=?, status=?, type_hint=?, updated_at=datetime('now') WHERE id=?`,
		c.SiteID, c.CredentialID, c.Name, c.BaseURL, c.ModelsCSV, c.GroupName, c.Priority, c.Weight, c.Status, c.TypeHint, c.ID); err != nil {
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
func (s *ChannelStore) RecordProbeSuccess(channelID int64, at time.Time) error {
	_, err := s.db.Exec(`UPDATE channels SET last_probe_at=?, last_probe_ok=1, last_probe_error='', updated_at=datetime('now') WHERE id=?`,
		at.UTC().Format(time.RFC3339Nano), channelID)
	if err != nil {
		return fmt.Errorf("channel probe success: %w", err)
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
