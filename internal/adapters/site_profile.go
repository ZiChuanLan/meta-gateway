// Site capability profiles, derived from the All API Hub account-site
// definition table: which site families support check-in / account APIs,
// whether they force access-token auth, and whether created keys follow a
// one-time-secret flow. Model discovery and relay stay OpenAI-compatible for
// every family; these flags only gate the account/check-in surfaces.
package adapters

// SiteBackendFamily mirrors AAH's ACCOUNT_SITE_ADAPTER_FAMILIES.
type SiteBackendFamily string

const (
	FamilyNewAPI      SiteBackendFamily = "new-api"
	FamilySub2API     SiteBackendFamily = "sub2api"
	FamilyAIHubMix    SiteBackendFamily = "aihubmix"
	FamilySharedChat  SiteBackendFamily = "sharedchat"
	FamilyUnsupported SiteBackendFamily = "unsupported"
)

// SiteProfile describes the account/check-in capabilities of a site family.
type SiteProfile struct {
	Family SiteBackendFamily
	// Checkin: whether the site exposes a New-API-style /api/user/checkin.
	Checkin bool
	// ForceToken: access-token auth is required (cookie auth unsupported).
	ForceToken bool
	// OneTimeKey: created keys are shown once (AIHubMix-style) and the secret
	// may be masked afterwards — operators should copy it immediately.
	OneTimeKey bool
	// UsagePath / CheckinPath / RedeemPath: web UI paths (informational).
	UsagePath   string
	CheckinPath string
	RedeemPath  string
}

// siteProfiles mirrors AAH's ACCOUNT_SITE_DEFINITIONS (subset that matters to
// a server-side gateway: check-in support, forced token auth, one-time keys).
var siteProfiles = map[string]SiteProfile{
	"new-api": {
		Family: FamilyNewAPI, Checkin: true,
		UsagePath: "/console/log", CheckinPath: "/console/personal",
	},
	"one-api": {
		Family: FamilyNewAPI, Checkin: true,
		UsagePath: "/console/log",
	},
	"anyrouter": {
		Family: FamilyNewAPI, Checkin: true,
		CheckinPath: "/console/topup",
	},
	"veloera": {
		Family: FamilyNewAPI, Checkin: true,
		UsagePath: "/app/logs/api-usage", CheckinPath: "/app/me", RedeemPath: "/app/wallet",
	},
	"done-hub": {
		Family: FamilyNewAPI, Checkin: true,
		UsagePath: "/panel/log", RedeemPath: "/panel/topup",
	},
	"one-hub": {
		Family: FamilyNewAPI, Checkin: true,
		UsagePath: "/panel/log", RedeemPath: "/panel/topup",
	},
	"v-api": {
		Family: FamilyNewAPI, Checkin: true,
		UsagePath: "/panel/log", CheckinPath: "/panel/profile", RedeemPath: "/panel/topup",
	},
	"voapi": {
		Family: FamilyNewAPI, Checkin: true,
		UsagePath: "/console/log", RedeemPath: "/wallet",
	},
	"super-api": {Family: FamilyNewAPI, Checkin: true},
	"rix-api": {
		Family: FamilyNewAPI, Checkin: true,
		UsagePath: "/log", CheckinPath: "/panel", RedeemPath: "/topup",
	},
	"neo-api": {Family: FamilyNewAPI, Checkin: true},
	"wong-gongyi": {
		Family: FamilyNewAPI, Checkin: true,
		CheckinPath: "/console/topup",
	},
	"sub2api": {
		Family: FamilySub2API, Checkin: false, ForceToken: true,
		UsagePath: "/usage", RedeemPath: "/redeem",
	},
	"aihubmix": {
		Family: FamilyAIHubMix, Checkin: false, ForceToken: true, OneTimeKey: true,
		UsagePath: "/statistics", CheckinPath: "/", RedeemPath: "/topup",
	},
	"sharedchat": {
		Family: FamilySharedChat, Checkin: false,
	},
	// Families with no server-side account adapter yet.
	"octopus":         {Family: FamilyUnsupported},
	"axonhub":         {Family: FamilyUnsupported},
	"claude-code-hub": {Family: FamilyUnsupported},
	"unknown":         {Family: FamilyNewAPI, Checkin: true},
}

// SiteProfileFor returns the capability profile for a site/channel family.
// Unknown families default to the New-API profile (historical behavior).
func SiteProfileFor(typeHint, platform string) SiteProfile {
	key := canonical(firstNonEmpty(typeHint, platform))
	if profile, ok := siteProfiles[key]; ok {
		return profile
	}
	// Known OpenAI-compatible relay brands with no explicit entry keep the
	// New-API family defaults.
	if key == "openai-compatible" || key == "openai" || key == "openaicompat" {
		return SiteProfile{Family: FamilyNewAPI}
	}
	return siteProfiles["unknown"]
}

// CheckinSupported reports whether the site family offers check-in.
func CheckinSupported(typeHint, platform string) bool {
	return SiteProfileFor(typeHint, platform).Checkin
}

// AccountSupported reports whether the site family has a server-side account
// adapter (token creation / sync / probe).
func AccountSupported(typeHint, platform string) bool {
	return SiteProfileFor(typeHint, platform).Family != FamilyUnsupported
}
