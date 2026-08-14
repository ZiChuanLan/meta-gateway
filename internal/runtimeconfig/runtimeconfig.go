// Package runtimeconfig merges env bootstrap with Admin DB overrides and applies hot reloads.
package runtimeconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/healthsweep"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/ratelimit"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webhook"
	"github.com/robfig/cron/v3"
)

// Editable is the Admin-writable subset of gateway runtime parameters.
type Editable struct {
	RetryTimes                  int    `json:"retry_times"`
	CrossChannelFailoverEnabled bool   `json:"cross_channel_failover_enabled"`
	CooldownSeconds             int    `json:"cooldown_seconds"`
	CheckinEnabled bool   `json:"checkin_enabled"`
	CheckinCron    string `json:"checkin_cron"`
	// DiscoveryCron is the scheduled model-list refresh expression (five-field
	// cron; empty = disabled). Same format as checkin_cron.
	DiscoveryCron string `json:"discovery_cron"`
	// DBGCCron is the scheduled database maintenance expression (orphan GC +
	// VACUUM; empty = disabled).
	DBGCCron string `json:"db_gc_cron"`
	RelayRatePerMinute          int    `json:"relay_rate_per_minute"`
	RelayRateBurst              int    `json:"relay_rate_burst"`
	AdminRatePerMinute          int    `json:"admin_rate_per_minute"`
	AdminRateBurst              int    `json:"admin_rate_burst"`
	AuditRetentionDays          int    `json:"audit_retention_days"`
	AuditRetentionRows          int    `json:"audit_retention_rows"`
	// ChannelAutoDisableThreshold: consecutive failures before auto-disable
	// (0 = feature off). RoutingLatencyAware enables latency-weighted picking.
	ChannelAutoDisableThreshold int  `json:"channel_auto_disable_threshold"`
	RoutingLatencyAware         bool `json:"routing_latency_aware"`
	// RoutingErrorAware penalizes channels with a high EWMA failure propensity.
	RoutingErrorAware bool `json:"routing_error_aware"`
	// RoutingConcurrencyEnabled enables the in-flight burst guard.
	RoutingConcurrencyEnabled bool `json:"routing_concurrency_enabled"`
	// RoutingConcurrencyLimit is the per-channel in-flight ceiling.
	RoutingConcurrencyLimit int `json:"routing_concurrency_limit"`
	// WebhookURL is the operational notification endpoint ("" disables).
	WebhookURL string `json:"webhook_url"`
	// ProxyURL is the global outbound HTTP(S) proxy; "" = direct. Channels
	// with their own proxy_url override it.
	ProxyURL string `json:"proxy_url"`
	// WebhookThrottleSeconds coalesces repeated events within the window.
	WebhookThrottleSeconds int `json:"webhook_throttle_seconds"`
	// StableFirstEnabled gates the 1/N grayscale pool.
	StableFirstEnabled bool `json:"stable_first_enabled"`
	// StableFirstDenominator is the draw base (25 = grayscale gets 1/25).
	StableFirstDenominator int `json:"stable_first_denominator"`
	// StableFirstPromoteRequests is the successful-attempt threshold for
	// automatic promotion out of the grayscale pool.
	StableFirstPromoteRequests int `json:"stable_first_promote_requests"`
	// RecoveryProbeEnabled enables the passive-recovery loop that probes
	// auto-disabled channels and restores them when the upstream answers.
	RecoveryProbeEnabled bool `json:"recovery_probe_enabled"`
	// RecoveryProbeIntervalSeconds is how often the recovery loop runs.
	RecoveryProbeIntervalSeconds int `json:"recovery_probe_interval_seconds"`
	// ProgressiveCooldownEnabled enables tiered cooldown with per-success decay
	// (fail 2→level2, fail 3→level3, fail 4→level4; success steps down one tier).
	ProgressiveCooldownEnabled bool `json:"progressive_cooldown_enabled"`
	CooldownLevel2Seconds      int  `json:"cooldown_level2_seconds"`
	CooldownLevel3Seconds      int  `json:"cooldown_level3_seconds"`
	CooldownLevel4Seconds      int  `json:"cooldown_level4_seconds"`
	// BreakerFailCount is the consecutive-failure threshold that parks a member
	// (route_members.enabled=0). 0 disables member parking (cooldown only).
	BreakerFailCount int `json:"breaker_fail_count"`
	// ModelBreakerFailCount is the in-memory channel×model circuit breaker
	// threshold. 0 disables the memory breaker.
	ModelBreakerFailCount int `json:"model_breaker_fail_count"`
	// KeyFailThreshold is the per channel×key×status consecutive-failure count
	// before an upstream API key is excluded from the pool. 0 disables.
	KeyFailThreshold int `json:"key_fail_threshold"`
	// StickyEnabled enables sticky-session routing (same conversation prefers
	// the previously successful channel). StickyTTLMinutes is the binding TTL.
	StickyEnabled     bool `json:"sticky_enabled"`
	StickyTTLMinutes  int  `json:"sticky_ttl_minutes"`
	// AlertConfigJSON is the multi-channel alert matrix (webhook/bark/
	// serverchan/telegram/smtp + cooldown + daily summary flag). Empty string
	// keeps the env bootstrap ("" itself disables all alert channels).
	AlertConfigJSON string `json:"alert_config_json"`
	// AlertSweepIntervalSeconds is the proactive health sweep cadence (0 = off).
	AlertSweepIntervalSeconds int `json:"alert_sweep_interval_seconds"`
	// AlertDailySummaryIntervalSeconds is the daily digest cadence (0 = off).
	AlertDailySummaryIntervalSeconds int `json:"alert_daily_summary_interval_seconds"`
	// HealthSweepEnabled turns on the periodic channel health sweep that grades
	// every enabled channel operational/degraded/error and alerts on transitions.
	HealthSweepEnabled         bool `json:"health_sweep_enabled"`
	HealthSweepIntervalSeconds int  `json:"health_sweep_interval_seconds"`
	HealthSweepJitterSeconds   int  `json:"health_sweep_jitter_seconds"`
	HealthSweepDegradedMs      int  `json:"health_sweep_degraded_ms"`
	HealthSweepConcurrency     int  `json:"health_sweep_concurrency"`
	HealthSweepTimeoutSeconds  int  `json:"health_sweep_timeout_seconds"`
	// ChannelRetryTimes is how many times the same upstream key is re-sent
	// after a retryable failure before moving to the next key/channel (0-5).
	ChannelRetryTimes int `json:"channel_retry_times"`
	// KeyPoolRotation enables rotating through the site key pool on failure.
	KeyPoolRotation bool `json:"key_pool_rotation"`
}

// Snapshot is the effective runtime view returned to Admin UI.
type Snapshot struct {
	Source       string     `json:"source"` // environment | admin_override
	HasOverride  bool       `json:"has_override"`
	Editable     Editable   `json:"editable"`
	EnvBootstrap Editable   `json:"env_bootstrap"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	Note         string     `json:"note"`
	// Read-only process facts (not Admin-writable).
	ServerHTTPAddr string `json:"server_http_addr"`
	DataDir        string `json:"data_dir"`
	BackupDir      string `json:"backup_dir"`
	PluginsDir     string `json:"plugins_dir"`
	// MetricsTokenMasked shows whether a metrics token is configured ("" = none).
	MetricsTokenMasked string `json:"metrics_token_masked"`
}

// Appliers are optional hot-reload targets. Nil entries are skipped.
type Appliers struct {
	Proxy *proxy.Service
	// Selector receives latency-awareness updates (hot reload target).
	Selector     *routing.Selector
	RelayLimiter *ratelimit.Limiter
	AdminLimiter *ratelimit.Limiter
	CheckinSched *checkin.Scheduler
	// CheckinAllowed reports whether the check-in module may run (plugin gate).
	// When nil, check-in enablement follows the editable flag alone.
	CheckinAllowed func() bool
	SetAudit       func(days, rows int)
	SetAuditLoop   func(days, rows int)
	// SetProgressiveCooldown hot-applies the tiered cooldown policy (nil store
	// or applier disables the feature entirely).
	SetProgressiveCooldown func(enabled bool, base time.Duration, levels [3]time.Duration, breakerCount int)
	// SetSticky hot-applies sticky-session routing: a non-nil store enables it
	// (with the given TTL), nil disables it. The applier must rewire selector,
	// proxy, and admin handler so the switch is live without a restart.
	SetSticky func(store *routing.StickyStore, ttl time.Duration)
	// SetGlobalProxy hot-applies the global outbound proxy ("" = direct). The
	// callback validates the URL against the outbound SSRF policy.
	SetGlobalProxy func(raw string) error
	// SetDiscoveryCron hot-applies the scheduled model-refresh expression
	// ("" = disabled).
	SetDiscoveryCron func(expression string) error
	// SetDBGCCron hot-applies the database-maintenance expression
	// ("" = disabled).
	SetDBGCCron func(expression string) error
	// SetRecoveryProbe hot-applies the passive-recovery probe configuration.
	SetRecoveryProbe func(enabled bool, interval time.Duration)
	// SetStableFirst hot-applies the grayscale pool (selector + promotion).
	SetStableFirst func(enabled bool, denominator, promoteRequests int)
	// SetConcurrencyAware hot-applies the in-flight burst guard.
	SetConcurrencyAware func(enabled bool, limit int)
	// SetWebhook hot-applies the operational webhook endpoint + throttle.
	SetWebhook func(url string, throttle time.Duration)
	// SetAlert hot-applies the alert matrix + sweep/digest cadences. cfg is the
	// parsed AlertConfigJSON (zero value = disable all alert channels).
	SetAlert func(cfg webhook.AlertConfig, sweepInterval, dailySummaryInterval time.Duration)
	// SetHealthSweep hot-applies the periodic channel health sweep (enabled +
	// interval/jitter/degraded threshold/concurrency/timeout).
	SetHealthSweep func(cfg healthsweep.Config)
	// SetChannelRetryTimes hot-applies the same-key re-send count.
	SetChannelRetryTimes func(times int)
	// SetKeyPoolRotation hot-applies whether the site key pool is rotated.
	SetKeyPoolRotation func(enabled bool)
}

// Controller loads, validates, persists, and applies runtime settings.
type Controller struct {
	mu       sync.RWMutex
	env      Editable
	cfg      *config.Config
	store    *store.RuntimeSettingsStore
	appliers Appliers
	current  Editable
	source   string
	updated  *time.Time
}

func New(cfg *config.Config, settingsStore *store.RuntimeSettingsStore, appliers Appliers) *Controller {
	env := Editable{
		RetryTimes:                       cfg.RetryTimes,
		CrossChannelFailoverEnabled:      cfg.CrossChannelFailoverEnabled,
		CooldownSeconds:                  int(cfg.Cooldown / time.Second),
		CheckinEnabled:                   cfg.CheckinEnabled,
		CheckinCron:                      cfg.CheckinCron,
		RelayRatePerMinute:               cfg.RelayRatePerMinute,
		RelayRateBurst:                   cfg.RelayRateBurst,
		AdminRatePerMinute:               cfg.AdminRatePerMinute,
		AdminRateBurst:                   cfg.AdminRateBurst,
		AuditRetentionDays:               cfg.AuditRetentionDays,
		AuditRetentionRows:               cfg.AuditRetentionRows,
		ChannelAutoDisableThreshold:      cfg.ChannelAutoDisableThreshold,
		RoutingLatencyAware:              cfg.RoutingLatencyAware,
		RoutingErrorAware:                cfg.RoutingErrorAware,
		RoutingConcurrencyEnabled:        cfg.RoutingConcurrencyEnabled,
		RoutingConcurrencyLimit:          cfg.RoutingConcurrencyLimit,
		WebhookURL:                       cfg.WebhookURL,
		WebhookThrottleSeconds:           cfg.WebhookThrottleSeconds,
		StableFirstEnabled:               cfg.StableFirstEnabled,
		StableFirstDenominator:           cfg.StableFirstDenominator,
		StableFirstPromoteRequests:       cfg.StableFirstPromoteRequests,
		RecoveryProbeEnabled:             cfg.RecoveryProbeEnabled,
		RecoveryProbeIntervalSeconds:     cfg.RecoveryProbeIntervalSeconds,
		ProgressiveCooldownEnabled:       cfg.ProgressiveCooldownEnabled,
		CooldownLevel2Seconds:            cfg.CooldownLevel2Seconds,
		CooldownLevel3Seconds:            cfg.CooldownLevel3Seconds,
		CooldownLevel4Seconds:            cfg.CooldownLevel4Seconds,
		BreakerFailCount:                 cfg.BreakerFailCount,
		ModelBreakerFailCount:            cfg.ModelBreakerFailCount,
		KeyFailThreshold:                 cfg.KeyFailThreshold,
		StickyEnabled:                    cfg.StickyEnabled,
		StickyTTLMinutes:                 int(cfg.StickyTTL / time.Minute),
		AlertConfigJSON:                  cfg.AlertConfigJSON,
		AlertSweepIntervalSeconds:        int(cfg.AlertSweepInterval / time.Second),
		AlertDailySummaryIntervalSeconds: int(cfg.AlertDailySummaryInterval / time.Second),
		HealthSweepEnabled:               cfg.HealthSweepEnabled,
		HealthSweepIntervalSeconds:       cfg.HealthSweepIntervalSeconds,
		HealthSweepJitterSeconds:         cfg.HealthSweepJitterSeconds,
		HealthSweepDegradedMs:            cfg.HealthSweepDegradedMs,
		HealthSweepConcurrency:           cfg.HealthSweepConcurrency,
		HealthSweepTimeoutSeconds:        cfg.HealthSweepTimeoutSeconds,
		ChannelRetryTimes:                cfg.ChannelRetryTimes,
		KeyPoolRotation:                  cfg.KeyPoolRotation,
	}
	c := &Controller{
		env:      env,
		cfg:      cfg,
		store:    settingsStore,
		appliers: appliers,
		current:  env,
		source:   "environment",
	}
	return c
}

// Bootstrap loads DB overrides (if any) and applies them once at process start.
func (c *Controller) Bootstrap() error {
	if c == nil {
		return nil
	}
	row, err := c.store.Get()
	if err != nil {
		return err
	}
	if row == nil || !row.HasOverride {
		c.applyLocked(c.env)
		return nil
	}
	editable := c.rowToEditableWithEnv(row)
	if err := Validate(editable); err != nil {
		// Fall back to env if stored row is corrupt.
		c.applyLocked(c.env)
		return fmt.Errorf("runtime settings override invalid, using env: %w", err)
	}
	c.mu.Lock()
	c.current = editable
	c.source = "admin_override"
	if !row.UpdatedAt.IsZero() {
		updated := row.UpdatedAt
		c.updated = &updated
	}
	c.mu.Unlock()
	c.applyLocked(editable)
	return nil
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	note := "Editable values apply immediately (hot reload). Server listen address and data directory still require restart."
	if c.source == "environment" {
		note = "Using environment bootstrap. Save from Admin to override and hot-reload. Secrets are never editable here."
	}
	return Snapshot{
		Source:         c.source,
		HasOverride:    c.source == "admin_override",
		Editable:       c.current,
		EnvBootstrap:   c.env,
		UpdatedAt:      c.updated,
		Note:           note,
		ServerHTTPAddr: c.cfg.HTTPAddr,
		DataDir:        c.cfg.DataDir,
		BackupDir:      c.cfg.BackupDir,
		PluginsDir:     c.cfg.PluginsDir,
		MetricsTokenMasked: maskToken(c.cfg.MetricsToken),
	}
}

// maskToken redacts a secret for read-only display ("" stays "").
func maskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 6 {
		return "••••"
	}
	return token[:4] + "••••" + token[len(token)-2:]
}

// Update validates, persists, and hot-applies Admin overrides.
func (c *Controller) Update(next Editable) (Snapshot, error) {
	if err := Validate(next); err != nil {
		return Snapshot{}, err
	}
	row := &store.RuntimeSettingsRow{
		HasOverride:                      true,
		RetryTimes:                       next.RetryTimes,
		CrossChannelFailoverEnabled:      boolInt(next.CrossChannelFailoverEnabled),
		CooldownSeconds:                  next.CooldownSeconds,
		CheckinEnabled:                   next.CheckinEnabled,
		CheckinCron:                      next.CheckinCron,
		RelayRatePerMinute:               next.RelayRatePerMinute,
		RelayRateBurst:                   next.RelayRateBurst,
		AdminRatePerMinute:               next.AdminRatePerMinute,
		AdminRateBurst:                   next.AdminRateBurst,
		AuditRetentionDays:               next.AuditRetentionDays,
		AuditRetentionRows:               next.AuditRetentionRows,
		ChannelAutoDisableThreshold:      next.ChannelAutoDisableThreshold,
		RoutingLatencyAware:              boolInt(next.RoutingLatencyAware),
		RoutingErrorAware:                boolInt(next.RoutingErrorAware),
		RoutingConcurrencyEnabled:        boolInt(next.RoutingConcurrencyEnabled),
		RoutingConcurrencyLimit:          next.RoutingConcurrencyLimit,
		WebhookURL:                       next.WebhookURL,
		ProxyURL:                         next.ProxyURL,
		DiscoveryCron:                    next.DiscoveryCron,
		DBGCCron:                         next.DBGCCron,
		WebhookThrottleSeconds:           next.WebhookThrottleSeconds,
		StableFirstEnabled:               boolInt(next.StableFirstEnabled),
		StableFirstDenominator:           next.StableFirstDenominator,
		StableFirstPromoteRequests:       next.StableFirstPromoteRequests,
		RecoveryProbeEnabled:             boolInt(next.RecoveryProbeEnabled),
		RecoveryProbeIntervalSeconds:     next.RecoveryProbeIntervalSeconds,
		ProgressiveCooldownEnabled:       boolInt(next.ProgressiveCooldownEnabled),
		CooldownLevel2Seconds:            next.CooldownLevel2Seconds,
		CooldownLevel3Seconds:            next.CooldownLevel3Seconds,
		CooldownLevel4Seconds:            next.CooldownLevel4Seconds,
		BreakerFailCount:                 next.BreakerFailCount,
		ModelBreakerFailCount:            next.ModelBreakerFailCount,
		KeyFailThreshold:                 next.KeyFailThreshold,
		StickyEnabled:                    boolInt(next.StickyEnabled),
		StickyTTLMinutes:                 next.StickyTTLMinutes,
		AlertConfigJSON:                  next.AlertConfigJSON,
		AlertSweepIntervalSeconds:        next.AlertSweepIntervalSeconds,
		AlertDailySummaryIntervalSeconds: next.AlertDailySummaryIntervalSeconds,
		HealthSweepEnabled:               boolInt(next.HealthSweepEnabled),
		HealthSweepIntervalSeconds:       next.HealthSweepIntervalSeconds,
		HealthSweepJitterSeconds:         next.HealthSweepJitterSeconds,
		HealthSweepDegradedMs:            next.HealthSweepDegradedMs,
		HealthSweepConcurrency:           next.HealthSweepConcurrency,
		HealthSweepTimeoutSeconds:        next.HealthSweepTimeoutSeconds,
		ChannelRetryTimes:                next.ChannelRetryTimes,
		KeyPoolRotation:                  boolInt(next.KeyPoolRotation),
	}
	// Apply first so invalid scheduler cron fails before DB write? Prefer validate cron in Validate.
	if err := c.applyWithError(next); err != nil {
		return Snapshot{}, err
	}
	if err := c.store.Save(row); err != nil {
		// Best-effort: settings already applied in memory; surface persistence error.
		return Snapshot{}, err
	}
	now := time.Now().UTC()
	c.mu.Lock()
	c.current = next
	c.source = "admin_override"
	c.updated = &now
	c.mu.Unlock()
	return c.Snapshot(), nil
}

// ClearOverride removes Admin override and re-applies env bootstrap.
func (c *Controller) ClearOverride() (Snapshot, error) {
	row := &store.RuntimeSettingsRow{HasOverride: false}
	if err := c.store.Save(row); err != nil {
		return Snapshot{}, err
	}
	if err := c.applyWithError(c.env); err != nil {
		return Snapshot{}, err
	}
	c.mu.Lock()
	c.current = c.env
	c.source = "environment"
	c.updated = nil
	c.mu.Unlock()
	return c.Snapshot(), nil
}

func (c *Controller) applyLocked(values Editable) {
	_ = c.applyWithError(values)
}

// ResyncCheckin re-applies the current check-in schedule using the latest editable
// settings and CheckinAllowed gate. Call when the checkin add-on is toggled.
func (c *Controller) ResyncCheckin() error {
	c.mu.RLock()
	values := c.current
	c.mu.RUnlock()
	if c.appliers.CheckinSched == nil {
		return nil
	}
	enabled := values.CheckinEnabled
	if c.appliers.CheckinAllowed != nil && !c.appliers.CheckinAllowed() {
		enabled = false
	}
	return c.appliers.CheckinSched.SetSchedule(values.CheckinCron, enabled)
}

func (c *Controller) applyWithError(values Editable) error {
	if c.appliers.Proxy != nil {
		c.appliers.Proxy.SetRetryPolicy(values.RetryTimes, time.Duration(values.CooldownSeconds)*time.Second)
		c.appliers.Proxy.SetCrossChannelFailoverEnabled(values.CrossChannelFailoverEnabled)
	}
	if c.appliers.SetGlobalProxy != nil {
		if err := c.appliers.SetGlobalProxy(values.ProxyURL); err != nil {
			return fmt.Errorf("proxy_url is invalid: %w", err)
		}
	}
	if c.appliers.SetDiscoveryCron != nil {
		if err := c.appliers.SetDiscoveryCron(values.DiscoveryCron); err != nil {
			return fmt.Errorf("discovery_cron is invalid: %w", err)
		}
	}
	if c.appliers.SetDBGCCron != nil {
		if err := c.appliers.SetDBGCCron(values.DBGCCron); err != nil {
			return fmt.Errorf("db_gc_cron is invalid: %w", err)
		}
	}
	if c.appliers.RelayLimiter != nil {
		c.appliers.RelayLimiter.SetLimits(values.RelayRatePerMinute, values.RelayRateBurst)
	}
	if c.appliers.AdminLimiter != nil {
		c.appliers.AdminLimiter.SetLimits(values.AdminRatePerMinute, values.AdminRateBurst)
	}
	if c.appliers.SetAudit != nil {
		c.appliers.SetAudit(values.AuditRetentionDays, values.AuditRetentionRows)
	}
	if c.appliers.SetAuditLoop != nil {
		c.appliers.SetAuditLoop(values.AuditRetentionDays, values.AuditRetentionRows)
	}
	if c.appliers.CheckinSched != nil {
		enabled := values.CheckinEnabled
		if c.appliers.CheckinAllowed != nil && !c.appliers.CheckinAllowed() {
			enabled = false
		}
		if err := c.appliers.CheckinSched.SetSchedule(values.CheckinCron, enabled); err != nil {
			return err
		}
	}
	// Channel auto-disable threshold + latency-aware routing hot reload.
	if c.appliers.Proxy != nil {
		c.appliers.Proxy.SetAutoDisableThreshold(values.ChannelAutoDisableThreshold)
		c.appliers.Proxy.SetLatencyAware(values.RoutingLatencyAware)
		// In-memory channel×model circuit breaker (MODEL_BREAKER_FAIL_COUNT).
		c.appliers.Proxy.SetBreakerFailCount(values.ModelBreakerFailCount)
		// Per-key auto-exclusion threshold (KEY_FAIL_THRESHOLD; 0 = off).
		c.appliers.Proxy.SetKeyFailThreshold(values.KeyFailThreshold)
	}
	if c.appliers.Selector != nil && c.appliers.Proxy != nil {
		c.appliers.Selector.SetLatencyAware(values.RoutingLatencyAware, c.appliers.Proxy.ChannelLatency)
		c.appliers.Selector.SetErrorAware(values.RoutingErrorAware, c.appliers.Proxy.ChannelErrorRate)
	}
	if c.appliers.SetProgressiveCooldown != nil {
		c.appliers.SetProgressiveCooldown(
			values.ProgressiveCooldownEnabled,
			time.Duration(values.CooldownSeconds)*time.Second,
			[3]time.Duration{
				time.Duration(values.CooldownLevel2Seconds) * time.Second,
				time.Duration(values.CooldownLevel3Seconds) * time.Second,
				time.Duration(values.CooldownLevel4Seconds) * time.Second,
			},
			values.BreakerFailCount,
		)
	}
	// Sticky-session routing hot swap: enabled → build a store with the TTL and
	// rewire selector/proxy/admin; disabled → nil store (off).
	if c.appliers.SetSticky != nil {
		var stickyStore *routing.StickyStore
		if values.StickyEnabled {
			stickyStore = routing.NewStickyStore(time.Duration(values.StickyTTLMinutes)*time.Minute, nil)
		}
		c.appliers.SetSticky(stickyStore, time.Duration(values.StickyTTLMinutes)*time.Minute)
	}
	// Passive-recovery probe configuration hot reload.
	if c.appliers.SetRecoveryProbe != nil {
		c.appliers.SetRecoveryProbe(values.RecoveryProbeEnabled, time.Duration(values.RecoveryProbeIntervalSeconds)*time.Second)
	}
	// Stable-first grayscale pool hot reload.
	if c.appliers.SetStableFirst != nil {
		c.appliers.SetStableFirst(values.StableFirstEnabled, values.StableFirstDenominator, values.StableFirstPromoteRequests)
	}
	if c.appliers.SetConcurrencyAware != nil {
		c.appliers.SetConcurrencyAware(values.RoutingConcurrencyEnabled, values.RoutingConcurrencyLimit)
	}
	if c.appliers.SetWebhook != nil {
		c.appliers.SetWebhook(values.WebhookURL, time.Duration(values.WebhookThrottleSeconds)*time.Second)
	}
	// Alert matrix + sweep/digest cadence hot reload. A JSON parse failure is
	// treated as "disable alert channels" (validation already rejects it for
	// Admin saves; this only guards corrupt bootstrap rows).
	if c.appliers.SetAlert != nil {
		var alertCfg webhook.AlertConfig
		if strings.TrimSpace(values.AlertConfigJSON) != "" {
			_ = json.Unmarshal([]byte(values.AlertConfigJSON), &alertCfg)
		}
		c.appliers.SetAlert(
			alertCfg,
			time.Duration(values.AlertSweepIntervalSeconds)*time.Second,
			time.Duration(values.AlertDailySummaryIntervalSeconds)*time.Second,
		)
	}
	// Periodic channel health sweep hot reload.
	if c.appliers.SetHealthSweep != nil {
		c.appliers.SetHealthSweep(healthsweep.Config{
			Enabled:             values.HealthSweepEnabled,
			IntervalSeconds:     values.HealthSweepIntervalSeconds,
			JitterSeconds:       values.HealthSweepJitterSeconds,
			DegradedThresholdMs: values.HealthSweepDegradedMs,
			Concurrency:         values.HealthSweepConcurrency,
			TimeoutSeconds:      values.HealthSweepTimeoutSeconds,
		})
	}
	// Same-key re-send count hot reload (0 = no re-send).
	if c.appliers.SetChannelRetryTimes != nil {
		c.appliers.SetChannelRetryTimes(values.ChannelRetryTimes)
	}
	// Key-pool rotation hot reload.
	if c.appliers.SetKeyPoolRotation != nil {
		c.appliers.SetKeyPoolRotation(values.KeyPoolRotation)
	}
	return nil
}

// boolInt converts a bool to the runtime-settings integer encoding.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func rowToEditable(row *store.RuntimeSettingsRow) Editable {
	return Editable{
		RetryTimes:                       row.RetryTimes,
		CrossChannelFailoverEnabled:      row.CrossChannelFailoverEnabled == 1,
		CooldownSeconds:                  row.CooldownSeconds,
		CheckinEnabled:                   row.CheckinEnabled,
		CheckinCron:                      row.CheckinCron,
		RelayRatePerMinute:               row.RelayRatePerMinute,
		RelayRateBurst:                   row.RelayRateBurst,
		AdminRatePerMinute:               row.AdminRatePerMinute,
		AdminRateBurst:                   row.AdminRateBurst,
		AuditRetentionDays:               row.AuditRetentionDays,
		AuditRetentionRows:               row.AuditRetentionRows,
		ChannelAutoDisableThreshold:      row.ChannelAutoDisableThreshold,
		RoutingLatencyAware:              row.RoutingLatencyAware == 1,
		RoutingErrorAware:                row.RoutingErrorAware == 1,
		RoutingConcurrencyEnabled:        row.RoutingConcurrencyEnabled == 1,
		RoutingConcurrencyLimit:          row.RoutingConcurrencyLimit,
		WebhookURL:                       row.WebhookURL,
		ProxyURL:                         row.ProxyURL,
		DiscoveryCron:                    row.DiscoveryCron,
		DBGCCron:                         row.DBGCCron,
		WebhookThrottleSeconds:           row.WebhookThrottleSeconds,
		StableFirstEnabled:               row.StableFirstEnabled == 1,
		StableFirstDenominator:           row.StableFirstDenominator,
		StableFirstPromoteRequests:       row.StableFirstPromoteRequests,
		ProgressiveCooldownEnabled:       row.ProgressiveCooldownEnabled == 1,
		CooldownLevel2Seconds:            row.CooldownLevel2Seconds,
		CooldownLevel3Seconds:            row.CooldownLevel3Seconds,
		CooldownLevel4Seconds:            row.CooldownLevel4Seconds,
		BreakerFailCount:                 row.BreakerFailCount,
		ModelBreakerFailCount:            row.ModelBreakerFailCount,
		KeyFailThreshold:                 row.KeyFailThreshold,
		StickyEnabled:                    row.StickyEnabled == 1,
		StickyTTLMinutes:                 row.StickyTTLMinutes,
		AlertConfigJSON:                  row.AlertConfigJSON,
		AlertSweepIntervalSeconds:        row.AlertSweepIntervalSeconds,
		AlertDailySummaryIntervalSeconds: row.AlertDailySummaryIntervalSeconds,
		HealthSweepEnabled:               row.HealthSweepEnabled == 1,
		HealthSweepIntervalSeconds:       row.HealthSweepIntervalSeconds,
		HealthSweepJitterSeconds:         row.HealthSweepJitterSeconds,
		HealthSweepDegradedMs:            row.HealthSweepDegradedMs,
		HealthSweepConcurrency:           row.HealthSweepConcurrency,
		HealthSweepTimeoutSeconds:        row.HealthSweepTimeoutSeconds,
		ChannelRetryTimes:                row.ChannelRetryTimes,
		KeyPoolRotation:                  row.KeyPoolRotation == 1,
	}
}

// rowToEditableWithEnv resolves NULL (unset) override fields against the env
// bootstrap so an older override row cannot accidentally zero new settings.
func (c *Controller) rowToEditableWithEnv(row *store.RuntimeSettingsRow) Editable {
	editable := rowToEditable(row)
	if editable.CrossChannelFailoverEnabled == false && row.CrossChannelFailoverEnabled == -1 {
		editable.CrossChannelFailoverEnabled = c.env.CrossChannelFailoverEnabled
	}
	if editable.ChannelAutoDisableThreshold < 0 {
		editable.ChannelAutoDisableThreshold = c.env.ChannelAutoDisableThreshold
	}
	if editable.RoutingLatencyAware == false && row.RoutingLatencyAware == -1 {
		editable.RoutingLatencyAware = c.env.RoutingLatencyAware
	}
	if editable.RoutingErrorAware == false && row.RoutingErrorAware == -1 {
		editable.RoutingErrorAware = c.env.RoutingErrorAware
	}
	if editable.RoutingConcurrencyEnabled == false && row.RoutingConcurrencyEnabled == -1 {
		editable.RoutingConcurrencyEnabled = c.env.RoutingConcurrencyEnabled
	}
	if editable.RoutingConcurrencyLimit < 0 {
		editable.RoutingConcurrencyLimit = c.env.RoutingConcurrencyLimit
	}
	if editable.WebhookURL == "" && row.WebhookURL == "" {
		editable.WebhookURL = c.env.WebhookURL
	}
	if editable.WebhookThrottleSeconds < 0 {
		editable.WebhookThrottleSeconds = c.env.WebhookThrottleSeconds
	}
	if editable.StableFirstEnabled == false && row.StableFirstEnabled == -1 {
		editable.StableFirstEnabled = c.env.StableFirstEnabled
	}
	if editable.StableFirstDenominator < 0 {
		editable.StableFirstDenominator = c.env.StableFirstDenominator
	}
	if editable.StableFirstPromoteRequests < 0 {
		editable.StableFirstPromoteRequests = c.env.StableFirstPromoteRequests
	}
	if editable.RecoveryProbeEnabled == false && row.RecoveryProbeEnabled == -1 {
		editable.RecoveryProbeEnabled = c.env.RecoveryProbeEnabled
	}
	if editable.RecoveryProbeIntervalSeconds < 0 {
		editable.RecoveryProbeIntervalSeconds = c.env.RecoveryProbeIntervalSeconds
	}
	if editable.ProgressiveCooldownEnabled == false && row.ProgressiveCooldownEnabled == -1 {
		editable.ProgressiveCooldownEnabled = c.env.ProgressiveCooldownEnabled
	}
	if editable.CooldownLevel2Seconds < 0 {
		editable.CooldownLevel2Seconds = c.env.CooldownLevel2Seconds
	}
	if editable.CooldownLevel3Seconds < 0 {
		editable.CooldownLevel3Seconds = c.env.CooldownLevel3Seconds
	}
	if editable.CooldownLevel4Seconds < 0 {
		editable.CooldownLevel4Seconds = c.env.CooldownLevel4Seconds
	}
	if editable.BreakerFailCount < 0 {
		editable.BreakerFailCount = c.env.BreakerFailCount
	}
	if editable.ModelBreakerFailCount < 0 {
		editable.ModelBreakerFailCount = c.env.ModelBreakerFailCount
	}
	if editable.KeyFailThreshold < 0 {
		editable.KeyFailThreshold = c.env.KeyFailThreshold
	}
	if editable.StickyEnabled == false && row.StickyEnabled == -1 {
		editable.StickyEnabled = c.env.StickyEnabled
	}
	if editable.StickyTTLMinutes < 0 {
		editable.StickyTTLMinutes = c.env.StickyTTLMinutes
	}
	if row.AlertConfigJSON == "" {
		editable.AlertConfigJSON = c.env.AlertConfigJSON
	}
	if editable.AlertSweepIntervalSeconds < 0 {
		editable.AlertSweepIntervalSeconds = c.env.AlertSweepIntervalSeconds
	}
	if editable.AlertDailySummaryIntervalSeconds < 0 {
		editable.AlertDailySummaryIntervalSeconds = c.env.AlertDailySummaryIntervalSeconds
	}
	if editable.HealthSweepEnabled == false && row.HealthSweepEnabled == -1 {
		editable.HealthSweepEnabled = c.env.HealthSweepEnabled
	}
	if editable.HealthSweepIntervalSeconds < 0 {
		editable.HealthSweepIntervalSeconds = c.env.HealthSweepIntervalSeconds
	}
	if editable.HealthSweepJitterSeconds < 0 {
		editable.HealthSweepJitterSeconds = c.env.HealthSweepJitterSeconds
	}
	if editable.HealthSweepDegradedMs < 0 {
		editable.HealthSweepDegradedMs = c.env.HealthSweepDegradedMs
	}
	if editable.HealthSweepConcurrency < 0 {
		editable.HealthSweepConcurrency = c.env.HealthSweepConcurrency
	}
	if editable.HealthSweepTimeoutSeconds < 0 {
		editable.HealthSweepTimeoutSeconds = c.env.HealthSweepTimeoutSeconds
	}
	if editable.ChannelRetryTimes < 0 {
		editable.ChannelRetryTimes = c.env.ChannelRetryTimes
	}
	if editable.KeyPoolRotation == false && row.KeyPoolRotation == -1 {
		editable.KeyPoolRotation = c.env.KeyPoolRotation
	}
	return editable
}

// Validate enforces the same bounds as env loading for Admin-writable fields.
func Validate(values Editable) error {
	if values.RetryTimes < 0 || values.RetryTimes > 100 {
		return fmt.Errorf("retry_times must be between 0 and 100")
	}
	if values.CooldownSeconds < 0 || values.CooldownSeconds > 86400 {
		return fmt.Errorf("cooldown_seconds must be between 0 and 86400")
	}
	if values.RelayRatePerMinute < 0 || values.RelayRatePerMinute > 1_000_000 {
		return fmt.Errorf("relay_rate_per_minute out of range")
	}
	if values.RelayRateBurst < 0 || values.RelayRateBurst > 1_000_000 {
		return fmt.Errorf("relay_rate_burst out of range")
	}
	if values.AdminRatePerMinute < 0 || values.AdminRatePerMinute > 1_000_000 {
		return fmt.Errorf("admin_rate_per_minute out of range")
	}
	if values.AdminRateBurst < 0 || values.AdminRateBurst > 1_000_000 {
		return fmt.Errorf("admin_rate_burst out of range")
	}
	if values.AuditRetentionDays < 0 || values.AuditRetentionDays > 36500 {
		return fmt.Errorf("audit_retention_days out of range")
	}
	if values.ChannelAutoDisableThreshold < 0 || values.ChannelAutoDisableThreshold > 1000 {
		return fmt.Errorf("channel_auto_disable_threshold out of range")
	}
	if values.CooldownLevel2Seconds < 0 || values.CooldownLevel2Seconds > 86400*7 {
		return fmt.Errorf("cooldown_level2_seconds out of range")
	}
	if values.CooldownLevel3Seconds < 0 || values.CooldownLevel3Seconds > 86400*7 {
		return fmt.Errorf("cooldown_level3_seconds out of range")
	}
	if values.CooldownLevel4Seconds < 0 || values.CooldownLevel4Seconds > 86400*30 {
		return fmt.Errorf("cooldown_level4_seconds out of range")
	}
	if values.BreakerFailCount != 0 && (values.BreakerFailCount < 2 || values.BreakerFailCount > 100) {
		return fmt.Errorf("breaker_fail_count must be between 2 and 100 (0 disables member parking)")
	}
	if values.ModelBreakerFailCount != 0 && (values.ModelBreakerFailCount < 2 || values.ModelBreakerFailCount > 100) {
		return fmt.Errorf("model_breaker_fail_count must be between 2 and 100 (0 disables)")
	}
	if values.KeyFailThreshold != 0 && (values.KeyFailThreshold < 2 || values.KeyFailThreshold > 100) {
		return fmt.Errorf("key_fail_threshold must be between 2 and 100 (0 disables)")
	}
	if values.StickyEnabled && (values.StickyTTLMinutes < 1 || values.StickyTTLMinutes > 1440) {
		return fmt.Errorf("sticky_ttl_minutes must be between 1 and 1440")
	}
	if values.RecoveryProbeIntervalSeconds < 0 || values.RecoveryProbeIntervalSeconds > 86400 {
		return fmt.Errorf("recovery_probe_interval_seconds must be between 0 and 86400")
	}
	// When progressive cooldown is on, the breaker threshold must leave the
	// tier table reachable: tieredBackoff maps fail 2/3/4 to levels[0/1/2], so a
	// threshold below 5 would disable the member before the 1h/24h tiers ever
	// apply (e.g. the old default of 3 killed the level-3/4 cooldowns).
	if values.ProgressiveCooldownEnabled && values.BreakerFailCount != 0 && values.BreakerFailCount < 5 {
		return fmt.Errorf("breaker_fail_count must be at least 5 when progressive cooldown is enabled")
	}
	// Alert matrix JSON must parse as webhook.AlertConfig ("" = disable all).
	if strings.TrimSpace(values.AlertConfigJSON) != "" {
		var alertCfg webhook.AlertConfig
		if err := json.Unmarshal([]byte(values.AlertConfigJSON), &alertCfg); err != nil {
			return fmt.Errorf("alert_config_json is not valid JSON: %w", err)
		}
	}
	if values.AlertSweepIntervalSeconds < 0 || values.AlertSweepIntervalSeconds > 24*60*60 {
		return fmt.Errorf("alert_sweep_interval_seconds must be between 0 and 86400")
	}
	if values.AlertDailySummaryIntervalSeconds < 0 || values.AlertDailySummaryIntervalSeconds > 24*60*60 {
		return fmt.Errorf("alert_daily_summary_interval_seconds must be between 0 and 86400")
	}
	// Channel health sweep bounds mirror the env loader. Only enforced while
	// the sweep is enabled: with the switch off the tuning values are dormant
	// (the healthsweep service also sanitizes them defensively).
	if values.HealthSweepEnabled {
		if values.HealthSweepIntervalSeconds < 10 || values.HealthSweepIntervalSeconds > 86400 {
			return fmt.Errorf("health_sweep_interval_seconds must be between 10 and 86400")
		}
		if values.HealthSweepJitterSeconds < 0 || values.HealthSweepJitterSeconds > 3600 {
			return fmt.Errorf("health_sweep_jitter_seconds must be between 0 and 3600")
		}
		if values.HealthSweepDegradedMs < 100 || values.HealthSweepDegradedMs > 60000 {
			return fmt.Errorf("health_sweep_degraded_ms must be between 100 and 60000")
		}
		if values.HealthSweepConcurrency < 1 || values.HealthSweepConcurrency > 64 {
			return fmt.Errorf("health_sweep_concurrency must be between 1 and 64")
		}
		if values.HealthSweepTimeoutSeconds < 1 || values.HealthSweepTimeoutSeconds > 120 {
			return fmt.Errorf("health_sweep_timeout_seconds must be between 1 and 120")
		}
	}
	if values.ChannelRetryTimes < 0 || values.ChannelRetryTimes > 5 {
		return fmt.Errorf("channel_retry_times must be between 0 and 5")
	}
	if values.StableFirstDenominator < 2 || values.StableFirstDenominator > 1000 {
		return fmt.Errorf("stable_first_denominator must be between 2 and 1000")
	}
	if values.StableFirstPromoteRequests < 1 || values.StableFirstPromoteRequests > 100000 {
		return fmt.Errorf("stable_first_promote_requests must be between 1 and 100000")
	}
	if values.RoutingConcurrencyLimit < 1 || values.RoutingConcurrencyLimit > 100000 {
		return fmt.Errorf("routing_concurrency_limit must be between 1 and 100000")
	}
	if values.WebhookThrottleSeconds < 1 || values.WebhookThrottleSeconds > 86400 {
		return fmt.Errorf("webhook_throttle_seconds must be between 1 and 86400")
	}
	if values.AuditRetentionRows < 0 || values.AuditRetentionRows > 10_000_000 {
		return fmt.Errorf("audit_retention_rows out of range")
	}
	cronExpr := values.CheckinCron
	if cronExpr == "" {
		cronExpr = "0 8 * * *"
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(cronExpr); err != nil {
		return fmt.Errorf("checkin_cron is invalid")
	}
	// Discovery cron: empty = disabled, otherwise must be a valid expression.
	if values.DiscoveryCron != "" {
		if _, err := parser.Parse(values.DiscoveryCron); err != nil {
			return fmt.Errorf("discovery_cron is invalid")
		}
	}
	if values.DBGCCron != "" {
		if _, err := parser.Parse(values.DBGCCron); err != nil {
			return fmt.Errorf("db_gc_cron is invalid")
		}
	}
	return nil
}
