package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// RuntimeSettingsRow is the durable Admin override document for hot-reloadable params.
// Pointer/null fields mean "not overridden" when HasOverride is false.
// When HasOverride is true, all editable fields are set from Admin.
type RuntimeSettingsRow struct {
	HasOverride                 bool
	RetryTimes                  int
	CooldownSeconds             int
	CheckinEnabled              bool
	CheckinCron                 string
	RelayRatePerMinute          int
	RelayRateBurst              int
	AdminRatePerMinute          int
	AdminRateBurst              int
	AuditRetentionDays          int
	AuditRetentionRows          int
	ChannelAutoDisableThreshold int
	RoutingLatencyAware         int
	UpdatedAt                   time.Time
}

// RuntimeSettingsStore persists a single-row runtime settings document.
type RuntimeSettingsStore struct {
	db *sql.DB
}

func (s *RuntimeSettingsStore) Get() (*RuntimeSettingsRow, error) {
	row := s.db.QueryRow(`
		SELECT has_override, retry_times, cooldown_seconds, checkin_enabled, checkin_cron,
		       relay_rate_per_minute, relay_rate_burst, admin_rate_per_minute, admin_rate_burst,
		       audit_retention_days, audit_retention_rows,
		       channel_auto_disable_threshold, routing_latency_aware, updated_at
		FROM runtime_settings WHERE id = 1`)
	var (
		hasOverride                                            int
		retry, cooldown, checkinEnabled, relayRate, relayBurst sql.NullInt64
		adminRate, adminBurst, auditDays, auditRows            sql.NullInt64
		autoDisableThreshold, latencyAware                     sql.NullInt64
		cron, updated                                          sql.NullString
	)
	if err := row.Scan(
		&hasOverride, &retry, &cooldown, &checkinEnabled, &cron,
		&relayRate, &relayBurst, &adminRate, &adminBurst,
		&auditDays, &auditRows,
		&autoDisableThreshold, &latencyAware, &updated,
	); err != nil {
		if err == sql.ErrNoRows {
			return &RuntimeSettingsRow{}, nil
		}
		return nil, fmt.Errorf("runtime settings get: %w", err)
	}
	out := &RuntimeSettingsRow{HasOverride: hasOverride != 0}
	if retry.Valid {
		out.RetryTimes = int(retry.Int64)
	}
	if cooldown.Valid {
		out.CooldownSeconds = int(cooldown.Int64)
	}
	if checkinEnabled.Valid {
		out.CheckinEnabled = checkinEnabled.Int64 != 0
	}
	if cron.Valid {
		out.CheckinCron = strings.TrimSpace(cron.String)
	}
	if relayRate.Valid {
		out.RelayRatePerMinute = int(relayRate.Int64)
	}
	if relayBurst.Valid {
		out.RelayRateBurst = int(relayBurst.Int64)
	}
	if adminRate.Valid {
		out.AdminRatePerMinute = int(adminRate.Int64)
	}
	if adminBurst.Valid {
		out.AdminRateBurst = int(adminBurst.Int64)
	}
	if auditDays.Valid {
		out.AuditRetentionDays = int(auditDays.Int64)
	}
	if auditRows.Valid {
		out.AuditRetentionRows = int(auditRows.Int64)
	}
	if autoDisableThreshold.Valid {
		out.ChannelAutoDisableThreshold = int(autoDisableThreshold.Int64)
	} else {
		// NULL means "not overridden yet" — follow env bootstrap.
		out.ChannelAutoDisableThreshold = -1
	}
	if latencyAware.Valid {
		out.RoutingLatencyAware = int(latencyAware.Int64)
	} else {
		out.RoutingLatencyAware = -1
	}
	if updated.Valid {
		if parsed, err := time.Parse("2006-01-02 15:04:05", updated.String); err == nil {
			out.UpdatedAt = parsed.UTC()
		} else if parsed, err := time.Parse(time.RFC3339Nano, updated.String); err == nil {
			out.UpdatedAt = parsed.UTC()
		}
	}
	return out, nil
}

func (s *RuntimeSettingsStore) Save(settings *RuntimeSettingsRow) error {
	if settings == nil {
		return fmt.Errorf("runtime settings save: nil")
	}
	hasOverride := 0
	if settings.HasOverride {
		hasOverride = 1
	}
	checkinEnabled := 0
	if settings.CheckinEnabled {
		checkinEnabled = 1
	}
	cron := strings.TrimSpace(settings.CheckinCron)
	latencyState := settings.RoutingLatencyAware
	if settings.RoutingLatencyAware == 0 || settings.RoutingLatencyAware == -1 {
		latencyState = 1 // default on
	}
	_, err := s.db.Exec(`
		INSERT INTO runtime_settings (
			id, has_override, retry_times, cooldown_seconds, checkin_enabled, checkin_cron,
			relay_rate_per_minute, relay_rate_burst, admin_rate_per_minute, admin_rate_burst,
			audit_retention_days, audit_retention_rows,
			channel_auto_disable_threshold, routing_latency_aware, updated_at
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			has_override = excluded.has_override,
			retry_times = excluded.retry_times,
			cooldown_seconds = excluded.cooldown_seconds,
			checkin_enabled = excluded.checkin_enabled,
			checkin_cron = excluded.checkin_cron,
			relay_rate_per_minute = excluded.relay_rate_per_minute,
			relay_rate_burst = excluded.relay_rate_burst,
			admin_rate_per_minute = excluded.admin_rate_per_minute,
			admin_rate_burst = excluded.admin_rate_burst,
			audit_retention_days = excluded.audit_retention_days,
			audit_retention_rows = excluded.audit_retention_rows,
			channel_auto_disable_threshold = excluded.channel_auto_disable_threshold,
			routing_latency_aware = excluded.routing_latency_aware,
			updated_at = datetime('now')`,
		hasOverride,
		settings.RetryTimes,
		settings.CooldownSeconds,
		checkinEnabled,
		cron,
		settings.RelayRatePerMinute,
		settings.RelayRateBurst,
		settings.AdminRatePerMinute,
		settings.AdminRateBurst,
		settings.AuditRetentionDays,
		settings.AuditRetentionRows,
		settings.ChannelAutoDisableThreshold,
		latencyState,
	)
	if err != nil {
		return fmt.Errorf("runtime settings save: %w", err)
	}
	return nil
}
