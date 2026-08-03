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
	CheckinEnabled bool
	CheckinCron    string

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
	TrustedProxyCIDRs             []string
	RelayRatePerMinute            int
	RelayRateBurst                int
	RelayModelRatePerMinute       int
	RelayModelRateBurst           int
	// ChannelAutoDisableThreshold: consecutive member failures before a channel
	// is auto-disabled. 0 disables the feature.
	ChannelAutoDisableThreshold int
	// RoutingLatencyAware enables latency-weighted channel selection.
	RoutingLatencyAware     bool
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
		TrustedProxyCIDRs:             trustedProxies,
		RelayRatePerMinute:            relayRate, RelayRateBurst: relayBurst,
		RelayModelRatePerMinute: relayModelRate, RelayModelRateBurst: relayModelBurst,
		ChannelAutoDisableThreshold: autoDisableThreshold,
		RoutingLatencyAware:         latencyAware,
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
