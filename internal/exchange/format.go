// Package exchange owns portable channel exchange formats and orchestration.
package exchange

import "time"

const (
	Format       = "meta-gateway-aah-exchange"
	Version      = 1
	MaxBodyBytes = 10 << 20
	maxItems     = 5000
)

type Envelope struct {
	Format     string           `json:"format"`
	Version    int              `json:"version"`
	ExportedAt time.Time        `json:"exported_at"`
	Importable bool             `json:"importable"`
	Items      []Item           `json:"items"`
	Skipped    []SkippedChannel `json:"skipped,omitempty"`
}

type SkippedChannel struct {
	ChannelID int64  `json:"channel_id"`
	Name      string `json:"name"`
	Reason    string `json:"reason"`
}

type Item struct {
	Name         string   `json:"name"`
	BaseURL      string   `json:"base_url"`
	APIKey       string   `json:"api_key,omitempty"`
	Models       []string `json:"models"`
	Group        string   `json:"group"`
	Priority     int      `json:"priority"`
	Weight       int      `json:"weight"`
	SiteTypeHint string   `json:"site_type_hint"`
	Status       string   `json:"-"`
	// CredentialKind is stored on credentials.kind (api_key | access_token | session | ...).
	CredentialKind string `json:"-"`
	// MetaJSON is stored on credentials.meta_json (e.g. platform_user_id for New API check-in).
	MetaJSON string `json:"-"`
	// CheckinEnabled is stored on credentials.checkin_enabled.
	CheckinEnabled bool `json:"-"`
}

type ErrorKind string

const (
	ErrorValidation  ErrorKind = "validation_error"
	ErrorUnsupported ErrorKind = "unsupported_format"
	ErrorConflict    ErrorKind = "identity_conflict"
	ErrorNotFound    ErrorKind = "channel_not_found"
	ErrorInternal    ErrorKind = "internal_error"
)

type Error struct {
	Kind ErrorKind
}

func (e *Error) Error() string { return string(e.Kind) }

func formatError(kind ErrorKind) error { return &Error{Kind: kind} }
