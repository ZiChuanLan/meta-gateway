package domain

import (
	"strconv"
	"strings"
	"time"
)

// The call policy: how a site wants automated traffic to look.
//
// It exists because "do not probe us" and "do not call us" are different
// requests, and a one-token `hi` is exactly the signature some sites ban. A
// policy therefore decides the *shape* of an automated call, never whether one
// happens.
//
// Untyped string constants, following ModelSyncModeAuto/Manual: the columns and
// the console payloads are plain strings, and a named type here would only buy
// casts at every boundary.
const (
	// CallPolicyAllowProbe: a minimal probe (a one-token "hi") is fine. This is
	// the default: it must not start spending real tokens on its own.
	CallPolicyAllowProbe = "allow_probe"
	// CallPolicyRealCallsOnly: the site bans probing, so every automated dialog
	// call is sent in the shape of a real small request — a normal token budget
	// and a real question — instead of a one-token probe.
	CallPolicyRealCallsOnly = "real_calls_only"
)

// NormalizeCallPolicy maps empty and unknown values onto allow_probe, the
// do-nothing-extra default. Mirrors NormalizeModelSyncMode: an unrecognised
// value from an older/newer row must never silently become the stricter or the
// more expensive behaviour by accident.
func NormalizeCallPolicy(raw string) string {
	switch strings.TrimSpace(raw) {
	case CallPolicyRealCallsOnly:
		return CallPolicyRealCallsOnly
	default:
		return CallPolicyAllowProbe
	}
}

// ResolveCallPolicy applies the inheritance chain channel > site > default.
func ResolveCallPolicy(channelPolicy, sitePolicy string) string {
	if strings.TrimSpace(channelPolicy) != "" {
		return NormalizeCallPolicy(channelPolicy)
	}
	return NormalizeCallPolicy(sitePolicy)
}

// CallForm is the shape one automated call takes.
const (
	// CallFormMinimal is the cheap probe: one short prompt, one token.
	CallFormMinimal = "minimal"
	// CallFormReal is a real small request: a normal token budget, a real
	// question from a pool, and a system message.
	CallFormReal = "real"
)

// CallPurpose names why an automated request is being sent. Every call site
// declares one, because the purpose is what makes a future "we only did X on
// this channel" question answerable from the logs, and what the form table
// below is keyed on.
const (
	// PurposeProbe is the scheduled or on-demand model probe.
	PurposeProbe = "probe"
	// PurposeKeepalive is the idle-driven keepalive call.
	PurposeKeepalive = "keepalive"
	// PurposeDiagnostic is an operator-composed test from the console. It is
	// already a real request by construction, so a policy never reshapes it.
	PurposeDiagnostic = "diagnostic"
)

// AllCallPurposes is the registry every call site must pick from. It exists so a
// test can assert the form table covers all of them: a new automated call path
// that forgets to declare itself fails the build rather than quietly probing a
// site that bans probing.
var AllCallPurposes = []string{PurposeProbe, PurposeKeepalive, PurposeDiagnostic}

// CallFormFor returns the shape a purpose must take under a policy.
//
// A site that bans probing upgrades every automated dialog call to the real
// form. An operator-composed diagnostic is already a real request — the human
// typed the prompt and the token budget — so it is returned unchanged.
func CallFormFor(policy, purpose string) string {
	if purpose == PurposeDiagnostic {
		return CallFormReal
	}
	if NormalizeCallPolicy(policy) == CallPolicyRealCallsOnly {
		return CallFormReal
	}
	return CallFormMinimal
}

// KeepaliveConfig is the resolved keepalive window for one channel.
//
// IdleDays is the ban window as the site declares it; SafetyMarginDays is how
// many days early the call fires, because arriving on the last day is arriving
// after the ban on any site that counts a day in its own timezone.
//
// The model to call is not here: it is resolved onto KeepaliveTarget.Model,
// which is the only place a caller reads it from.
type KeepaliveConfig struct {
	Enabled          bool   `json:"enabled"`
	IdleDays         int    `json:"idle_days"`
	SafetyMarginDays int    `json:"safety_margin_days"`
	Prompt           string `json:"prompt,omitempty"`
	MaxTokens        int    `json:"max_tokens,omitempty"`
	DailyCap         int    `json:"daily_cap,omitempty"`
	QuietHours       string `json:"quiet_hours,omitempty"`
}

// IdleSince is the moment the idle clock starts: the last real call, or the
// channel's creation when there has never been one.
func (t KeepaliveTarget) IdleSince(now time.Time) time.Time {
	if t.LastCallAt != nil {
		return *t.LastCallAt
	}
	if !t.CreatedAt.IsZero() {
		return t.CreatedAt
	}
	return now
}

// IdleDays is how long this account has gone without a call, which is what the
// site's ban counts.
func (t KeepaliveTarget) IdleDays(now time.Time) float64 {
	return now.Sub(t.IdleSince(now)).Hours() / 24
}

// InQuietHours reports whether now falls inside a "22:00-07:00" window, during
// which a keepalive is deferred.
//
// The comparison uses the time's own location, so "22:00" means 22:00 where the
// process runs. That is the honest reading of a setting an operator types next
// to their own clock, and it is stated in the console rather than guessed per
// site: a window is a property of the operator's night, not of the upstream.
// Empty, malformed or degenerate values never match, because a typo must not
// silently switch a keepalive off.
func InQuietHours(spec string, now time.Time) bool {
	start, end, ok := parseQuietHours(spec)
	if !ok || start == end {
		return false
	}
	minute := now.Hour()*60 + now.Minute()
	if start < end {
		return minute >= start && minute < end
	}
	// Wrapping midnight (22:00-07:00).
	return minute >= start || minute < end
}

// ValidQuietHours reports whether a quiet-hours spec is well formed AND can ever
// match. It is the console's validation and shares the parser with
// InQuietHours, so the two cannot disagree about what "valid" means.
//
// A degenerate window (00:00-00:00) parses but never matches; treating it as
// invalid is the difference between "rejected, fix your typo" and "saved,
// silently does nothing".
func ValidQuietHours(spec string) bool {
	if strings.TrimSpace(spec) == "" {
		return true
	}
	start, end, ok := parseQuietHours(spec)
	return ok && start != end
}

func parseQuietHours(spec string) (int, int, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, okStart := parseClock(parts[0])
	end, okEnd := parseClock(parts[1])
	if !okStart || !okEnd {
		return 0, 0, false
	}
	return start, end, true
}

func parseClock(raw string) (int, bool) {
	fields := strings.Split(strings.TrimSpace(raw), ":")
	if len(fields) != 2 {
		return 0, false
	}
	hour, errHour := strconv.Atoi(fields[0])
	minute, errMinute := strconv.Atoi(fields[1])
	if errHour != nil || errMinute != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// Ready reports whether this channel should be called now, given how long it has
// been idle. Zero idle days means "no window configured", which is never ready —
// a channel nobody gave a window to must not be pinged.
func (k KeepaliveConfig) Ready(idleDays float64) bool {
	if !k.Enabled || k.IdleDays <= 0 {
		return false
	}
	return idleDays >= float64(k.IdleDays-k.SafetyMarginDays)
}

// RemainingDays is how many days of the window are left at the given idle age.
// Negative means the window has already been exceeded.
func (k KeepaliveConfig) RemainingDays(idleDays float64) float64 {
	if k.IdleDays <= 0 {
		return 0
	}
	return float64(k.IdleDays) - idleDays
}

// TriggerReason is the human-readable "why this call happened" that goes into
// the log line and the console, e.g. "idle 19 days > threshold 13".
func (k KeepaliveConfig) TriggerReason(idleDays float64) string {
	return "idle " + strconv.FormatFloat(idleDays, 'f', 1, 64) + "d >= threshold " +
		strconv.Itoa(k.IdleDays-k.SafetyMarginDays) + "d (window " + strconv.Itoa(k.IdleDays) + "d)"
}

// KeepaliveEvent is one recorded keepalive call. It is the footprint a site
// administrator asks about ("you called us on the 3rd") and the counter the
// daily cap reads.
type KeepaliveEvent struct {
	ID           int64     `json:"id"`
	CredentialID int64     `json:"credential_id"`
	SiteID       int64     `json:"site_id"`
	SiteName     string    `json:"site_name"`
	ChannelID    int64     `json:"channel_id"`
	ChannelName  string    `json:"channel_name"`
	Model        string    `json:"model"`
	Form         string    `json:"form"`
	Reason       string    `json:"reason"`
	OK           bool      `json:"ok"`
	StatusCode   int       `json:"status_code"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// KeepaliveTarget is one credential's keepalive state, resolved from the
// channel > site > global chain.
//
// The counting unit is the credential — a ban counts calls to one account, and
// two channels sharing a key would otherwise be called twice — while the sending
// unit stays a single channel x model: the call goes straight to that channel,
// so routing has no say in whether an account can be kept alive.
type KeepaliveTarget struct {
	// CredentialID is plumbing (the ban counts per account, and two channels can
	// share one), not a fact an operator reads, so it stays out of the payload.
	CredentialID int64  `json:"-"`
	SiteID       int64  `json:"site_id"`
	SiteName     string `json:"site_name"`
	ChannelID    int64  `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	Model        string `json:"model"`
	// SiteModel is the site's own setting, empty when nothing is pinned: the
	// console shows it in the field, while Model shows what a call would send.
	SiteModel string          `json:"site_model,omitempty"`
	Policy    string          `json:"call_policy"`
	Config    KeepaliveConfig `json:"config"`
	// Candidates are the models this channel advertised when its list was last
	// fetched (models_csv). Empty means "nothing fetched yet", which is also why
	// the target has no model to call: the console offers them as the picklist
	// behind the site's keepalive model field.
	Candidates []string `json:"candidates,omitempty"`
	// LastCallAt is the newest real call across every channel of this
	// credential. nil means "never called" — which is not the same as "idle
	// forever", so it is reported rather than treated as day zero.
	LastCallAt *time.Time `json:"last_call_at,omitempty"`
	// CreatedAt is the channel's own age, the idle clock's floor when there has
	// never been a call: a channel added yesterday must not be called today just
	// because it has no history.
	CreatedAt time.Time `json:"-"`
	// SendsToday counts this credential's successful keepalive calls since UTC
	// midnight, for the daily cap.
	SendsToday int `json:"sends_today"`
	// SkipReason names why a target cannot be called at all (no model fetched and
	// none configured), so the console can show "cannot keepalive" instead of
	// leaving a silent gap.
	SkipReason string `json:"skip_reason,omitempty"`
}
