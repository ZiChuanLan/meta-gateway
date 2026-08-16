package domain

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Entity status values.
const (
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
	// StatusAutoDisabled marks a channel that hit the consecutive-failure
	// threshold; it is excluded from routing until manually or probe-recovered.
	StatusAutoDisabled = "auto_disabled"
)

// Channel health is the business/service dimension. It must not be used as a
// synonym for network reachability: a reachable upstream can still have a
// failed model probe or degraded route members.
const (
	HealthStateDisabled  = "disabled"
	HealthStateUnhealthy = "unhealthy"
	HealthStateDegraded  = "degraded"
	HealthStateHealthy   = "healthy"
	HealthStateUnknown   = "unknown"
)

// Connectivity is the network-layer dimension recorded by the explicit Ping
// check. Unknown means that no Ping result has been recorded yet.
const (
	ConnectivityStateUnknown     = "unknown"
	ConnectivityStateReachable   = "reachable"
	ConnectivityStateUnreachable = "unreachable"
)

// ---------------------------------------------------------------------------
// Site
// ---------------------------------------------------------------------------

// Site represents an upstream API provider.
type Site struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	BaseURL   string    `json:"base_url"`
	Platform  string    `json:"platform"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Credential
// ---------------------------------------------------------------------------

// Credential holds encrypted secrets for a site.
type Credential struct {
	ID                int64  `json:"id"`
	SiteID            int64  `json:"site_id"`
	Kind              string `json:"kind"` // api_key | session | access_token | password
	SecretEnc         []byte `json:"-"`    // never serialized in JSON
	MetaJSON          string `json:"meta_json,omitempty"`
	Status            string `json:"status"`
	CheckinEnabled    bool   `json:"checkin_enabled"`
	ImportFingerprint string `json:"-"`
	// ModelsCSV is the per-key model allowlist (comma-separated, "*" suffix
	// wildcards). Empty = the key serves every model on its channel.
	ModelsCSV string    `json:"models_csv,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CheckinLog struct {
	ID           int64     `json:"id"`
	SiteID       int64     `json:"site_id"`
	CredentialID int64     `json:"credential_id"`
	Source       string    `json:"source"`
	Status       string    `json:"status"`
	Category     string    `json:"category"`
	Message      string    `json:"message"`
	Reward       string    `json:"reward,omitempty"`
	LatencyMs    int       `json:"latency_ms"`
	RanAt        time.Time `json:"ran_at"`
}

// ---------------------------------------------------------------------------
// Channel
// ---------------------------------------------------------------------------

// Channel represents a relay target (upstream API endpoint + model group).
type Channel struct {
	ID           int64  `json:"id"`
	SiteID       *int64 `json:"site_id,omitempty"`
	CredentialID *int64 `json:"credential_id,omitempty"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	ModelsCSV    string `json:"models_csv"`
	GroupName    string `json:"group_name"`
	Priority     int    `json:"priority"`
	Weight       int    `json:"weight"`
	Status       string `json:"status"`
	TypeHint     string `json:"type_hint,omitempty"`
	// MaxReasoningEffort is the highest reasoning_effort this channel's
	// upstream accepts (none/minimal/low/medium/high/xhigh/max). Empty =
	// passthrough. Requests asking for more are downgraded at forward time.
	MaxReasoningEffort string `json:"max_reasoning_effort,omitempty"`
	// PayloadRules is a JSON array of body-rewrite rules (match conditions +
	// set/delete/filter actions) applied before forwarding. Empty = passthrough.
	PayloadRules string `json:"payload_rules,omitempty"`
	// MaxConcurrent is the hard per-channel concurrency ceiling (FIFO wait
	// queue at the proxy; 0 = unlimited).
	MaxConcurrent int `json:"max_concurrent,omitempty"`
	// ProxyURL is the per-channel outbound HTTP(S) proxy; empty inherits the
	// global proxy (runtime setting proxy_url).
	ProxyURL string `json:"proxy_url,omitempty"`
	// HeaderOverride is a JSON object of extra upstream request headers
	// (merged after the adapter's auth headers; hop-by-hop names rejected).
	HeaderOverride string `json:"header_override,omitempty"`
	// SystemPrompt is injected as a system message ahead of user messages.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// RetryConfig is a JSON-encoded RetryConfig (per-channel retryable status
	// codes and error-text patterns). Empty string = global defaults only.
	RetryConfig string `json:"retry_config,omitempty"`
	// Tags is a comma-separated free-form tag list for bulk operations
	// (priority/weight/status updates by tag).
	Tags string `json:"tags,omitempty"`
	// StableFirst marks the channel as a grayscale candidate: it receives a
	// small 1/N fraction of traffic until it earns promotion.
	StableFirst bool `json:"stable_first,omitempty"`
	// StableFirstRequests counts successful relay attempts since the channel
	// was marked grayscale (promotion input).
	StableFirstRequests int `json:"stable_first_requests,omitempty"`
	// ConsecutiveFailures counts failed relay attempts (auto-disable input).
	ConsecutiveFailures int       `json:"-"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// RetryableErrorPattern matches an upstream error message (substring or regex).
type RetryableErrorPattern struct {
	Pattern string `json:"pattern"`
	Regex   bool   `json:"regex,omitempty"`
}

// ChannelPatch is a partial channel update applied to every channel with a
// given tag (bulk operations). Nil fields are left untouched.
type ChannelPatch struct {
	Priority       *int    `json:"priority"`
	Weight         *int    `json:"weight"`
	Status         *string `json:"status"`
	ModelsCSV      *string `json:"models_csv"`
	GroupName      *string `json:"group_name"`
	RetryConfig    *string `json:"retry_config"`
	SystemPrompt   *string `json:"system_prompt"`
	HeaderOverride *string `json:"header_override"`
}

// RetryConfig is the per-channel retry policy (mirrors AxonHub retry.go).
// The global default set (429 + 5xx) always applies; these are additive.
type RetryConfig struct {
	StatusCodes   []int                   `json:"status_codes,omitempty"`
	ErrorPatterns []RetryableErrorPattern `json:"error_patterns,omitempty"`
	// compiled holds pre-compiled regexes aligned with ErrorPatterns entries
	// whose Regex flag is set (nil for substring entries or failed compiles).
	// Unexported so JSON round-trips and equality comparisons are unaffected;
	// populated by ParseRetryConfig so the hot path never recompiles regexes.
	compiled []*regexp.Regexp
}

// CompiledPatterns returns the pre-compiled regexes aligned with
// ErrorPatterns (nil entries for substring patterns or compile failures).
// Non-nil only after ParseRetryConfig populated them.
func (c RetryConfig) CompiledPatterns() []*regexp.Regexp {
	return c.compiled
}

// ParseRetryConfig decodes a channel's retry_config JSON. Malformed or empty
// input yields an empty config (global defaults only). Regex patterns are
// compiled once here; invalid patterns are stored as nil and skipped at match
// time, matching the old per-call behavior (a compile error never matched).
func ParseRetryConfig(raw string) RetryConfig {
	var cfg RetryConfig
	if strings.TrimSpace(raw) == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return RetryConfig{}
	}
	cfg.compiled = make([]*regexp.Regexp, len(cfg.ErrorPatterns))
	for i, p := range cfg.ErrorPatterns {
		if !p.Regex || p.Pattern == "" {
			continue
		}
		if re, err := regexp.Compile(p.Pattern); err == nil {
			cfg.compiled[i] = re
		}
	}
	return cfg
}

// ChannelOverview combines channel configuration with discovery and routing health.
type ChannelOverview struct {
	Channel      Channel `json:"channel"`
	SitePlatform string  `json:"site_platform,omitempty"`
	// CheckinSupported / AccountSupported come from the site family profile
	// (AAH-derived capability table), filled by the admin API layer.
	CheckinSupported   bool       `json:"checkin_supported"`
	AccountSupported   bool       `json:"account_supported"`
	CredentialKind     string     `json:"credential_kind,omitempty"`
	CheckinEnabled     bool       `json:"checkin_enabled"`
	HasUserCredential  bool       `json:"has_user_credential"`
	HasPlatformUserID  bool       `json:"has_platform_user_id"`
	HasAPIKey          bool       `json:"has_api_key"`
	SiteUsable         bool       `json:"site_usable"`
	CredentialUsable   bool       `json:"credential_usable"`
	ModelCount         int        `json:"model_count"`
	LastCheckedAt      *time.Time `json:"last_checked_at,omitempty"`
	LastLatencyMs      int        `json:"last_latency_ms"`
	DiscoverySource    string     `json:"discovery_source,omitempty"`
	RouteCount         int        `json:"route_count"`
	EnabledMemberCount int        `json:"enabled_member_count"`
	CoolingMemberCount int        `json:"cooling_member_count"`
	FailureCount       int        `json:"failure_count"`
	LastError          string     `json:"last_error,omitempty"`
	LastProbeAt        *time.Time `json:"last_probe_at,omitempty"`
	LastProbeOK        bool       `json:"last_probe_ok"`
	// LastProbeError stores a redacted failure category or a non-error verdict
	// such as probe_slow when the health sweep grades latency.
	LastProbeError string `json:"last_probe_error,omitempty"`
	// LastPing* record the most recent connectivity ping (network-layer
	// reachability, separate from model/auth probing).
	LastPingAt    *time.Time `json:"last_ping_at,omitempty"`
	LastPingOK    bool       `json:"last_ping_ok"`
	LastPingError string     `json:"last_ping_error,omitempty"`
	LastPingMs    int        `json:"last_ping_ms"`
	// HealthState is the derived five-state health machine (Metapi-inspired):
	// disabled / unhealthy / degraded / healthy / unknown.
	HealthState       string `json:"health_state,omitempty"`
	HealthReason      string `json:"health_reason,omitempty"`
	ConnectivityState string `json:"connectivity_state"`
}

// DiscoveredModel is one model observed during a successful channel refresh.
type DiscoveredModel struct {
	ID        int64     `json:"id"`
	ChannelID int64     `json:"channel_id"`
	ModelName string    `json:"model_name"`
	Available bool      `json:"available"`
	Source    string    `json:"source"`
	LatencyMs int       `json:"latency_ms"`
	CheckedAt time.Time `json:"checked_at"`
}

// ---------------------------------------------------------------------------
// Model metadata
// ---------------------------------------------------------------------------

// ModelMetadata annotates a canonical model name with capability information
// (context window, modalities, thinking support, vendor). Empty fields mean
// unknown; SupportsThinking -1 = unknown, 0 = no, 1 = yes.
type ModelMetadata struct {
	ID               int64  `json:"id"`
	ModelName        string `json:"model_name"`
	ContextWindow    int64  `json:"context_window"`
	InputModalities  string `json:"input_modalities"`
	OutputModalities string `json:"output_modalities"`
	SupportsThinking int    `json:"supports_thinking"`
	Vendor           string `json:"vendor"`
	Notes            string `json:"notes"`
	UpdatedAt        string `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Route
// ---------------------------------------------------------------------------

// RoutingMode values for Route.RoutingMode. The empty string is treated as
// RoutingModeAuto for compatibility with older clients.
const (
	RoutingModeAuto     = "auto"
	RoutingModeLatency  = "latency"
	RoutingModeWeighted = "weighted"
	RoutingModeAdaptive = "adaptive"
	// RoutingModeSingle pins the route to one member (SingleMemberID): every
	// other member is skipped and cross-channel retry rounds count as 0.
	RoutingModeSingle = "single"
)

// NormalizeRoutingMode maps an empty or unknown-format mode to RoutingModeAuto.
func NormalizeRoutingMode(mode string) string {
	if mode == "" {
		return RoutingModeAuto
	}
	return mode
}

// Route maps a model pattern to channels.
type Route struct {
	ID           int64  `json:"id"`
	ModelPattern string `json:"model_pattern"`
	Enabled      bool   `json:"enabled"`
	RoutingMode  string `json:"routing_mode"`
	MappingJSON  string `json:"mapping_json,omitempty"`
	Notes        string `json:"notes,omitempty"`
	// SingleMemberID is the route_members.id pinned by RoutingModeSingle.
	// nil = not pinned (mode single without a live pin behaves as auto).
	SingleMemberID *int64 `json:"single_member_id,omitempty"`
	// RetryTimes overrides the global retry rounds (RETRY_TIMES) for this
	// model. nil = follow the global setting; 0 = no cross-channel retry.
	RetryTimes *int `json:"retry_times,omitempty"`
	// ChannelRetryTimes overrides the global same-key re-send count
	// (CHANNEL_RETRY_TIMES) for this model. nil = follow the global setting.
	ChannelRetryTimes *int      `json:"channel_retry_times,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// RouteMember
// ---------------------------------------------------------------------------

// RouteMember binds a channel to a route with priority/weight.
type RouteMember struct {
	ID             int64      `json:"id"`
	RouteID        int64      `json:"route_id"`
	ChannelID      int64      `json:"channel_id"`
	Priority       int        `json:"priority"`
	Weight         int        `json:"weight"`
	Enabled        bool       `json:"enabled"`
	Auto           bool       `json:"auto"`
	ManualOverride bool       `json:"manual_override"`
	FailCount      int        `json:"fail_count"`
	CooldownUntil  *time.Time `json:"cooldown_until,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// DownstreamKey
// ---------------------------------------------------------------------------

// DownstreamKey authenticates downstream clients.
type DownstreamKey struct {
	ID        int64  `json:"id"`
	TokenHash string `json:"-"` // never serialized
	// TokenEnc is the MASTER_KEY-encrypted plaintext token, kept so operators
	// can re-view/copy a key after creation. Empty for keys created before
	// plaintext storage existed. Never serialized in JSON.
	TokenEnc []byte `json:"-"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Scopes    string `json:"scopes,omitempty"`
	// QuotaTotalTokens is 0 for unlimited. When >0, relay is blocked once used >= total.
	QuotaTotalTokens int64 `json:"quota_total_tokens"`
	// QuotaUsedTokens is the cumulative total tokens charged to this key.
	QuotaUsedTokens int64 `json:"quota_used_tokens"`
	// Optional display prices (currency-agnostic units per 1k tokens).
	PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
	PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
	// PriceCachePer1k is the unit price for cache-read tokens. 0 = fall back
	// to the prompt price (cache-read is billed as prompt).
	PriceCachePer1k float64 `json:"price_cache_per_1k"`
	// ModelAllowlist, when non-empty, restricts this key to the listed models.
	// ModelDenylist blocks the listed models even if they are allowlisted.
	// Both are comma-separated model names.
	ModelAllowlist string `json:"model_allowlist,omitempty"`
	ModelDenylist  string `json:"model_denylist,omitempty"`
	// ExpiresAt is an RFC3339 timestamp; empty means the key never expires.
	ExpiresAt string `json:"expires_at,omitempty"`
	// AllowedIPs is a newline-separated list of IPs/CIDRs; empty means any source.
	AllowedIPs string `json:"allowed_ips,omitempty"`
	// GroupName is the multi-tenant group this key belongs to ("default" when
	// unset). Group quotas/rate limits apply on top of the key's own limits.
	GroupName string    `json:"group_name"`
	CreatedAt time.Time `json:"created_at"`
}

// KeyGroup is a multi-tenant token group with its own quota and rate limits.
// QuotaTotalTokens 0 = unlimited; RatePerMinute 0 = no group-level limiting.
type KeyGroup struct {
	Name             string    `json:"name"`
	QuotaTotalTokens int64     `json:"quota_total_tokens"`
	QuotaUsedTokens  int64     `json:"quota_used_tokens"`
	RatePerMinute    int       `json:"rate_per_minute"`
	RateBurst        int       `json:"rate_burst"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// ProxyLog
// ---------------------------------------------------------------------------

// ProxyLog records a relayed request (without secrets).
type ProxyLog struct {
	ID               int64  `json:"id"`
	RequestID        string `json:"request_id"`
	ChannelID        int64  `json:"channel_id"`
	RouteID          int64  `json:"route_id,omitempty"`
	RoutePattern     string `json:"route_pattern,omitempty"`
	Model            string `json:"model"`
	Status           int    `json:"status"`
	LatencyMs        int    `json:"latency_ms"`
	Attempt          int    `json:"attempt"`
	ErrorBrief       string `json:"error_brief,omitempty"`
	DownstreamKeyID  int64  `json:"downstream_key_id,omitempty"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	TotalTokens      int    `json:"total_tokens,omitempty"`
	// CacheReadTokens / CacheCreationTokens record upstream prompt-cache
	// accounting (Anthropic cache_read/creation, OpenAI cached_tokens,
	// Gemini cachedContentTokenCount).
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	// FirstByteMs is the time to the first streamed byte (0 for non-stream).
	FirstByteMs int `json:"first_byte_ms,omitempty"`
	// ClientFamily is the coarse client classification from the User-Agent.
	ClientFamily string `json:"client_family,omitempty"`
	// ReasoningEffort is the client-requested reasoning effort (OpenAI style,
	// e.g. low / medium / high / max / xhigh) when present in the request body.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// MappedReasoningEffort records a capability downgrade such as "max→high"
	// applied before forwarding (channel max_reasoning_effort). Empty = unchanged.
	MappedReasoningEffort string `json:"mapped_reasoning_effort,omitempty"`
	// TokensPerSecond is the derived stream throughput (completion tokens over
	// effective latency), AxonHub-style TPS metric.
	TokensPerSecond float64 `json:"tokens_per_second,omitempty"`
	Stream          bool    `json:"stream,omitempty"`
	Path            string  `json:"path,omitempty"`
	// KeyFingerprint is the sha256 prefix of the upstream API key that served
	// the attempt (never the plaintext). Empty when no key was used.
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	SessionKey     string `json:"session_key,omitempty"`
	// UpstreamRequestID is the upstream x-request-id header echoed by the
	// serving channel, enabling cross-referencing with the upstream's logs.
	UpstreamRequestID string    `json:"upstream_request_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// UsageRecord is one metered relay completion used for billing summaries.
type UsageRecord struct {
	ID                  int64  `json:"id"`
	RequestID           string `json:"request_id"`
	DownstreamKeyID     int64  `json:"downstream_key_id"`
	ChannelID           int64  `json:"channel_id"`
	Model               string `json:"model"`
	Path                string `json:"path"`
	Stream              bool   `json:"stream"`
	PromptTokens        int    `json:"prompt_tokens"`
	CompletionTokens    int    `json:"completion_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	Status              int    `json:"status"`
	// Cost is the persisted monetary amount for this relay (key unit prices ×
	// model ratio), computed at record time so bills never depend on later
	// price edits.
	Cost float64 `json:"cost"`
	// GroupName is the key's tenant group; used only to accrue group quota in
	// the same transaction (not persisted on the usage row itself).
	GroupName string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// UsageSummary aggregates metered traffic for Admin views.
type UsageSummary struct {
	RequestCount     int64   `json:"request_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Cost             float64 `json:"cost"`
}

// ModelRatio is the per-model billing markup (1.0 = no markup).
type ModelRatio struct {
	Model     string    `json:"model"`
	Ratio     float64   `json:"ratio"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RoutingCandidate contains the persisted facts needed to evaluate one member.
type RoutingCandidate struct {
	Member           RouteMember `json:"member"`
	Channel          Channel     `json:"channel"`
	CredentialUsable bool        `json:"credential_usable"`
	// ModelPattern is the route's model_pattern, used to scope per-model
	// adaptive scoring (latency/error EMA is tracked per channel × model).
	ModelPattern string `json:"model_pattern,omitempty"`
}

// RouteOverview is the admin-facing route matrix with enriched channel members.
type RouteOverview struct {
	Route   Route              `json:"route"`
	Members []RoutingCandidate `json:"members"`
}
