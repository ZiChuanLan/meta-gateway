package store

import (
	"database/sql"
	"encoding/json"
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
	// DefaultModelSyncMode is the sync mode new channels get when the create
	// request omits model_sync_mode ("auto"|"manual"; "" = cleared override).
	DefaultModelSyncMode string

	// Scheduled model-probe configuration. ProbeChannels and ProbeModels are
	// stored as JSON arrays; an empty slice (which is also what a malformed
	// value degrades to) means "everything".
	ProbeCron        string
	ProbePrompt      string
	ProbeMaxTokens   int
	ProbeConcurrency int
	// ProbeAutoDisable is the consecutive-failure threshold; 0 = report only.
	ProbeAutoDisable int
	ProbeChannels    []int64
	ProbeModels      []string

	UpdatedAt time.Time
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
		       default_model_sync_mode,
		       probe_cron, probe_prompt, probe_max_tokens, probe_concurrency,
		       probe_auto_disable, probe_channels, probe_models,
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
		defaultSyncMode                                                                    sql.NullString
		probeCron, probePrompt                                                             sql.NullString
		probeMaxTokens, probeConcurrency, probeAutoDisable                                 sql.NullInt64
		probeChannels, probeModels                                                         sql.NullString
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
		&channelRetry, &keyPoolRotation,
		&defaultSyncMode,
		&probeCron, &probePrompt, &probeMaxTokens, &probeConcurrency, &probeAutoDisable,
		&probeChannels, &probeModels,
		&updated,
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
	if defaultSyncMode.Valid {
		out.DefaultModelSyncMode = strings.TrimSpace(defaultSyncMode.String)
	}
	// The probe columns are NOT NULL with defaults, so they always read back.
	// The JSON lists are the only ones that can fail to parse, and a bad list
	// degrades to "everything" rather than an error: a malformed scope should
	// not stop the gateway from booting.
	if probeCron.Valid {
		out.ProbeCron = strings.TrimSpace(probeCron.String)
	}
	if probePrompt.Valid {
		out.ProbePrompt = probePrompt.String
	}
	if probeMaxTokens.Valid {
		out.ProbeMaxTokens = int(probeMaxTokens.Int64)
	}
	if probeConcurrency.Valid {
		out.ProbeConcurrency = int(probeConcurrency.Int64)
	}
	if probeAutoDisable.Valid {
		out.ProbeAutoDisable = int(probeAutoDisable.Int64)
	}
	if probeChannels.Valid {
		out.ProbeChannels = decodeIntList(probeChannels.String)
	}
	if probeModels.Valid {
		out.ProbeModels = decodeStringList(probeModels.String)
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
			default_model_sync_mode,
			probe_cron, probe_prompt, probe_max_tokens, probe_concurrency,
			probe_auto_disable, probe_channels, probe_models,
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
			?,
			?, ?, ?, ?,
			?, ?, ?,
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
			default_model_sync_mode = excluded.default_model_sync_mode,
			probe_cron = excluded.probe_cron,
			probe_prompt = excluded.probe_prompt,
			probe_max_tokens = excluded.probe_max_tokens,
			probe_concurrency = excluded.probe_concurrency,
			probe_auto_disable = excluded.probe_auto_disable,
			probe_channels = excluded.probe_channels,
			probe_models = excluded.probe_models,
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
		settings.DefaultModelSyncMode,
		settings.ProbeCron,
		settings.ProbePrompt,
		settings.ProbeMaxTokens,
		settings.ProbeConcurrency,
		settings.ProbeAutoDisable,
		encodeIntList(settings.ProbeChannels),
		encodeStringList(settings.ProbeModels),
	)
	if err != nil {
		return fmt.Errorf("runtime settings save: %w", err)
	}
	return nil
}

// A scheduled scope is stored as a JSON array. Both encoders write "[]" for an
// empty selection and both decoders return nil on any error, because the worst
// outcome of a malformed scope is a wider probe run — never a startup failure.
func encodeIntList(values []int64) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func encodeStringList(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func decodeIntList(raw string) []int64 {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values []int64
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func decodeStringList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}
