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
	RoutingErrorAware           int
	RoutingConcurrencyEnabled   int
	RoutingConcurrencyLimit     int
	WebhookURL                  string
	WebhookThrottleSeconds      int
	StableFirstEnabled          int
	StableFirstDenominator      int
	StableFirstPromoteRequests  int
	RecoveryProbeEnabled        int
	RecoveryProbeIntervalSeconds int
	ProgressiveCooldownEnabled  int
	CooldownLevel2Seconds       int
	CooldownLevel3Seconds       int
	CooldownLevel4Seconds       int
	BreakerFailCount            int
	// AlertConfigJSON is the multi-channel alert matrix (webhook/bark/
	// serverchan/telegram/smtp), JSON-encoded; "" = use env bootstrap.
	AlertConfigJSON string
	// AlertSweepIntervalSeconds: proactive health sweep cadence (0 = off).
	AlertSweepIntervalSeconds int
	// AlertDailySummaryIntervalSeconds: daily digest cadence (0 = off).
	AlertDailySummaryIntervalSeconds int
	UpdatedAt                       time.Time
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
		       channel_auto_disable_threshold, routing_latency_aware,
		       routing_error_aware,
		       routing_concurrency_enabled, routing_concurrency_limit,
		       webhook_url, webhook_throttle_seconds,
		       stable_first_enabled, stable_first_denominator, stable_first_promote_requests,
		       recovery_probe_enabled, recovery_probe_interval_seconds,
		       progressive_cooldown_enabled, cooldown_level2_seconds, cooldown_level3_seconds,
		       cooldown_level4_seconds, breaker_fail_count,
		       alert_config_json, alert_sweep_interval_seconds, alert_daily_summary_interval_seconds,
		       updated_at
		FROM runtime_settings WHERE id = 1`)
	var (
		hasOverride                                                                   int
		retry, cooldown, checkinEnabled, relayRate, relayBurst                       sql.NullInt64
		adminRate, adminBurst, auditDays, auditRows                                   sql.NullInt64
		autoDisableThreshold, latencyAware, errorAware, concurrencyAware, concurrencyLimit sql.NullInt64
		webhookURL                                                                   sql.NullString
		webhookThrottle                                                              sql.NullInt64
		sfEnabled, sfDenominator, sfPromote                               sql.NullInt64
		recovery, recoveryInterval                                                    sql.NullInt64
		progressive, level2, level3, level4                                           sql.NullInt64
		breakerCount                                                                  sql.NullInt64
		alertConfigJSON                                                              sql.NullString
		alertSweep, alertDaily                                                        sql.NullInt64
		cron, updated                                                                 sql.NullString
	)
	if err := row.Scan(
		&hasOverride, &retry, &cooldown, &checkinEnabled, &cron,
		&relayRate, &relayBurst, &adminRate, &adminBurst,
		&auditDays, &auditRows,
		&autoDisableThreshold, &latencyAware, &errorAware,
		&concurrencyAware, &concurrencyLimit,
		&webhookURL, &webhookThrottle,
		&sfEnabled, &sfDenominator, &sfPromote,
		&recovery, &recoveryInterval,
		&progressive, &level2, &level3, &level4, &breakerCount,
		&alertConfigJSON, &alertSweep, &alertDaily, &updated,
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
	if errorAware.Valid {
		out.RoutingErrorAware = int(errorAware.Int64)
	} else {
		out.RoutingErrorAware = -1
	}
	if concurrencyAware.Valid {
		out.RoutingConcurrencyEnabled = int(concurrencyAware.Int64)
	} else {
		out.RoutingConcurrencyEnabled = -1
	}
	if concurrencyLimit.Valid {
		out.RoutingConcurrencyLimit = int(concurrencyLimit.Int64)
	} else {
		out.RoutingConcurrencyLimit = -1
	}
	if webhookURL.Valid {
		out.WebhookURL = strings.TrimSpace(webhookURL.String)
	}
	if webhookThrottle.Valid {
		out.WebhookThrottleSeconds = int(webhookThrottle.Int64)
	} else {
		out.WebhookThrottleSeconds = -1
	}
	if sfEnabled.Valid {
		out.StableFirstEnabled = int(sfEnabled.Int64)
	} else {
		out.StableFirstEnabled = -1
	}
	if sfDenominator.Valid {
		out.StableFirstDenominator = int(sfDenominator.Int64)
	} else {
		out.StableFirstDenominator = -1
	}
	if sfPromote.Valid {
		out.StableFirstPromoteRequests = int(sfPromote.Int64)
	} else {
		out.StableFirstPromoteRequests = -1
	}
	if recovery.Valid {
		out.RecoveryProbeEnabled = int(recovery.Int64)
	} else {
		out.RecoveryProbeEnabled = -1
	}
	if recoveryInterval.Valid {
		out.RecoveryProbeIntervalSeconds = int(recoveryInterval.Int64)
	} else {
		out.RecoveryProbeIntervalSeconds = -1
	}
	if progressive.Valid {
		out.ProgressiveCooldownEnabled = int(progressive.Int64)
	} else {
		out.ProgressiveCooldownEnabled = -1
	}
	if level2.Valid {
		out.CooldownLevel2Seconds = int(level2.Int64)
	} else {
		out.CooldownLevel2Seconds = -1
	}
	if level3.Valid {
		out.CooldownLevel3Seconds = int(level3.Int64)
	} else {
		out.CooldownLevel3Seconds = -1
	}
	if level4.Valid {
		out.CooldownLevel4Seconds = int(level4.Int64)
	} else {
		out.CooldownLevel4Seconds = -1
	}
	if breakerCount.Valid {
		out.BreakerFailCount = int(breakerCount.Int64)
	} else {
		out.BreakerFailCount = -1
	}
	if alertConfigJSON.Valid {
		out.AlertConfigJSON = strings.TrimSpace(alertConfigJSON.String)
	}
	if alertSweep.Valid {
		out.AlertSweepIntervalSeconds = int(alertSweep.Int64)
	} else {
		out.AlertSweepIntervalSeconds = -1
	}
	if alertDaily.Valid {
		out.AlertDailySummaryIntervalSeconds = int(alertDaily.Int64)
	} else {
		out.AlertDailySummaryIntervalSeconds = -1
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
			channel_auto_disable_threshold, routing_latency_aware,
			routing_error_aware,
			routing_concurrency_enabled, routing_concurrency_limit,
			webhook_url, webhook_throttle_seconds,
			stable_first_enabled, stable_first_denominator, stable_first_promote_requests,
			recovery_probe_enabled, recovery_probe_interval_seconds,
			progressive_cooldown_enabled, cooldown_level2_seconds, cooldown_level3_seconds,
			cooldown_level4_seconds, breaker_fail_count,
			alert_config_json, alert_sweep_interval_seconds, alert_daily_summary_interval_seconds,
			updated_at
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
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
			routing_error_aware = excluded.routing_error_aware,
			routing_concurrency_enabled = excluded.routing_concurrency_enabled,
			routing_concurrency_limit = excluded.routing_concurrency_limit,
			webhook_url = excluded.webhook_url,
			webhook_throttle_seconds = excluded.webhook_throttle_seconds,
			stable_first_enabled = excluded.stable_first_enabled,
			stable_first_denominator = excluded.stable_first_denominator,
			stable_first_promote_requests = excluded.stable_first_promote_requests,
			recovery_probe_enabled = excluded.recovery_probe_enabled,
			recovery_probe_interval_seconds = excluded.recovery_probe_interval_seconds,
			progressive_cooldown_enabled = excluded.progressive_cooldown_enabled,
			cooldown_level2_seconds = excluded.cooldown_level2_seconds,
			cooldown_level3_seconds = excluded.cooldown_level3_seconds,
			cooldown_level4_seconds = excluded.cooldown_level4_seconds,
			breaker_fail_count = excluded.breaker_fail_count,
			alert_config_json = excluded.alert_config_json,
			alert_sweep_interval_seconds = excluded.alert_sweep_interval_seconds,
			alert_daily_summary_interval_seconds = excluded.alert_daily_summary_interval_seconds,
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
		settings.RoutingErrorAware,
		settings.RoutingConcurrencyEnabled,
		settings.RoutingConcurrencyLimit,
		settings.WebhookURL,
		settings.WebhookThrottleSeconds,
		settings.StableFirstEnabled,
		settings.StableFirstDenominator,
		settings.StableFirstPromoteRequests,
		settings.RecoveryProbeEnabled,
		settings.RecoveryProbeIntervalSeconds,
		settings.ProgressiveCooldownEnabled,
		settings.CooldownLevel2Seconds,
		settings.CooldownLevel3Seconds,
		settings.CooldownLevel4Seconds,
		settings.BreakerFailCount,
		settings.AlertConfigJSON,
		settings.AlertSweepIntervalSeconds,
		settings.AlertDailySummaryIntervalSeconds,
	)
	if err != nil {
		return fmt.Errorf("runtime settings save: %w", err)
	}
	return nil
}
