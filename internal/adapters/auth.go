package adapters

// AuthMode identifies the credential transport used for account-facing
// upstream requests. Empty values keep the historical Bearer behavior.
type AuthMode string

const (
	AuthAccessToken AuthMode = "access_token"
	AuthCookie      AuthMode = "cookie"
	// AuthAuto prefers the access token and falls back to Cookie for safe
	// idempotent account GET requests when the upstream rejects the token.
	AuthAuto AuthMode = "auto"
)

func normalizeAuthMode(mode AuthMode) AuthMode {
	switch mode {
	case AuthCookie:
		return AuthCookie
	case AuthAuto:
		return AuthAuto
	default:
		return AuthAccessToken
	}
}
