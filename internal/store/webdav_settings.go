package store

import (
	"database/sql"
	"strings"
	"time"
)

// WebDAVSettings is the durable Admin WebDAV pull configuration.
// Password fields hold ciphertext (or empty).
type WebDAVSettings struct {
	// HasOverride distinguishes an explicit Admin choice (including disabled)
	// from the untouched bootstrap row, allowing the operator to override a
	// fully configured WEBDAV_* environment without deleting secrets.
	HasOverride       bool
	Enabled           bool
	URL               string
	Username          string
	PasswordEnc       string
	BackupPasswordEnc string
	CronExpr          string
	UpdatedAt         time.Time
}

// WebDAVSettingsStore persists a single-row WebDAV settings document.
type WebDAVSettingsStore struct {
	db *sql.DB
}

func (s *WebDAVSettingsStore) Get() (*WebDAVSettings, error) {
	row := s.db.QueryRow(`
		SELECT has_override, enabled, url, username, password_enc, backup_password_enc, cron_expr, updated_at
		FROM webdav_settings WHERE id = 1`)
	var hasOverride, enabled int
	var settings WebDAVSettings
	var updated string
	if err := row.Scan(
		&hasOverride,
		&enabled,
		&settings.URL,
		&settings.Username,
		&settings.PasswordEnc,
		&settings.BackupPasswordEnc,
		&settings.CronExpr,
		&updated,
	); err != nil {
		if err == sql.ErrNoRows {
			return &WebDAVSettings{CronExpr: "0 */6 * * *"}, nil
		}
		return nil, err
	}
	settings.HasOverride = hasOverride != 0
	settings.Enabled = enabled != 0
	if parsed, err := time.Parse("2006-01-02 15:04:05", updated); err == nil {
		settings.UpdatedAt = parsed.UTC()
	}
	if strings.TrimSpace(settings.CronExpr) == "" {
		settings.CronExpr = "0 */6 * * *"
	}
	return &settings, nil
}

func (s *WebDAVSettingsStore) Save(settings *WebDAVSettings) error {
	if settings == nil {
		return sql.ErrNoRows
	}
	cron := strings.TrimSpace(settings.CronExpr)
	if cron == "" {
		cron = "0 */6 * * *"
	}
	hasOverride := 0
	if settings.HasOverride {
		hasOverride = 1
	}
	enabled := 0
	if settings.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO webdav_settings (id, has_override, enabled, url, username, password_enc, backup_password_enc, cron_expr, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			has_override = excluded.has_override,
			enabled = excluded.enabled,
			url = excluded.url,
			username = excluded.username,
			password_enc = excluded.password_enc,
			backup_password_enc = excluded.backup_password_enc,
			cron_expr = excluded.cron_expr,
			updated_at = datetime('now')`,
		hasOverride,
		enabled,
		strings.TrimSpace(settings.URL),
		settings.Username,
		settings.PasswordEnc,
		settings.BackupPasswordEnc,
		cron,
	)
	return err
}
