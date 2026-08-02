package domain

import "time"

// Entity status values.
const (
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
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
	ID                int64     `json:"id"`
	SiteID            int64     `json:"site_id"`
	Kind              string    `json:"kind"` // api_key | session | access_token | password
	SecretEnc         []byte    `json:"-"`    // never serialized in JSON
	MetaJSON          string    `json:"meta_json,omitempty"`
	Status            string    `json:"status"`
	CheckinEnabled    bool      `json:"checkin_enabled"`
	ImportFingerprint string    `json:"-"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
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
	ID           int64     `json:"id"`
	SiteID       *int64    `json:"site_id,omitempty"`
	CredentialID *int64    `json:"credential_id,omitempty"`
	Name         string    `json:"name"`
	BaseURL      string    `json:"base_url"`
	ModelsCSV    string    `json:"models_csv"`
	GroupName    string    `json:"group_name"`
	Priority     int       `json:"priority"`
	Weight       int       `json:"weight"`
	Status       string    `json:"status"`
	TypeHint     string    `json:"type_hint,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ChannelOverview combines channel configuration with discovery and routing health.
type ChannelOverview struct {
	Channel            Channel    `json:"channel"`
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
	LastProbeError     string     `json:"last_probe_error,omitempty"`
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
// Route
// ---------------------------------------------------------------------------

// Route maps a model pattern to channels.
type Route struct {
	ID           int64     `json:"id"`
	ModelPattern string    `json:"model_pattern"`
	Enabled      bool      `json:"enabled"`
	MappingJSON  string    `json:"mapping_json,omitempty"`
	Notes        string    `json:"notes,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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
	// ModelAllowlist, when non-empty, restricts this key to the listed models.
	// ModelDenylist blocks the listed models even if they are allowlisted.
	// Both are comma-separated model names.
	ModelAllowlist string    `json:"model_allowlist,omitempty"`
	ModelDenylist  string    `json:"model_denylist,omitempty"`
	// ExpiresAt is an RFC3339 timestamp; empty means the key never expires.
	ExpiresAt string `json:"expires_at,omitempty"`
	// AllowedIPs is a newline-separated list of IPs/CIDRs; empty means any source.
	AllowedIPs string `json:"allowed_ips,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ---------------------------------------------------------------------------
// ProxyLog
// ---------------------------------------------------------------------------

// ProxyLog records a relayed request (without secrets).
type ProxyLog struct {
	ID               int64     `json:"id"`
	RequestID        string    `json:"request_id"`
	ChannelID        int64     `json:"channel_id"`
	RouteID          int64     `json:"route_id,omitempty"`
	RoutePattern     string    `json:"route_pattern,omitempty"`
	Model            string    `json:"model"`
	Status           int       `json:"status"`
	LatencyMs        int       `json:"latency_ms"`
	Attempt          int       `json:"attempt"`
	ErrorBrief       string    `json:"error_brief,omitempty"`
	DownstreamKeyID  int64     `json:"downstream_key_id,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	TotalTokens      int       `json:"total_tokens,omitempty"`
	Stream           bool      `json:"stream,omitempty"`
	Path             string    `json:"path,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// UsageRecord is one metered relay completion used for billing summaries.
type UsageRecord struct {
	ID               int64     `json:"id"`
	RequestID        string    `json:"request_id"`
	DownstreamKeyID  int64     `json:"downstream_key_id"`
	ChannelID        int64     `json:"channel_id"`
	Model            string    `json:"model"`
	Path             string    `json:"path"`
	Stream           bool      `json:"stream"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	Status           int       `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
}

// UsageSummary aggregates metered traffic for Admin views.
type UsageSummary struct {
	RequestCount     int64   `json:"request_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCost    float64 `json:"estimated_cost"`
}

// RoutingCandidate contains the persisted facts needed to evaluate one member.
type RoutingCandidate struct {
	Member           RouteMember `json:"member"`
	Channel          Channel     `json:"channel"`
	CredentialUsable bool        `json:"credential_usable"`
}

// RouteOverview is the admin-facing route matrix with enriched channel members.
type RouteOverview struct {
	Route   Route              `json:"route"`
	Members []RoutingCandidate `json:"members"`
}
