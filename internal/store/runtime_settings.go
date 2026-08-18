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
	HasOverride                  bool
	RetryTimes                   int
	CrossChannelFailoverEnabled  int
	CooldownSeconds              int
	CheckinEnabled               bool
	CheckinCron                  string
	RelayRatePerMinute           int
	RelayRateBurst               int
	AdminRatePerMinute           int
	AdminRateBurst               int
	AuditRetentionDays           int
	AuditRetentionRows           int
	ChannelAutoDisableThreshold  int
	RoutingLatencyAware          int
	RoutingErrorAware            int
	RoutingConcurrencyEnabled    int
	RoutingConcurrencyLimit      int
	WebhookURL                   string
	ProxyURL                     string
	DiscoveryCron                string
	DBGCCron                     string
	WebhookThrottleSeconds       int
	StableFirstEnabled           int
	StableFirstDenominator       int
	StableFirstPromoteRequests   int
	RecoveryProbeEnabled         int
	RecoveryProbeIntervalSeconds int
	FaultProtectionEnabled       int
	StickyEnabled                int
	StickyTTLMinutes             int
	// AlertConfigJSON is the multi-channel alert matrix (webhook/bark/
	// serverchan/telegram/smtp), JSON-encoded; "" = use env bootstrap.
	AlertConfigJSON string
	// AlertSweepIntervalSeconds: proactive health sweep cadence (0 = off).
	AlertSweepIntervalSeconds int
	// AlertDailySummaryIntervalSeconds: daily digest cadence (0 = off).
	AlertDailySummaryIntervalSeconds int
	// HealthSweepEnabled/IntervalSeconds/JitterSeconds/DegradedMs/
	// Concurrency/TimeoutSeconds: periodic channel health sweep (grades
	// operational/degraded/error and alerts on transitions). -1 = env bootstrap.
	HealthSweepEnabled         int
	HealthSweepIntervalSeconds int
	HealthSweepJitterSeconds   int
	HealthSweepDegradedMs      int
	HealthSweepConcurrency     int
	HealthSweepTimeoutSeconds  int
	// ChannelRetryTimes: how many times the same upstream key is re-sent after
	// a retryable failure before moving to the next key/channel. -1 = env.
	ChannelRetryTimes int
	// KeyPoolRotation: rotate through the site key pool on failure. -1 = env.
	KeyPoolRotation int
	UpdatedAt       time.Time
}

// RuntimeSettingsStore persists a single-row runtime settings document.
type RuntimeSettingsStore struct {
	db *sql.DB
}

func (s *RuntimeSettingsStore) Get() (*RuntimeSettingsRow, error) {
	row := s.db.QueryRow(`
		SELECT has_override, retry_times, cross_channel_failover_enabled, cooldown_seconds, checkin_enabled, checkin_cron,
		       relay_rate_per_minute, relay_rate_burst, admin_rate_per_minute, admin_rate_burst,
		       audit_retention_days, audit_retention_rows,
		       channel_auto_disable_threshold, routing_latency_aware,
		       routing_error_aware,
		       routing_concurrency_enabled, routing_concurrency_limit,
		       webhook_url, webhook_throttle_seconds,
		       proxy_url,
		       discovery_cron,
		       db_gc_cron,
		       stable_first_enabled, stable_first_denominator, stable_first_promote_requests,
	       recovery_probe_enabled, recovery_probe_interval_seconds, fault_protection_enabled,
	       sticky_enabled, sticky_ttl_minutes,
		       alert_config_json, alert_sweep_interval_seconds, alert_daily_summary_interval_seconds,
		       health_sweep_enabled, health_sweep_interval_seconds, health_sweep_jitter_seconds,
		       health_sweep_degraded_ms, health_sweep_concurrency, health_sweep_timeout_seconds,
		       channel_retry_times,
		       key_pool_rotation,
		       updated_at
		FROM runtime_settings WHERE id = 1`)
	var (
		hasOverride                                                                        int
		retry, crossChannelFailover, cooldown, checkinEnabled, relayRate, relayBurst       sql.NullInt64
		adminRate, adminBurst, auditDays, auditRows                                        sql.NullInt64
		autoDisableThreshold, latencyAware, errorAware, concurrencyAware, concurrencyLimit sql.NullInt64
		webhookURL                                                                         sql.NullString
		proxyURL                                                                           sql.NullString
		discoveryCron                                                                      sql.NullString
		dbGCCron                                                                           sql.NullString
		webhookThrottle                                                                    sql.NullInt64
		sfEnabled, sfDenominator, sfPromote                                                sql.NullInt64
		recovery, recoveryInterval, faultProtection                                        sql.NullInt64
		stickyEnabled, stickyTTL                                                           sql.NullInt64
		alertConfigJSON                                                                    sql.NullString
		alertSweep, alertDaily                                                             sql.NullInt64
		hsEnabled, hsInterval, hsJitter, hsDegraded, hsConcurrency, hsTimeout              sql.NullInt64
		channelRetry                                                                       sql.NullInt64
		keyPoolRotation                                                                    sql.NullInt64
		cron, updated                                                                      sql.NullString
	)
	if err := row.Scan(
		&hasOverride, &retry, &crossChannelFailover, &cooldown, &checkinEnabled, &cron,
		&relayRate, &relayBurst, &adminRate, &adminBurst,
		&auditDays, &auditRows,
		&autoDisableThreshold, &latencyAware, &errorAware,
		&concurrencyAware, &concurrencyLimit,
		&webhookURL, &webhookThrottle, &proxyURL, &discoveryCron, &dbGCCron,
		&sfEnabled, &sfDenominator, &sfPromote,
		&recovery, &recoveryInterval, &faultProtection,
		&stickyEnabled, &stickyTTL,
		&alertConfigJSON, &alertSweep, &alertDaily,
		&hsEnabled, &hsInterval, &hsJitter, &hsDegraded, &hsConcurrency, &hsTimeout,
		&channelRetry, &keyPoolRotation, &updated,
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
	if crossChannelFailover.Valid {
		out.CrossChannelFailoverEnabled = int(crossChannelFailover.Int64)
	} else {
		out.CrossChannelFailoverEnabled = -1
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
	if proxyURL.Valid {
		out.ProxyURL = strings.TrimSpace(proxyURL.String)
	}
	if discoveryCron.Valid {
		out.DiscoveryCron = strings.TrimSpace(discoveryCron.String)
	}
	if dbGCCron.Valid {
		out.DBGCCron = strings.TrimSpace(dbGCCron.String)
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
	if faultProtection.Valid {
		out.FaultProtectionEnabled = int(faultProtection.Int64)
	} else {
		out.FaultProtectionEnabled = -1
	}
	if stickyEnabled.Valid {
		out.StickyEnabled = int(stickyEnabled.Int64)
	} else {
		out.StickyEnabled = -1
	}
	if stickyTTL.Valid {
		out.StickyTTLMinutes = int(stickyTTL.Int64)
	} else {
		out.StickyTTLMinutes = -1
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
	if hsEnabled.Valid {
		out.HealthSweepEnabled = int(hsEnabled.Int64)
	} else {
		out.HealthSweepEnabled = -1
	}
	if hsInterval.Valid {
		out.HealthSweepIntervalSeconds = int(hsInterval.Int64)
	} else {
		out.HealthSweepIntervalSeconds = -1
	}
	if hsJitter.Valid {
		out.HealthSweepJitterSeconds = int(hsJitter.Int64)
	} else {
		out.HealthSweepJitterSeconds = -1
	}
	if hsDegraded.Valid {
		out.HealthSweepDegradedMs = int(hsDegraded.Int64)
	} else {
		out.HealthSweepDegradedMs = -1
	}
	if hsConcurrency.Valid {
		out.HealthSweepConcurrency = int(hsConcurrency.Int64)
	} else {
		out.HealthSweepConcurrency = -1
	}
	if hsTimeout.Valid {
		out.HealthSweepTimeoutSeconds = int(hsTimeout.Int64)
	} else {
		out.HealthSweepTimeoutSeconds = -1
	}
	if channelRetry.Valid {
		out.ChannelRetryTimes = int(channelRetry.Int64)
	} else {
		out.ChannelRetryTimes = -1
	}
	if keyPoolRotation.Valid {
		out.KeyPoolRotation = int(keyPoolRotation.Int64)
	} else {
		out.KeyPoolRotation = -1
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
	if settings.RoutingLatencyAware == -1 {
		latencyState = 1 // unset → default on
	}
	_, err := s.db.Exec(`
		INSERT INTO runtime_settings (
			id, has_override, retry_times, cross_channel_failover_enabled, cooldown_seconds, checkin_enabled, checkin_cron,
			relay_rate_per_minute, relay_rate_burst, admin_rate_per_minute, admin_rate_burst,
			audit_retention_days, audit_retention_rows,
			channel_auto_disable_threshold, routing_latency_aware,
			routing_error_aware,
			routing_concurrency_enabled, routing_concurrency_limit,
			webhook_url, webhook_throttle_seconds,
			proxy_url,
			discovery_cron, db_gc_cron,
			stable_first_enabled, stable_first_denominator, stable_first_promote_requests,
			recovery_probe_enabled, recovery_probe_interval_seconds, fault_protection_enabled,
			sticky_enabled, sticky_ttl_minutes,
			alert_config_json, alert_sweep_interval_seconds, alert_daily_summary_interval_seconds,
			health_sweep_enabled, health_sweep_interval_seconds, health_sweep_jitter_seconds,
			health_sweep_degraded_ms, health_sweep_concurrency, health_sweep_timeout_seconds,
			channel_retry_times,
			key_pool_rotation,
			updated_at
		) VALUES (
			1, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?,
			?, ?,
			?,
			?, ?,
			?, ?,
			?,
			?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?,
			?,
			datetime('now')
		)
		ON CONFLICT(id) DO UPDATE SET
			has_override = excluded.has_override,
			retry_times = excluded.retry_times,
			cross_channel_failover_enabled = excluded.cross_channel_failover_enabled,
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
			proxy_url = excluded.proxy_url,
			discovery_cron = excluded.discovery_cron,
			db_gc_cron = excluded.db_gc_cron,
			stable_first_enabled = excluded.stable_first_enabled,
			stable_first_denominator = excluded.stable_first_denominator,
			stable_first_promote_requests = excluded.stable_first_promote_requests,
			recovery_probe_enabled = excluded.recovery_probe_enabled,
			recovery_probe_interval_seconds = excluded.recovery_probe_interval_seconds,
			fault_protection_enabled = excluded.fault_protection_enabled,
			sticky_enabled = excluded.sticky_enabled,
			sticky_ttl_minutes = excluded.sticky_ttl_minutes,
			alert_config_json = excluded.alert_config_json,
			alert_sweep_interval_seconds = excluded.alert_sweep_interval_seconds,
			alert_daily_summary_interval_seconds = excluded.alert_daily_summary_interval_seconds,
			health_sweep_enabled = excluded.health_sweep_enabled,
			health_sweep_interval_seconds = excluded.health_sweep_interval_seconds,
			health_sweep_jitter_seconds = excluded.health_sweep_jitter_seconds,
			health_sweep_degraded_ms = excluded.health_sweep_degraded_ms,
			health_sweep_concurrency = excluded.health_sweep_concurrency,
			health_sweep_timeout_seconds = excluded.health_sweep_timeout_seconds,
			channel_retry_times = excluded.channel_retry_times,
			key_pool_rotation = excluded.key_pool_rotation,
			updated_at = datetime('now')`,
		hasOverride,
		settings.RetryTimes,
		settings.CrossChannelFailoverEnabled,
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
		settings.ProxyURL,
		settings.DiscoveryCron,
		settings.DBGCCron,
		settings.StableFirstEnabled,
		settings.StableFirstDenominator,
		settings.StableFirstPromoteRequests,
		settings.RecoveryProbeEnabled,
		settings.RecoveryProbeIntervalSeconds,
		settings.FaultProtectionEnabled,
		settings.StickyEnabled,
		settings.StickyTTLMinutes,
		settings.AlertConfigJSON,
		settings.AlertSweepIntervalSeconds,
		settings.AlertDailySummaryIntervalSeconds,
		settings.HealthSweepEnabled,
		settings.HealthSweepIntervalSeconds,
		settings.HealthSweepJitterSeconds,
		settings.HealthSweepDegradedMs,
		settings.HealthSweepConcurrency,
		settings.HealthSweepTimeoutSeconds,
		settings.ChannelRetryTimes,
		settings.KeyPoolRotation,
	)
	if err != nil {
		return fmt.Errorf("runtime settings save: %w", err)
	}
	return nil
}
