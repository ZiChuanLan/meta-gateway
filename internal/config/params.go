package config

import (
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EnvParam is one environment variable the process read at startup, together
// with the effective value it produced.
//
// The console renders these because an environment variable is otherwise
// invisible once the container is running: the only way to answer "why did my
// image request die at exactly 60s" or "is my .env value even in this container"
// was to read the deployment files and reason about compose interpolation. The
// value column is what the process actually uses; FromEnv says whether it came
// from the environment at all — a variable compose never passed looks exactly
// like a deliberate default, and that difference is the whole answer.
type EnvParam struct {
	Key string `json:"key"`
	// Kind is derived from the bound field: bool, int, duration, string, list.
	Kind string `json:"kind"`
	// Value is the effective value, already formatted for display. Secrets are
	// masked here — this payload must never carry a credential.
	Value string `json:"value"`
	// FromEnv is true when the variable was present and non-empty.
	FromEnv bool `json:"from_env"`
	// Secret marks values that are masked.
	Secret bool `json:"secret"`
}

// envParamSpec binds one environment variable to the Config field holding its
// effective value. There is no default column on purpose: the literal defaults
// live at the reader call sites in Load, and copying them here would create the
// second truth this repository keeps getting bitten by. docs/reference/environment.md
// is the generated table for "what is the default"; this one answers "what is
// running".
type envParamSpec struct {
	Key    string
	Field  string
	Secret bool
}

// envParamSpecs covers every variable Load reads. params_test.go parses
// config.go and fails when this table and the source disagree in either
// direction, so a new variable cannot ship without showing up in the console,
// and a removed one cannot linger here.
var envParamSpecs = []envParamSpec{
	{Key: "ADMIN_RATE_BURST", Field: "AdminRateBurst"},
	{Key: "ADMIN_RATE_PER_MINUTE", Field: "AdminRatePerMinute"},
	{Key: "ADMIN_TOKEN", Field: "AdminToken", Secret: true},
	{Key: "ADMIN_TOKENS", Field: "AdminTokens", Secret: true},
	{Key: "ADMIN_TOKEN_LOGIN", Field: "AdminTokenLogin"},
	{Key: "ADMIN_USERNAME", Field: "AdminUsername"},
	{Key: "ALERT_CONFIG_JSON", Field: "AlertConfigJSON", Secret: true},
	{Key: "ALERT_DAILY_SUMMARY_INTERVAL_SECONDS", Field: "AlertDailySummaryInterval"},
	{Key: "ALERT_SWEEP_INTERVAL_SECONDS", Field: "AlertSweepInterval"},
	{Key: "AUDIT_RETENTION_DAYS", Field: "AuditRetentionDays"},
	{Key: "AUDIT_RETENTION_ROWS", Field: "AuditRetentionRows"},
	{Key: "BACKUP_DIR", Field: "BackupDir"},
	{Key: "BACKUP_RETENTION_COUNT", Field: "BackupRetentionCount"},
	{Key: "BALANCE_HISTORY_RETENTION_DAYS", Field: "BalanceHistoryRetentionDays"},
	{Key: "CHANNEL_AUTO_DISABLE_THRESHOLD", Field: "ChannelAutoDisableThreshold"},
	{Key: "CHANNEL_RETRY_TIMES", Field: "ChannelRetryTimes"},
	{Key: "CHECKIN_CRON", Field: "CheckinCron"},
	{Key: "CHECKIN_ENABLED", Field: "CheckinEnabled"},
	{Key: "CHECKIN_TZ", Field: "CheckinTZ"},
	{Key: "COOLDOWN_SECONDS", Field: "Cooldown"},
	{Key: "CORS_ALLOWED_ORIGINS", Field: "CORSAllowedOrigins"},
	{Key: "CROSS_CHANNEL_FAILOVER_ENABLED", Field: "CrossChannelFailoverEnabled"},
	{Key: "DATA_DIR", Field: "DataDir"},
	{Key: "DECISION_SNAPSHOT_RETENTION_DAYS", Field: "DecisionSnapshotRetentionDays"},
	{Key: "EXCHANGE_ALLOW_SECRET_EXPORT", Field: "ExchangeAllowSecretExport"},
	{Key: "FAULT_PROTECTION_ENABLED", Field: "FaultProtectionEnabled"},
	{Key: "HEALTH_HISTORY_RETENTION_DAYS", Field: "HealthHistoryRetentionDays"},
	{Key: "HEALTH_SWEEP_CONCURRENCY", Field: "HealthSweepConcurrency"},
	{Key: "HEALTH_SWEEP_DEGRADED_MS", Field: "HealthSweepDegradedMs"},
	{Key: "HEALTH_SWEEP_ENABLED", Field: "HealthSweepEnabled"},
	{Key: "HEALTH_SWEEP_INTERVAL_SECONDS", Field: "HealthSweepIntervalSeconds"},
	{Key: "HEALTH_SWEEP_JITTER_SECONDS", Field: "HealthSweepJitterSeconds"},
	{Key: "HEALTH_SWEEP_TIMEOUT_SECONDS", Field: "HealthSweepTimeoutSeconds"},
	{Key: "HTTP_ADDR", Field: "HTTPAddr"},
	{Key: "MASTER_KEY", Field: "MasterKey", Secret: true},
	{Key: "MAX_ADMIN_BODY_BYTES", Field: "MaxAdminBodyBytes"},
	{Key: "MAX_HEADER_BYTES", Field: "MaxHeaderBytes"},
	{Key: "METRICS_TOKEN", Field: "MetricsToken", Secret: true},
	{Key: "MODEL_CATALOG_SOURCES", Field: "ModelCatalogSources"},
	{Key: "MODEL_CATALOG_SYNC_INTERVAL_HOURS", Field: "ModelCatalogInterval"},
	{Key: "MODEL_CATALOG_SYNC_PRICES", Field: "ModelCatalogSyncPrices"},
	{Key: "MODEL_CHANGE_AUTO_IGNORE_DAYS", Field: "ModelChangeAutoIgnoreDays"},
	{Key: "MODEL_CHANGE_RETENTION_DAYS", Field: "ModelChangeRetentionDays"},
	{Key: "OUTBOUND_ALLOW_CIDRS", Field: "OutboundAllowCIDRs"},
	{Key: "OUTBOUND_ALLOW_HOSTS", Field: "OutboundAllowHosts"},
	{Key: "OUTBOUND_CONNECT_TIMEOUT_SECONDS", Field: "OutboundConnectTimeout"},
	{Key: "OUTBOUND_HEADER_TIMEOUT_SECONDS", Field: "OutboundResponseHeaderTimeout"},
	{Key: "OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS", Field: "OutboundImageHeaderTimeout"},
	{Key: "OUTBOUND_MAX_IDLE_CONNS", Field: "OutboundMaxIdleConns"},
	{Key: "OUTBOUND_MAX_IDLE_CONNS_PER_HOST", Field: "OutboundMaxIdleConnsPerHost"},
	{Key: "OUTBOUND_TLS_TIMEOUT_SECONDS", Field: "OutboundTLSHandshakeTimeout"},
	{Key: "PLUGIN_CATALOG_URL", Field: "PluginCatalogURL"},
	{Key: "PLUGIN_MARKET_URLS", Field: "PluginMarketURLs"},
	{Key: "PLUGINS_DIR", Field: "PluginsDir"},
	{Key: "READINESS_TIMEOUT_SECONDS", Field: "ReadinessTimeout"},
	{Key: "RECOVERY_PROBE_ENABLED", Field: "RecoveryProbeEnabled"},
	{Key: "RECOVERY_PROBE_INTERVAL_SECONDS", Field: "RecoveryProbeIntervalSeconds"},
	{Key: "RELAY_MODEL_RATE_BURST", Field: "RelayModelRateBurst"},
	{Key: "RELAY_MODEL_RATE_PER_MINUTE", Field: "RelayModelRatePerMinute"},
	{Key: "RELAY_RATE_BURST", Field: "RelayRateBurst"},
	{Key: "RELAY_RATE_PER_MINUTE", Field: "RelayRatePerMinute"},
	{Key: "RETRY_TIMES", Field: "RetryTimes"},
	{Key: "ROUTING_CONCURRENCY_AWARE", Field: "RoutingConcurrencyEnabled"},
	{Key: "ROUTING_CONCURRENCY_LIMIT", Field: "RoutingConcurrencyLimit"},
	{Key: "ROUTING_ERROR_AWARE", Field: "RoutingErrorAware"},
	{Key: "ROUTING_LATENCY_AWARE", Field: "RoutingLatencyAware"},
	{Key: "SERVER_IDLE_TIMEOUT_SECONDS", Field: "ServerIdleTimeout"},
	{Key: "SERVER_READ_HEADER_TIMEOUT_SECONDS", Field: "ServerReadHeaderTimeout"},
	{Key: "SERVER_READ_TIMEOUT_SECONDS", Field: "ServerReadTimeout"},
	{Key: "SERVER_SHUTDOWN_TIMEOUT_SECONDS", Field: "ServerShutdownTimeout"},
	{Key: "SITE_PROBE_INTERVAL_SECONDS", Field: "SiteProbeIntervalSeconds"},
	{Key: "SITE_PROBE_JITTER_SECONDS", Field: "SiteProbeJitterSeconds"},
	{Key: "SITE_PROBE_RETENTION_DAYS", Field: "SiteProbeRetentionDays"},
	{Key: "SQLITE_MAX_OPEN_CONNS", Field: "SQLiteMaxOpenConns"},
	{Key: "STABLE_FIRST_DENOMINATOR", Field: "StableFirstDenominator"},
	{Key: "STABLE_FIRST_ENABLED", Field: "StableFirstEnabled"},
	{Key: "STABLE_FIRST_PROMOTE_REQUESTS", Field: "StableFirstPromoteRequests"},
	{Key: "STICKY_ENABLED", Field: "StickyEnabled"},
	{Key: "STICKY_TTL_MINUTES", Field: "StickyTTL"},
	{Key: "TRUSTED_PROXY_CIDRS", Field: "TrustedProxyCIDRs"},
	{Key: "TRUSTED_SCRAPER_CIDRS", Field: "TrustedScraperCIDRs"},
	{Key: "UPDATE_CHECK_ENABLED", Field: "UpdateCheckEnabled"},
	{Key: "WEBDAV_BACKUP_PASSWORD", Field: "WebDAVBackupPassword", Secret: true},
	{Key: "WEBDAV_CRON", Field: "WebDAVCron"},
	{Key: "WEBDAV_MAX_BYTES", Field: "WebDAVMaxBytes"},
	{Key: "WEBDAV_PASSWORD", Field: "WebDAVPassword", Secret: true},
	{Key: "WEBDAV_SYNC_ENABLED", Field: "WebDAVSyncEnabled"},
	{Key: "WEBDAV_UPLOAD_BACKUP_PASSWORD", Field: "WebDAVUploadBackupPassword", Secret: true},
	{Key: "WEBDAV_UPLOAD_ENABLED", Field: "WebDAVUploadEnabled"},
	{Key: "WEBDAV_UPLOAD_PASSWORD", Field: "WebDAVUploadPassword", Secret: true},
	{Key: "WEBDAV_UPLOAD_URL", Field: "WebDAVUploadURL"},
	{Key: "WEBDAV_UPLOAD_USERNAME", Field: "WebDAVUploadUsername"},
	{Key: "WEBDAV_URL", Field: "WebDAVURL"},
	{Key: "WEBDAV_USERNAME", Field: "WebDAVUsername"},
	{Key: "WEBHOOK_THROTTLE_SECONDS", Field: "WebhookThrottleSeconds"},
	{Key: "WEBHOOK_URL", Field: "WebhookURL", Secret: true},
}

var (
	durationType = reflect.TypeOf(time.Duration(0))
	// userinfoPattern finds "scheme://user:password@host" so a credential
	// embedded in a URL is masked while the host stays readable.
	userinfoPattern = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@]*@`)
)

// DeploymentParameters returns every environment variable the process read,
// with the value it is actually running with.
func (c *Config) DeploymentParameters() []EnvParam {
	if c == nil {
		return nil
	}
	value := reflect.ValueOf(c).Elem()
	params := make([]EnvParam, 0, len(envParamSpecs))
	for _, spec := range envParamSpecs {
		field := value.FieldByName(spec.Field)
		if !field.IsValid() {
			continue
		}
		params = append(params, EnvParam{
			Key:     spec.Key,
			Kind:    fieldKind(field),
			Value:   renderParamValue(field, spec.Secret),
			FromEnv: envParamPresent(spec.Key),
			Secret:  spec.Secret,
		})
	}
	return params
}

// envParamPresent mirrors what the readers treat as "set": a variable that is
// absent, or present but blank, falls back to its default.
func envParamPresent(key string) bool {
	value, ok := os.LookupEnv(key)
	return ok && strings.TrimSpace(value) != ""
}

func fieldKind(field reflect.Value) string {
	switch {
	case field.Type() == durationType:
		return "duration"
	case field.Kind() == reflect.Bool:
		return "bool"
	case field.Kind() == reflect.String:
		return "string"
	case field.Kind() == reflect.Slice:
		return "list"
	default:
		return "int"
	}
}

func renderParamValue(field reflect.Value, secret bool) string {
	switch {
	case field.Type() == durationType:
		return field.Interface().(time.Duration).String()
	case field.Kind() == reflect.String:
		value := field.String()
		if secret {
			return maskParam(value)
		}
		return redactURLUserinfo(value)
	case field.Kind() == reflect.Bool:
		return strconv.FormatBool(field.Bool())
	case field.Kind() == reflect.Slice:
		values := make([]string, 0, field.Len())
		for i := 0; i < field.Len(); i++ {
			values = append(values, field.Index(i).String())
		}
		joined := strings.Join(values, ", ")
		if secret {
			return maskParam(joined)
		}
		return joined
	default:
		return strconv.FormatInt(field.Int(), 10)
	}
}

// maskParam hides a credential without hiding whether one is configured.
func maskParam(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "••••••"
}

// redactURLUserinfo masks user:password inside a URL.
func redactURLUserinfo(value string) string {
	return userinfoPattern.ReplaceAllString(value, "${1}••••••@")
}
