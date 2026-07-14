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
	ID        int64     `json:"id"`
	TokenHash string    `json:"-"` // never serialized
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Scopes    string    `json:"scopes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ---------------------------------------------------------------------------
// ProxyLog
// ---------------------------------------------------------------------------

// ProxyLog records a relayed request (without secrets).
type ProxyLog struct {
	ID         int64     `json:"id"`
	RequestID  string    `json:"request_id"`
	ChannelID  int64     `json:"channel_id"`
	Model      string    `json:"model"`
	Status     int       `json:"status"`
	LatencyMs  int       `json:"latency_ms"`
	Attempt    int       `json:"attempt"`
	ErrorBrief string    `json:"error_brief,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// RoutingCandidate contains the persisted facts needed to evaluate one member.
type RoutingCandidate struct {
	Member           RouteMember `json:"member"`
	Channel          Channel     `json:"channel"`
	CredentialUsable bool        `json:"credential_usable"`
}
