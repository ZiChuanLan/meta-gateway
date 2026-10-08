package config

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr      string   `env:"HTTP_ADDR"`
	DataDir       string   `env:"DATA_DIR"`
	AdminToken    string   `env:"ADMIN_TOKEN" secret:"true"`
	AdminUsername string   `env:"ADMIN_USERNAME"`
	AdminTokens   []string `env:"ADMIN_TOKENS" secret:"true"`
	// AdminTokenLogin re-opens the ADMIN_TOKEN-as-password login after the
	// deployment has claimed an owner account. Empty (the default) means the
	// token only works while no owner credential exists, which is what makes the
	// first-run claim close the upgrade path by itself; "break-glass" is the
	// documented recovery hatch for a deployment whose owner forgot the password.
	AdminTokenLogin string `env:"ADMIN_TOKEN_LOGIN"`
	MasterKey       string `env:"MASTER_KEY" secret:"true"`
	RetryTimes      int    `env:"RETRY_TIMES"`
	// ChannelRetryTimes is how many times the same upstream key is re-sent
	// after a retryable failure before moving to the next key/channel.
	// Network errors (transport) fail fast after these retries instead of
	// fanning out across every channel.
	ChannelRetryTimes int `env:"CHANNEL_RETRY_TIMES"`
	// UpdateCheckEnabled lets the gateway query GitHub for newer releases to
	// power the console update badge.
	UpdateCheckEnabled          bool          `env:"UPDATE_CHECK_ENABLED"`
	CrossChannelFailoverEnabled bool          `env:"CROSS_CHANNEL_FAILOVER_ENABLED"`
	Cooldown                    time.Duration `env:"COOLDOWN_SECONDS"`
	// SQLiteMaxOpenConns is the SQLite connection-pool ceiling (WAL allows
	// concurrent readers). Default 4; 1 restores the fully serialized behavior.
	SQLiteMaxOpenConns int    `env:"SQLITE_MAX_OPEN_CONNS"`
	CheckinEnabled     bool   `env:"CHECKIN_ENABLED"`
	CheckinCron        string `env:"CHECKIN_CRON"`
	// CheckinTZ is the IANA timezone (e.g. "Asia/Shanghai") the check-in cron is
	// interpreted in. Empty means the process local timezone (UTC in containers).
	CheckinTZ string `env:"CHECKIN_TZ"`

	WebDAVSyncEnabled    bool   `env:"WEBDAV_SYNC_ENABLED"`
	WebDAVUploadEnabled  bool   `env:"WEBDAV_UPLOAD_ENABLED"`
	WebDAVURL            string `env:"WEBDAV_URL"`
	WebDAVUsername       string `env:"WEBDAV_USERNAME"`
	WebDAVPassword       string `env:"WEBDAV_PASSWORD" secret:"true"`
	WebDAVBackupPassword string `env:"WEBDAV_BACKUP_PASSWORD" secret:"true"`
	// Upload direction owns its own connection; each WEBDAV_UPLOAD_* falls back
	// to the matching shared variable when unset.
	WebDAVUploadURL            string `env:"WEBDAV_UPLOAD_URL"`
	WebDAVUploadUsername       string `env:"WEBDAV_UPLOAD_USERNAME"`
	WebDAVUploadPassword       string `env:"WEBDAV_UPLOAD_PASSWORD" secret:"true"`
	WebDAVUploadBackupPassword string `env:"WEBDAV_UPLOAD_BACKUP_PASSWORD" secret:"true"`
	WebDAVCron                 string `env:"WEBDAV_CRON"`
	WebDAVMaxBytes             int64  `env:"WEBDAV_MAX_BYTES"`

	OutboundAllowHosts            []string      `env:"OUTBOUND_ALLOW_HOSTS"`
	OutboundAllowCIDRs            []string      `env:"OUTBOUND_ALLOW_CIDRS"`
	OutboundConnectTimeout        time.Duration `env:"OUTBOUND_CONNECT_TIMEOUT_SECONDS"`
	OutboundTLSHandshakeTimeout   time.Duration `env:"OUTBOUND_TLS_TIMEOUT_SECONDS"`
	OutboundResponseHeaderTimeout time.Duration `env:"OUTBOUND_HEADER_TIMEOUT_SECONDS"`
	// OutboundImageHeaderTimeout is the response-header ceiling for the image
	// endpoints, which are slower by nature and never retried.
	OutboundImageHeaderTimeout time.Duration `env:"OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS"`
	// OutboundMaxIdleConns is the total outbound idle connection ceiling.
	OutboundMaxIdleConns int `env:"OUTBOUND_MAX_IDLE_CONNS"`
	// OutboundMaxIdleConnsPerHost is the per-upstream-host idle connection ceiling.
	OutboundMaxIdleConnsPerHost int      `env:"OUTBOUND_MAX_IDLE_CONNS_PER_HOST"`
	TrustedProxyCIDRs           []string `env:"TRUSTED_PROXY_CIDRS"`
	RelayRatePerMinute          int      `env:"RELAY_RATE_PER_MINUTE"`
	RelayRateBurst              int      `env:"RELAY_RATE_BURST"`
	RelayModelRatePerMinute     int      `env:"RELAY_MODEL_RATE_PER_MINUTE"`
	RelayModelRateBurst         int      `env:"RELAY_MODEL_RATE_BURST"`
	// ChannelAutoDisableThreshold: consecutive member failures before a channel
	// is auto-disabled. 0 disables the feature.
	ChannelAutoDisableThreshold int `env:"CHANNEL_AUTO_DISABLE_THRESHOLD"`
	// RoutingLatencyAware enables latency-weighted channel selection.
	RoutingLatencyAware bool `env:"ROUTING_LATENCY_AWARE"`
	// RoutingErrorAware penalizes channels with a high EWMA failure propensity.
	RoutingErrorAware bool `env:"ROUTING_ERROR_AWARE"`
	// RoutingConcurrencyEnabled enables the in-flight burst guard.
	RoutingConcurrencyEnabled bool `env:"ROUTING_CONCURRENCY_AWARE"`
	// RoutingConcurrencyLimit is the per-channel in-flight ceiling.
	RoutingConcurrencyLimit int `env:"ROUTING_CONCURRENCY_LIMIT"`
	// WebhookURL is the operational notification endpoint ("" disables).
	WebhookURL string `env:"WEBHOOK_URL" secret:"true"`
	// WebhookThrottleSeconds coalesces repeated events within the window.
	WebhookThrottleSeconds int `env:"WEBHOOK_THROTTLE_SECONDS"`
	// AlertConfigJSON is the multi-channel alert matrix config (bark/serverchan/
	// telegram/smtp + cooldown + daily summary flag), JSON-encoded.
	AlertConfigJSON string `env:"ALERT_CONFIG_JSON" secret:"true"`
	// AlertDailySummaryInterval is how often the daily digest runs (0 = off).
	AlertDailySummaryInterval time.Duration `env:"ALERT_DAILY_SUMMARY_INTERVAL_SECONDS"`
	// AlertSweepInterval is how often the proactive health sweep runs (0 = off).
	AlertSweepInterval time.Duration `env:"ALERT_SWEEP_INTERVAL_SECONDS"`
	// RecoveryProbeEnabled enables the passive-recovery loop for auto-disabled channels.
	RecoveryProbeEnabled bool `env:"RECOVERY_PROBE_ENABLED"`
	// RecoveryProbeIntervalSeconds is the recovery-loop cadence.
	RecoveryProbeIntervalSeconds int `env:"RECOVERY_PROBE_INTERVAL_SECONDS"`
	// FaultProtectionEnabled gates fixed cooldown and channel auto-disable.
	FaultProtectionEnabled bool `env:"FAULT_PROTECTION_ENABLED"`
	// FaultProtectionConfigured distinguishes a loaded config from a minimal
	// test/embedder literal, whose zero value should preserve legacy protection.
	FaultProtectionConfigured bool
	// StickyEnabled enables sticky-session routing (same conversation prefers
	// the previously successful channel).
	StickyEnabled bool `env:"STICKY_ENABLED"`
	// StickyTTL is how long a session binding stays valid without renewal.
	StickyTTL time.Duration `env:"STICKY_TTL_MINUTES"`
	// StableFirstEnabled gates the 1/N grayscale pool.
	StableFirstEnabled bool `env:"STABLE_FIRST_ENABLED"`
	// StableFirstDenominator is the draw base (25 = grayscale gets 1/25).
	StableFirstDenominator int `env:"STABLE_FIRST_DENOMINATOR"`
	// StableFirstPromoteRequests is the successful-attempt threshold for
	// automatic promotion out of the grayscale pool.
	StableFirstPromoteRequests int      `env:"STABLE_FIRST_PROMOTE_REQUESTS"`
	AdminRatePerMinute         int      `env:"ADMIN_RATE_PER_MINUTE"`
	AdminRateBurst             int      `env:"ADMIN_RATE_BURST"`
	MetricsToken               string   `env:"METRICS_TOKEN" secret:"true"`
	TrustedScraperCIDRs        []string `env:"TRUSTED_SCRAPER_CIDRS"`
	// CORSAllowedOrigins opens the downstream /v1 surface to browser callers.
	// Empty means any origin ("*"), which is the zero-config default; entries
	// are exact origins or "*.example.com" subdomain patterns.
	CORSAllowedOrigins      []string      `env:"CORS_ALLOWED_ORIGINS"`
	MaxHeaderBytes          int           `env:"MAX_HEADER_BYTES"`
	MaxAdminBodyBytes       int64         `env:"MAX_ADMIN_BODY_BYTES"`
	ServerReadHeaderTimeout time.Duration `env:"SERVER_READ_HEADER_TIMEOUT_SECONDS"`
	ServerReadTimeout       time.Duration `env:"SERVER_READ_TIMEOUT_SECONDS"`
	ServerIdleTimeout       time.Duration `env:"SERVER_IDLE_TIMEOUT_SECONDS"`
	ServerShutdownTimeout   time.Duration `env:"SERVER_SHUTDOWN_TIMEOUT_SECONDS"`
	ReadinessTimeout        time.Duration `env:"READINESS_TIMEOUT_SECONDS"`
	AuditRetentionDays      int           `env:"AUDIT_RETENTION_DAYS"`
	AuditRetentionRows      int           `env:"AUDIT_RETENTION_ROWS"`
	// HealthHistoryRetentionDays bounds channel_health_history rows (default 90).
	HealthHistoryRetentionDays int `env:"HEALTH_HISTORY_RETENTION_DAYS"`
	// BalanceHistoryRetentionDays and DecisionSnapshotRetentionDays control
	// the other daily maintenance pruners. Zero disables each pruner.
	BalanceHistoryRetentionDays   int `env:"BALANCE_HISTORY_RETENTION_DAYS"`
	DecisionSnapshotRetentionDays int `env:"DECISION_SNAPSHOT_RETENTION_DAYS"`
	// ModelChangeRetentionDays bounds finished model_changes rows (default
	// 90); ModelChangeAutoIgnoreDays auto-ignores pending removals with no
	// route impact after that many days (0 = off by default).
	ModelChangeRetentionDays  int `env:"MODEL_CHANGE_RETENTION_DAYS"`
	ModelChangeAutoIgnoreDays int `env:"MODEL_CHANGE_AUTO_IGNORE_DAYS"`
	// SiteProbeRetentionDays bounds external site-probe rounds and their
	// samples (default 7): one row per monitored model per site per round.
	SiteProbeRetentionDays int `env:"SITE_PROBE_RETENTION_DAYS"`
	// SiteProbeIntervalSeconds / SiteProbeJitterSeconds are the external
	// site-probe collection cadence (defaults 900 + 120). They are the env
	// bootstrap for a hot-reloadable runtime setting, so an operator can speed
	// the round up for sites that publish heartbeats every minute without a
	// restart.
	SiteProbeIntervalSeconds int `env:"SITE_PROBE_INTERVAL_SECONDS"`
	SiteProbeJitterSeconds   int `env:"SITE_PROBE_JITTER_SECONDS"`
	// SiteProbeCatalogURL is the third-party monitoring directory the scheduled
	// third-party health snapshot is read from. Empty keeps the shipped default
	// (siteprobe.DefaultCatalogURL).
	SiteProbeCatalogURL  string `env:"SITE_PROBE_CATALOG_URL"`
	BackupRetentionCount int    `env:"BACKUP_RETENTION_COUNT"`
	BackupDir            string `env:"BACKUP_DIR"`
	PluginsDir           string `env:"PLUGINS_DIR"`
	PluginCatalogURL     string `env:"PLUGIN_CATALOG_URL"`
	// PluginMarketURLs appends extra plugin market registry URLs
	// (comma-separated; the built-in official registry is always included).
	PluginMarketURLs []string `env:"PLUGIN_MARKET_URLS"`
	// ModelCatalogSources selects which external model indexes a sync reads
	// (comma-separated: "litellm", "models.dev"). Empty disables the sync
	// entirely, which is also what a zero interval does.
	ModelCatalogSources []string `env:"MODEL_CATALOG_SOURCES"`
	// ModelCatalogInterval is the gap between scheduled catalog syncs. Zero
	// turns the schedule off while leaving the manual sync available.
	ModelCatalogInterval time.Duration `env:"MODEL_CATALOG_SYNC_INTERVAL_HOURS"`
	// ModelCatalogSyncPrices lets a sync fill in per-1k prices it finds. It
	// only ever fills columns the gateway has left at zero, so an operator's own
	// price always wins; set false to keep every price hand-managed.
	ModelCatalogSyncPrices bool `env:"MODEL_CATALOG_SYNC_PRICES"`
	// ExchangeAllowSecretExport gates include_secrets on export (default true for compat).
	ExchangeAllowSecretExport bool `env:"EXCHANGE_ALLOW_SECRET_EXPORT"`
	// HealthSweepEnabled enables the periodic channel health sweep (jittered
	// probes grading operational/degraded/error with transition alerts).
	HealthSweepEnabled bool `env:"HEALTH_SWEEP_ENABLED"`
	// HealthSweepIntervalSeconds is the base probe interval.
	HealthSweepIntervalSeconds int `env:"HEALTH_SWEEP_INTERVAL_SECONDS"`
	// HealthSweepJitterSeconds is the per-round random jitter ceiling.
	HealthSweepJitterSeconds int `env:"HEALTH_SWEEP_JITTER_SECONDS"`
	// HealthSweepDegradedMs: latency above this grades the channel degraded.
	HealthSweepDegradedMs int `env:"HEALTH_SWEEP_DEGRADED_MS"`
	// HealthSweepConcurrency caps simultaneous probes.
	HealthSweepConcurrency int `env:"HEALTH_SWEEP_CONCURRENCY"`
	// HealthSweepTimeoutSeconds bounds one probe.
	HealthSweepTimeoutSeconds int `env:"HEALTH_SWEEP_TIMEOUT_SECONDS"`
}

func Load() (*Config, error) {
	retryTimes, err := envInt("RETRY_TIMES", 2, 0, 100)
	if err != nil {
		return nil, err
	}
	channelRetryTimes, err := envInt("CHANNEL_RETRY_TIMES", 1, 0, 5)
	if err != nil {
		return nil, err
	}

	updateCheckEnabled, err := envBool("UPDATE_CHECK_ENABLED", true)
	if err != nil {
		return nil, err
	}
	crossChannelFailover, err := envBool("CROSS_CHANNEL_FAILOVER_ENABLED", true)
	if err != nil {
		return nil, err
	}
	cooldownSeconds, err := envInt("COOLDOWN_SECONDS", 30, 0, 86400)
	if err != nil {
		return nil, err
	}
	checkinEnabled, err := envBool("CHECKIN_ENABLED", false)
	if err != nil {
		return nil, err
	}
	checkinTZ := strings.TrimSpace(envStr("CHECKIN_TZ", ""))
	if checkinTZ != "" {
		if _, err := time.LoadLocation(checkinTZ); err != nil {
			return nil, fmt.Errorf("config: CHECKIN_TZ must be a valid IANA timezone (e.g. Asia/Shanghai): %v", err)
		}
	}
	webdavSyncEnabled, err := envBool("WEBDAV_SYNC_ENABLED", false)
	if err != nil {
		return nil, err
	}
	webdavUploadEnabled, err := envBool("WEBDAV_UPLOAD_ENABLED", false)
	if err != nil {
		return nil, err
	}
	webdavMaxBytes, err := envInt("WEBDAV_MAX_BYTES", 10<<20, 1024, 32<<20)
	if err != nil {
		return nil, err
	}
	webdavURL := strings.TrimSpace(envStr("WEBDAV_URL", ""))
	webdavUsername := envStr("WEBDAV_USERNAME", "")
	webdavPassword := envStr("WEBDAV_PASSWORD", "")
	webdavBackupPassword := envStr("WEBDAV_BACKUP_PASSWORD", "")
	// The upload direction owns its own connection; unset WEBDAV_UPLOAD_*
	// variables reuse the shared ones so existing compose files keep working.
	webdavUploadURL := firstNonEmpty([]string{strings.TrimSpace(envStr("WEBDAV_UPLOAD_URL", "")), webdavURL})
	webdavUploadUsername := firstNonEmpty([]string{envStr("WEBDAV_UPLOAD_USERNAME", ""), webdavUsername})
	webdavUploadPassword := firstNonEmpty([]string{envStr("WEBDAV_UPLOAD_PASSWORD", ""), webdavPassword})
	webdavUploadBackupPassword := firstNonEmpty([]string{envStr("WEBDAV_UPLOAD_BACKUP_PASSWORD", ""), webdavBackupPassword})
	connectTimeout, err := envDurationSeconds("OUTBOUND_CONNECT_TIMEOUT_SECONDS", 10, 1, 300)
	if err != nil {
		return nil, err
	}
	tlsTimeout, err := envDurationSeconds("OUTBOUND_TLS_TIMEOUT_SECONDS", 10, 1, 300)
	if err != nil {
		return nil, err
	}
	headerTimeout, err := envDurationSeconds("OUTBOUND_HEADER_TIMEOUT_SECONDS", 60, 1, 3600)
	if err != nil {
		return nil, err
	}
	// Image generation and editing is the one family that legitimately takes
	// minutes: the caller is waiting for an artifact, the operation is never
	// retried (a replay would bill twice), and the upstream may need well over
	// the chat timeout. Without a separate ceiling those requests fail with
	// "timeout awaiting response headers" while the upstream is still working.
	imageHeaderTimeout, err := envDurationSeconds("OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS", 300, 1, 3600)
	if err != nil {
		return nil, err
	}
	sqliteMaxConns, err := envInt("SQLITE_MAX_OPEN_CONNS", 4, 1, 16)
	if err != nil {
		return nil, err
	}
	outboundMaxIdle, err := envInt("OUTBOUND_MAX_IDLE_CONNS", 512, 0, 100000)
	if err != nil {
		return nil, err
	}
	outboundMaxIdlePerHost, err := envInt("OUTBOUND_MAX_IDLE_CONNS_PER_HOST", 64, 0, 10000)
	if err != nil {
		return nil, err
	}
	hosts, err := envHosts("OUTBOUND_ALLOW_HOSTS")
	if err != nil {
		return nil, err
	}
	cidrs, err := envCIDRs("OUTBOUND_ALLOW_CIDRS")
	if err != nil {
		return nil, err
	}
	trustedProxies, err := envCIDRs("TRUSTED_PROXY_CIDRS")
	if err != nil {
		return nil, err
	}
	relayRate, err := envInt("RELAY_RATE_PER_MINUTE", 600, 0, 1000000)
	if err != nil {
		return nil, err
	}
	relayModelRate, modelRateErr := envInt("RELAY_MODEL_RATE_PER_MINUTE", 0, 0, 1000000)
	if modelRateErr != nil {
		return nil, modelRateErr
	}
	relayModelBurst, modelBurstErr := envInt("RELAY_MODEL_RATE_BURST", 0, 0, 1000000)
	if modelBurstErr != nil {
		return nil, modelBurstErr
	}
	autoDisableThreshold, autoDisableErr := envInt("CHANNEL_AUTO_DISABLE_THRESHOLD", 5, 0, 1000)
	if autoDisableErr != nil {
		return nil, autoDisableErr
	}
	latencyAware, err := envBool("ROUTING_LATENCY_AWARE", true)
	if err != nil {
		return nil, err
	}
	errorAware, err := envBool("ROUTING_ERROR_AWARE", true)
	if err != nil {
		return nil, err
	}
	concurrencyAware, err := envBool("ROUTING_CONCURRENCY_AWARE", true)
	if err != nil {
		return nil, err
	}
	concurrencyLimit, err := envInt("ROUTING_CONCURRENCY_LIMIT", 64, 1, 100000)
	if err != nil {
		return nil, err
	}
	webhookURL := strings.TrimSpace(envStr("WEBHOOK_URL", ""))
	webhookThrottle, err := envInt("WEBHOOK_THROTTLE_SECONDS", 300, 1, 86400)
	if err != nil {
		return nil, err
	}
	alertConfigJSON := strings.TrimSpace(envStr("ALERT_CONFIG_JSON", ""))
	alertDailyInterval, err := envIntSeconds("ALERT_DAILY_SUMMARY_INTERVAL_SECONDS", 0, 0, 24*60*60)
	if err != nil {
		return nil, err
	}
	alertSweepInterval, err := envIntSeconds("ALERT_SWEEP_INTERVAL_SECONDS", 0, 0, 24*60*60)
	if err != nil {
		return nil, err
	}
	recoveryProbe, err := envBool("RECOVERY_PROBE_ENABLED", true)
	if err != nil {
		return nil, err
	}
	recoveryProbeInterval, err := envInt("RECOVERY_PROBE_INTERVAL_SECONDS", 600, 10, 86400)
	if err != nil {
		return nil, err
	}
	faultProtection, err := envBool("FAULT_PROTECTION_ENABLED", true)
	if err != nil {
		return nil, err
	}
	stickyEnabled, err := envBool("STICKY_ENABLED", false)
	if err != nil {
		return nil, err
	}
	stickyTTLMinutes, err := envInt("STICKY_TTL_MINUTES", 30, 1, 1440)
	if err != nil {
		return nil, err
	}
	stableFirstEnabled, err := envBool("STABLE_FIRST_ENABLED", false)
	if err != nil {
		return nil, err
	}
	stableFirstDenominator, err := envInt("STABLE_FIRST_DENOMINATOR", 25, 2, 1000)
	if err != nil {
		return nil, err
	}
	stableFirstPromote, err := envInt("STABLE_FIRST_PROMOTE_REQUESTS", 100, 1, 100000)
	if err != nil {
		return nil, err
	}
	relayBurst, err := envInt("RELAY_RATE_BURST", 100, 0, 1000000)
	if err != nil {
		return nil, err
	}
	adminRate, err := envInt("ADMIN_RATE_PER_MINUTE", 300, 0, 1000000)
	if err != nil {
		return nil, err
	}
	adminBurst, err := envInt("ADMIN_RATE_BURST", 50, 0, 1000000)
	if err != nil {
		return nil, err
	}
	trustedScrapers, err := envCIDRs("TRUSTED_SCRAPER_CIDRS")
	if err != nil {
		return nil, err
	}
	// Absent or empty keeps the downstream surface open to any origin.
	corsOrigins := splitList(os.Getenv("CORS_ALLOWED_ORIGINS"))
	maxHeaderBytes, err := envInt("MAX_HEADER_BYTES", 1<<20, 4096, 16<<20)
	if err != nil {
		return nil, err
	}
	maxAdminBodyBytes, err := envInt("MAX_ADMIN_BODY_BYTES", 2<<20, 1024, 16<<20)
	if err != nil {
		return nil, err
	}
	readHeaderTimeout, err := envDurationSeconds("SERVER_READ_HEADER_TIMEOUT_SECONDS", 10, 1, 300)
	if err != nil {
		return nil, err
	}
	readTimeout, err := envDurationSeconds("SERVER_READ_TIMEOUT_SECONDS", 30, 1, 3600)
	if err != nil {
		return nil, err
	}
	idleTimeout, err := envDurationSeconds("SERVER_IDLE_TIMEOUT_SECONDS", 120, 1, 3600)
	if err != nil {
		return nil, err
	}
	shutdownTimeout, err := envDurationSeconds("SERVER_SHUTDOWN_TIMEOUT_SECONDS", 15, 1, 300)
	if err != nil {
		return nil, err
	}
	readinessTimeout, err := envDurationSeconds("READINESS_TIMEOUT_SECONDS", 2, 1, 30)
	if err != nil {
		return nil, err
	}
	auditDays, err := envInt("AUDIT_RETENTION_DAYS", 90, 0, 36500)
	if err != nil {
		return nil, err
	}
	auditRows, err := envInt("AUDIT_RETENTION_ROWS", 100000, 0, 10000000)
	if err != nil {
		return nil, err
	}
	healthHistoryDays, err := envInt("HEALTH_HISTORY_RETENTION_DAYS", 90, 0, 36500)
	if err != nil {
		return nil, err
	}
	balanceHistoryDays, err := envInt("BALANCE_HISTORY_RETENTION_DAYS", 90, 0, 36500)
	if err != nil {
		return nil, err
	}
	decisionSnapshotDays, err := envInt("DECISION_SNAPSHOT_RETENTION_DAYS", 7, 0, 36500)
	if err != nil {
		return nil, err
	}
	modelChangeRetentionDays, err := envInt("MODEL_CHANGE_RETENTION_DAYS", 90, 0, 36500)
	if err != nil {
		return nil, err
	}
	siteProbeRetentionDays, err := envInt("SITE_PROBE_RETENTION_DAYS", 7, 0, 36500)
	if err != nil {
		return nil, err
	}
	siteProbeIntervalSeconds, err := envInt("SITE_PROBE_INTERVAL_SECONDS", 900, 60, 86400)
	if err != nil {
		return nil, err
	}
	siteProbeJitterSeconds, err := envInt("SITE_PROBE_JITTER_SECONDS", 120, 0, 3600)
	if err != nil {
		return nil, err
	}
	siteProbeCatalogURL := envStr("SITE_PROBE_CATALOG_URL", "")
	modelChangeAutoIgnoreDays, err := envInt("MODEL_CHANGE_AUTO_IGNORE_DAYS", 0, 0, 36500)
	if err != nil {
		return nil, err
	}
	backupRetentionCount, err := envInt("BACKUP_RETENTION_COUNT", 30, 0, 100000)
	if err != nil {
		return nil, err
	}
	// The catalog sweep defaults to daily. Zero disables the schedule but keeps
	// the console's manual sync working.
	modelCatalogHours, err := envInt("MODEL_CATALOG_SYNC_INTERVAL_HOURS", 24, 0, 8760)
	if err != nil {
		return nil, err
	}
	modelCatalogPrices, err := envBool("MODEL_CATALOG_SYNC_PRICES", true)
	if err != nil {
		return nil, err
	}
	modelCatalogSources, err := envModelCatalogSources()
	if err != nil {
		return nil, err
	}
	metricsToken := envStr("METRICS_TOKEN", "")
	if metricsToken == "" && len(trustedScrapers) == 0 {
		return nil, fmt.Errorf("config: METRICS_TOKEN or TRUSTED_SCRAPER_CIDRS is required")
	}
	dataDir := envStr("DATA_DIR", "./data")

	adminTokens, err := envAdminTokens()
	if err != nil {
		return nil, err
	}
	exchangeAllowSecretExport, err := envBool("EXCHANGE_ALLOW_SECRET_EXPORT", true)
	if err != nil {
		return nil, err
	}
	healthSweepEnabled, err := envBool("HEALTH_SWEEP_ENABLED", true)
	if err != nil {
		return nil, err
	}
	healthSweepInterval, err := envInt("HEALTH_SWEEP_INTERVAL_SECONDS", 300, 10, 86400)
	if err != nil {
		return nil, err
	}
	healthSweepJitter, err := envInt("HEALTH_SWEEP_JITTER_SECONDS", 30, 0, 3600)
	if err != nil {
		return nil, err
	}
	healthSweepDegraded, err := envInt("HEALTH_SWEEP_DEGRADED_MS", 2000, 100, 60000)
	if err != nil {
		return nil, err
	}
	healthSweepConcurrency, err := envInt("HEALTH_SWEEP_CONCURRENCY", 4, 1, 64)
	if err != nil {
		return nil, err
	}
	healthSweepTimeout, err := envInt("HEALTH_SWEEP_TIMEOUT_SECONDS", 15, 1, 120)
	if err != nil {
		return nil, err
	}

	return &Config{
		HTTPAddr:                    envStr("HTTP_ADDR", ":4100"),
		DataDir:                     dataDir,
		AdminToken:                  firstNonEmpty(adminTokens),
		AdminUsername:               envStr("ADMIN_USERNAME", "admin"),
		AdminTokenLogin:             envStr("ADMIN_TOKEN_LOGIN", ""),
		AdminTokens:                 adminTokens,
		MasterKey:                   envStr("MASTER_KEY", ""),
		RetryTimes:                  retryTimes,
		ChannelRetryTimes:           channelRetryTimes,
		UpdateCheckEnabled:          updateCheckEnabled,
		CrossChannelFailoverEnabled: crossChannelFailover,
		Cooldown:                    time.Duration(cooldownSeconds) * time.Second,
		CheckinEnabled:              checkinEnabled,
		CheckinCron:                 envStr("CHECKIN_CRON", "0 8 * * *"),
		CheckinTZ:                   checkinTZ,

		WebDAVSyncEnabled:          webdavSyncEnabled,
		WebDAVUploadEnabled:        webdavUploadEnabled,
		WebDAVURL:                  webdavURL,
		WebDAVUsername:             webdavUsername,
		WebDAVPassword:             webdavPassword,
		WebDAVBackupPassword:       webdavBackupPassword,
		WebDAVUploadURL:            webdavUploadURL,
		WebDAVUploadUsername:       webdavUploadUsername,
		WebDAVUploadPassword:       webdavUploadPassword,
		WebDAVUploadBackupPassword: webdavUploadBackupPassword,
		WebDAVCron:                 envStr("WEBDAV_CRON", "0 */6 * * *"),
		WebDAVMaxBytes:             int64(webdavMaxBytes),

		OutboundAllowHosts:            hosts,
		OutboundAllowCIDRs:            cidrs,
		OutboundConnectTimeout:        connectTimeout,
		OutboundTLSHandshakeTimeout:   tlsTimeout,
		OutboundResponseHeaderTimeout: headerTimeout,
		OutboundImageHeaderTimeout:    imageHeaderTimeout,
		OutboundMaxIdleConns:          outboundMaxIdle,
		OutboundMaxIdleConnsPerHost:   outboundMaxIdlePerHost,
		SQLiteMaxOpenConns:            sqliteMaxConns,
		TrustedProxyCIDRs:             trustedProxies,
		RelayRatePerMinute:            relayRate, RelayRateBurst: relayBurst,
		RelayModelRatePerMinute: relayModelRate, RelayModelRateBurst: relayModelBurst,
		ChannelAutoDisableThreshold:  autoDisableThreshold,
		RoutingLatencyAware:          latencyAware,
		RoutingErrorAware:            errorAware,
		RoutingConcurrencyEnabled:    concurrencyAware,
		RoutingConcurrencyLimit:      concurrencyLimit,
		WebhookURL:                   webhookURL,
		WebhookThrottleSeconds:       webhookThrottle,
		AlertConfigJSON:              alertConfigJSON,
		AlertDailySummaryInterval:    alertDailyInterval,
		AlertSweepInterval:           alertSweepInterval,
		RecoveryProbeEnabled:         recoveryProbe,
		RecoveryProbeIntervalSeconds: recoveryProbeInterval,
		FaultProtectionEnabled:       faultProtection,
		FaultProtectionConfigured:    true,
		StickyEnabled:                stickyEnabled,
		StickyTTL:                    time.Duration(stickyTTLMinutes) * time.Minute,
		StableFirstEnabled:           stableFirstEnabled,
		StableFirstDenominator:       stableFirstDenominator,
		StableFirstPromoteRequests:   stableFirstPromote,
		AdminRatePerMinute:           adminRate, AdminRateBurst: adminBurst,
		MetricsToken: metricsToken, TrustedScraperCIDRs: trustedScrapers,
		CORSAllowedOrigins: corsOrigins,
		MaxHeaderBytes:     maxHeaderBytes, MaxAdminBodyBytes: int64(maxAdminBodyBytes),
		ServerReadHeaderTimeout: readHeaderTimeout, ServerReadTimeout: readTimeout,
		ServerIdleTimeout: idleTimeout, ServerShutdownTimeout: shutdownTimeout,
		ReadinessTimeout: readinessTimeout, AuditRetentionDays: auditDays,
		AuditRetentionRows: auditRows, HealthHistoryRetentionDays: healthHistoryDays,
		BalanceHistoryRetentionDays: balanceHistoryDays, DecisionSnapshotRetentionDays: decisionSnapshotDays,
		ModelChangeRetentionDays:   modelChangeRetentionDays,
		ModelChangeAutoIgnoreDays:  modelChangeAutoIgnoreDays,
		SiteProbeRetentionDays:     siteProbeRetentionDays,
		SiteProbeIntervalSeconds:   siteProbeIntervalSeconds,
		SiteProbeJitterSeconds:     siteProbeJitterSeconds,
		SiteProbeCatalogURL:        siteProbeCatalogURL,
		BackupRetentionCount:       backupRetentionCount,
		BackupDir:                  envStr("BACKUP_DIR", filepath.Join(dataDir, "backups")),
		PluginsDir:                 envStr("PLUGINS_DIR", filepath.Join(dataDir, "plugins")),
		PluginCatalogURL:           envStr("PLUGIN_CATALOG_URL", ""),
		ModelCatalogSources:        modelCatalogSources,
		ModelCatalogInterval:       time.Duration(modelCatalogHours) * time.Hour,
		ModelCatalogSyncPrices:     modelCatalogPrices,
		PluginMarketURLs:           envList("PLUGIN_MARKET_URLS"),
		ExchangeAllowSecretExport:  exchangeAllowSecretExport,
		HealthSweepEnabled:         healthSweepEnabled,
		HealthSweepIntervalSeconds: healthSweepInterval,
		HealthSweepJitterSeconds:   healthSweepJitter,
		HealthSweepDegradedMs:      healthSweepDegraded,
		HealthSweepConcurrency:     healthSweepConcurrency,
		HealthSweepTimeoutSeconds:  healthSweepTimeout,
	}, nil
}

func envAdminTokens() ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	add := func(raw string) {
		for _, part := range strings.Split(raw, ",") {
			token := strings.TrimSpace(part)
			if token == "" {
				continue
			}
			if _, exists := seen[token]; exists {
				continue
			}
			seen[token] = struct{}{}
			out = append(out, token)
		}
	}
	add(os.Getenv("ADMIN_TOKEN"))
	add(os.Getenv("ADMIN_TOKENS"))
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func firstNonEmpty(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// AdminTokenList returns rotation candidates (AdminTokens, falling back to AdminToken).
func (c *Config) AdminTokenList() []string {
	if c == nil {
		return nil
	}
	if len(c.AdminTokens) > 0 {
		return c.AdminTokens
	}
	if strings.TrimSpace(c.AdminToken) != "" {
		return []string{c.AdminToken}
	}
	return nil
}

// CheckinLocation returns the timezone check-in schedules run in. Falls back to
// the process local timezone when CHECKIN_TZ is unset (Load already validated
// the value when set, so a load error here is not expected).
func (c *Config) CheckinLocation() *time.Location {
	if c == nil || strings.TrimSpace(c.CheckinTZ) == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(strings.TrimSpace(c.CheckinTZ)); err == nil {
		return loc
	}
	return time.Local
}

func envBool(key string, def bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return def, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("config: %s must be a boolean", key)
	}
	return parsed, nil
}

func envStr(key, def string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return def
}

// envList splits a comma-separated environment variable into trimmed entries.
func envList(key string) []string {
	var out []string
	for _, part := range strings.Split(os.Getenv(key), ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// envIntSeconds parses an integer environment variable as seconds.
func envIntSeconds(key string, def, min, max int) (time.Duration, error) {
	n, err := envInt(key, def, min, max)
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * time.Second, nil
}

// modelCatalogSourceIDs is the accepted set for MODEL_CATALOG_SOURCES. It is
// duplicated from modelcatalog.DefaultURLs on purpose: config validates input
// and the catalog package owns the endpoints, and a typo here should be a
// startup error rather than a silently ignored source.
var modelCatalogSourceIDs = []string{"litellm", "models.dev"}

// envModelCatalogSources parses MODEL_CATALOG_SOURCES. Unset means every known
// source; naming a subset narrows the sweep and the manual sync alike.
func envModelCatalogSources() ([]string, error) {
	configured := envList("MODEL_CATALOG_SOURCES")
	if len(configured) == 0 {
		out := make([]string, len(modelCatalogSourceIDs))
		copy(out, modelCatalogSourceIDs)
		return out, nil
	}
	seen := make(map[string]bool, len(configured))
	out := make([]string, 0, len(configured))
	for _, value := range configured {
		match := ""
		for _, known := range modelCatalogSourceIDs {
			if strings.EqualFold(value, known) {
				match = known
				break
			}
		}
		if match == "" {
			return nil, fmt.Errorf("config: MODEL_CATALOG_SOURCES has unknown source %q (known: %s)",
				value, strings.Join(modelCatalogSourceIDs, ", "))
		}
		if seen[match] {
			continue
		}
		seen[match] = true
		out = append(out, match)
	}
	return out, nil
}

func envInt(key string, def, min, max int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return def, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("config: %s must be between %d and %d", key, min, max)
	}
	return n, nil
}

func envDurationSeconds(key string, def, min, max int) (time.Duration, error) {
	seconds, err := envInt(key, def, min, max)
	return time.Duration(seconds) * time.Second, err
}

func envHosts(key string) ([]string, error) {
	values := splitList(os.Getenv(key))
	for _, value := range values {
		if !validHostname(value) {
			return nil, fmt.Errorf("config: %s contains an invalid hostname", key)
		}
	}
	return values, nil
}

func validHostname(value string) bool {
	if value == "" || len(value) > 253 || net.ParseIP(value) != nil || strings.ContainsAny(value, "/:@") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func envCIDRs(key string) ([]string, error) {
	values := splitList(os.Getenv(key))
	for _, value := range values {
		if _, err := netip.ParsePrefix(value); err != nil {
			return nil, fmt.Errorf("config: %s contains an invalid CIDR", key)
		}
	}
	return values, nil
}

func splitList(raw string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, item := range strings.Split(raw, ",") {
		value := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(item), "."))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
