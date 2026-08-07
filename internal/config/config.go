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
	HTTPAddr       string
	DataDir        string
	AdminToken     string
	AdminTokens    []string
	MasterKey      string
	RetryTimes     int
	Cooldown       time.Duration
	// SQLiteMaxOpenConns is the SQLite connection-pool ceiling (WAL allows
	// concurrent readers). Default 4; 1 restores the fully serialized behavior.
	SQLiteMaxOpenConns int
	CheckinEnabled bool
	CheckinCron    string
	// CheckinTZ is the IANA timezone (e.g. "Asia/Shanghai") the check-in cron is
	// interpreted in. Empty means the process local timezone (UTC in containers).
	CheckinTZ string

	WebDAVSyncEnabled    bool
	WebDAVURL            string
	WebDAVUsername       string
	WebDAVPassword       string
	WebDAVBackupPassword string
	WebDAVCron           string
	WebDAVMaxBytes       int64

	OutboundAllowHosts            []string
	OutboundAllowCIDRs            []string
	OutboundConnectTimeout        time.Duration
	OutboundTLSHandshakeTimeout   time.Duration
	OutboundResponseHeaderTimeout time.Duration
	// OutboundMaxIdleConns is the total outbound idle connection ceiling.
	OutboundMaxIdleConns int
	// OutboundMaxIdleConnsPerHost is the per-upstream-host idle connection ceiling.
	OutboundMaxIdleConnsPerHost int
	TrustedProxyCIDRs             []string
	RelayRatePerMinute            int
	RelayRateBurst                int
	RelayModelRatePerMinute       int
	RelayModelRateBurst           int
	// ChannelAutoDisableThreshold: consecutive member failures before a channel
	// is auto-disabled. 0 disables the feature.
	ChannelAutoDisableThreshold int
	// RoutingLatencyAware enables latency-weighted channel selection.
	RoutingLatencyAware bool
	// RoutingErrorAware penalizes channels with a high EWMA failure propensity.
	RoutingErrorAware bool
	// RoutingConcurrencyEnabled enables the in-flight burst guard.
	RoutingConcurrencyEnabled bool
	// RoutingConcurrencyLimit is the per-channel in-flight ceiling.
	RoutingConcurrencyLimit int
// WebhookURL is the operational notification endpoint ("" disables).
WebhookURL string
// WebhookThrottleSeconds coalesces repeated events within the window.
WebhookThrottleSeconds int
// AlertConfigJSON is the multi-channel alert matrix config (bark/serverchan/
// telegram/smtp + cooldown + daily summary flag), JSON-encoded.
AlertConfigJSON string
// AlertDailySummaryInterval is how often the daily digest runs (0 = off).
AlertDailySummaryInterval time.Duration
// AlertSweepInterval is how often the proactive health sweep runs (0 = off).
AlertSweepInterval time.Duration
	// RecoveryProbeEnabled enables the passive-recovery loop for auto-disabled channels.
	RecoveryProbeEnabled bool
	// RecoveryProbeIntervalSeconds is the recovery-loop cadence.
	RecoveryProbeIntervalSeconds int
	// ProgressiveCooldownEnabled enables tiered cooldown with per-success decay.
	ProgressiveCooldownEnabled bool
	// CooldownLevel2Seconds/3/4 are the tiered cooldown penalties for the
	// second/third/fourth consecutive failures.
	CooldownLevel2Seconds int
	CooldownLevel3Seconds int
	CooldownLevel4Seconds int
	// BreakerFailCount is the consecutive-failure threshold that parks a member.
	BreakerFailCount int
	// StickyEnabled enables sticky-session routing (same conversation prefers
	// the previously successful channel).
	StickyEnabled bool
	// StickyTTL is how long a session binding stays valid without renewal.
	StickyTTL time.Duration
	// StableFirstEnabled gates the 1/N grayscale pool.
	StableFirstEnabled bool
	// StableFirstDenominator is the draw base (25 = grayscale gets 1/25).
	StableFirstDenominator int
	// StableFirstPromoteRequests is the successful-attempt threshold for
	// automatic promotion out of the grayscale pool.
	StableFirstPromoteRequests int
	AdminRatePerMinute      int
	AdminRateBurst          int
	MetricsToken            string
	TrustedScraperCIDRs     []string
	MaxHeaderBytes          int
	MaxAdminBodyBytes       int64
	ServerReadHeaderTimeout time.Duration
	ServerReadTimeout       time.Duration
	ServerIdleTimeout       time.Duration
	ServerShutdownTimeout   time.Duration
	ReadinessTimeout        time.Duration
	AuditRetentionDays      int
	AuditRetentionRows      int
	BackupDir               string
	PluginsDir              string
	PluginCatalogURL        string
	// ExchangeAllowSecretExport gates include_secrets on export (default true for compat).
	ExchangeAllowSecretExport bool
	// HealthSweepEnabled enables the periodic channel health sweep (jittered
	// probes grading operational/degraded/error with transition alerts).
	HealthSweepEnabled bool
	// HealthSweepIntervalSeconds is the base probe interval.
	HealthSweepIntervalSeconds int
	// HealthSweepJitterSeconds is the per-round random jitter ceiling.
	HealthSweepJitterSeconds int
	// HealthSweepDegradedMs: latency above this grades the channel degraded.
	HealthSweepDegradedMs int
	// HealthSweepConcurrency caps simultaneous probes.
	HealthSweepConcurrency int
	// HealthSweepTimeoutSeconds bounds one probe.
	HealthSweepTimeoutSeconds int
}

func Load() (*Config, error) {
	retryTimes, err := envInt("RETRY_TIMES", 2, 0, 100)
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
	webdavMaxBytes, err := envInt("WEBDAV_MAX_BYTES", 10<<20, 1024, 32<<20)
	if err != nil {
		return nil, err
	}
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
	progressiveCooldown, err := envBool("PROGRESSIVE_COOLDOWN_ENABLED", true)
	if err != nil {
		return nil, err
	}
	cooldownLevel2, err := envInt("COOLDOWN_LEVEL2_SECONDS", 600, 0, 86400*7)
	if err != nil {
		return nil, err
	}
	cooldownLevel3, err := envInt("COOLDOWN_LEVEL3_SECONDS", 3600, 0, 86400*7)
	if err != nil {
		return nil, err
	}
	cooldownLevel4, err := envInt("COOLDOWN_LEVEL4_SECONDS", 86400, 0, 86400*30)
	if err != nil {
		return nil, err
	}
	breakerFailCount, err := envInt("BREAKER_FAIL_COUNT", 3, 2, 100)
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
	healthSweepEnabled, err := envBool("HEALTH_SWEEP_ENABLED", false)
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
		HTTPAddr:       envStr("HTTP_ADDR", ":4100"),
		DataDir:        dataDir,
		AdminToken:     firstNonEmpty(adminTokens),
		AdminTokens:    adminTokens,
		MasterKey:      envStr("MASTER_KEY", ""),
		RetryTimes:     retryTimes,
		Cooldown:       time.Duration(cooldownSeconds) * time.Second,
		CheckinEnabled: checkinEnabled,
		CheckinCron:    envStr("CHECKIN_CRON", "0 8 * * *"),
		CheckinTZ:      checkinTZ,

		WebDAVSyncEnabled:    webdavSyncEnabled,
		WebDAVURL:            strings.TrimSpace(envStr("WEBDAV_URL", "")),
		WebDAVUsername:       envStr("WEBDAV_USERNAME", ""),
		WebDAVPassword:       envStr("WEBDAV_PASSWORD", ""),
		WebDAVBackupPassword: envStr("WEBDAV_BACKUP_PASSWORD", ""),
		WebDAVCron:           envStr("WEBDAV_CRON", "0 */6 * * *"),
		WebDAVMaxBytes:       int64(webdavMaxBytes),

		OutboundAllowHosts:            hosts,
		OutboundAllowCIDRs:            cidrs,
		OutboundConnectTimeout:        connectTimeout,
		OutboundTLSHandshakeTimeout:   tlsTimeout,
		OutboundResponseHeaderTimeout: headerTimeout,
		OutboundMaxIdleConns:          outboundMaxIdle,
		OutboundMaxIdleConnsPerHost:   outboundMaxIdlePerHost,
		SQLiteMaxOpenConns:            sqliteMaxConns,
		TrustedProxyCIDRs:             trustedProxies,
		RelayRatePerMinute:            relayRate, RelayRateBurst: relayBurst,
		RelayModelRatePerMinute: relayModelRate, RelayModelRateBurst: relayModelBurst,
		ChannelAutoDisableThreshold: autoDisableThreshold,
		RoutingLatencyAware:         latencyAware,
		RoutingErrorAware:           errorAware,
		RoutingConcurrencyEnabled:  concurrencyAware,
		RoutingConcurrencyLimit:    concurrencyLimit,
		WebhookURL:                 webhookURL,
		WebhookThrottleSeconds:     webhookThrottle,
		AlertConfigJSON:            alertConfigJSON,
		AlertDailySummaryInterval:  alertDailyInterval,
		AlertSweepInterval:         alertSweepInterval,
		RecoveryProbeEnabled:        recoveryProbe,
		RecoveryProbeIntervalSeconds: recoveryProbeInterval,
		ProgressiveCooldownEnabled:  progressiveCooldown,
		CooldownLevel2Seconds:       cooldownLevel2,
		CooldownLevel3Seconds:       cooldownLevel3,
		CooldownLevel4Seconds:       cooldownLevel4,
		BreakerFailCount:            breakerFailCount,
		StickyEnabled:               stickyEnabled,
		StickyTTL:                   time.Duration(stickyTTLMinutes) * time.Minute,
		StableFirstEnabled:          stableFirstEnabled,
		StableFirstDenominator:      stableFirstDenominator,
		StableFirstPromoteRequests:  stableFirstPromote,
		AdminRatePerMinute:          adminRate, AdminRateBurst: adminBurst,
		MetricsToken: metricsToken, TrustedScraperCIDRs: trustedScrapers,
		MaxHeaderBytes: maxHeaderBytes, MaxAdminBodyBytes: int64(maxAdminBodyBytes),
		ServerReadHeaderTimeout: readHeaderTimeout, ServerReadTimeout: readTimeout,
		ServerIdleTimeout: idleTimeout, ServerShutdownTimeout: shutdownTimeout,
		ReadinessTimeout: readinessTimeout, AuditRetentionDays: auditDays,
		AuditRetentionRows: auditRows, BackupDir: envStr("BACKUP_DIR", filepath.Join(dataDir, "backups")),
		PluginsDir:                envStr("PLUGINS_DIR", filepath.Join(dataDir, "plugins")),
		PluginCatalogURL:          envStr("PLUGIN_CATALOG_URL", ""),
		ExchangeAllowSecretExport: exchangeAllowSecretExport,
		HealthSweepEnabled:        healthSweepEnabled,
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

// envIntSeconds parses an integer environment variable as seconds.
func envIntSeconds(key string, def, min, max int) (time.Duration, error) {
	n, err := envInt(key, def, min, max)
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * time.Second, nil
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
