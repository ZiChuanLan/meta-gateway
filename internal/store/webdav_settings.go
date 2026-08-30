package store

import (
	"database/sql"
	"strings"
	"time"
)

// WebDAVSettings is the durable Admin WebDAV sync configuration.
// Password fields hold ciphertext (or empty).
//
// The two sync directions own a fully independent connection: URL/Username/
// PasswordEnc/BackupPasswordEnc serve the AAH/exchange import, Upload* columns
// serve the native backup upload. Enabled and UploadEnabled gate their own
// direction, and each direction carries its own cron ("off" disarms that
// schedule); CronExpr is the legacy shared column kept for fallback.
type WebDAVSettings struct {
	// HasOverride distinguishes an explicit Admin choice (including disabled)
	// from the untouched bootstrap row, allowing the operator to override a
	// fully configured WEBDAV_* environment without deleting secrets.
	HasOverride             bool
	Enabled                 bool
	UploadEnabled           bool
	URL                     string
	Username                string
	PasswordEnc             string
	BackupPasswordEnc       string
	UploadURL               string
	UploadUsername          string
	UploadPasswordEnc       string
	UploadBackupPasswordEnc string
	CronExpr                string
	DownloadCron            string
	UploadCron              string
	UpdatedAt               time.Time
}

// WebDAVSettingsStore persists a single-row WebDAV settings document.
type WebDAVSettingsStore struct {
	db *sql.DB
}

const webdavSettingsColumns = `
		has_override, enabled, upload_enabled, url, username, password_enc, backup_password_enc,
		upload_url, upload_username, upload_password_enc, upload_backup_password_enc,
		cron_expr, download_cron, upload_cron, updated_at`

func (s *WebDAVSettingsStore) Get() (*WebDAVSettings, error) {
	row := s.db.QueryRow(`
		SELECT ` + webdavSettingsColumns + `
		FROM webdav_settings WHERE id = 1`)
	var hasOverride, enabled, uploadEnabled int
	var settings WebDAVSettings
	var updated string
	if err := row.Scan(
		&hasOverride,
		&enabled,
		&uploadEnabled,
		&settings.URL,
		&settings.Username,
		&settings.PasswordEnc,
		&settings.BackupPasswordEnc,
		&settings.UploadURL,
		&settings.UploadUsername,
		&settings.UploadPasswordEnc,
		&settings.UploadBackupPasswordEnc,
		&settings.CronExpr,
		&settings.DownloadCron,
		&settings.UploadCron,
		&updated,
	); err != nil {
		if err == sql.ErrNoRows {
			return &WebDAVSettings{CronExpr: "0 */6 * * *"}, nil
		}
		return nil, err
	}
	settings.HasOverride = hasOverride != 0
	settings.Enabled = enabled != 0
	settings.UploadEnabled = uploadEnabled != 0
	if parsed, err := time.Parse("2006-01-02 15:04:05", updated); err == nil {
		settings.UpdatedAt = parsed.UTC()
	}
	if strings.TrimSpace(settings.CronExpr) == "" {
		settings.CronExpr = "0 */6 * * *"
	}
	if strings.TrimSpace(settings.DownloadCron) == "" {
		settings.DownloadCron = settings.CronExpr
	}
	if strings.TrimSpace(settings.UploadCron) == "" {
		settings.UploadCron = settings.CronExpr
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
	downloadCron := strings.TrimSpace(settings.DownloadCron)
	if downloadCron == "" {
		downloadCron = cron
	}
	uploadCron := strings.TrimSpace(settings.UploadCron)
	if uploadCron == "" {
		uploadCron = cron
	}
	hasOverride := 0
	if settings.HasOverride {
		hasOverride = 1
	}
	enabled := 0
	if settings.Enabled {
		enabled = 1
	}
	uploadEnabled := 0
	if settings.UploadEnabled {
		uploadEnabled = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO webdav_settings (
			id, has_override, enabled, upload_enabled, url, username, password_enc, backup_password_enc,
			upload_url, upload_username, upload_password_enc, upload_backup_password_enc,
			cron_expr, download_cron, upload_cron, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			has_override = excluded.has_override,
			enabled = excluded.enabled,
			upload_enabled = excluded.upload_enabled,
			url = excluded.url,
			username = excluded.username,
			password_enc = excluded.password_enc,
			backup_password_enc = excluded.backup_password_enc,
			upload_url = excluded.upload_url,
			upload_username = excluded.upload_username,
			upload_password_enc = excluded.upload_password_enc,
			upload_backup_password_enc = excluded.upload_backup_password_enc,
			cron_expr = excluded.cron_expr,
			download_cron = excluded.download_cron,
			upload_cron = excluded.upload_cron,
			updated_at = datetime('now')`,
		hasOverride,
		enabled,
		uploadEnabled,
		strings.TrimSpace(settings.URL),
		settings.Username,
		settings.PasswordEnc,
		settings.BackupPasswordEnc,
		strings.TrimSpace(settings.UploadURL),
		settings.UploadUsername,
		settings.UploadPasswordEnc,
		settings.UploadBackupPasswordEnc,
		cron,
		downloadCron,
		uploadCron,
	)
	return err
}
