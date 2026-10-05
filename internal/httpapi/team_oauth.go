package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// Third-party sign-in (GitHub, Linux.do) for team members.
//
// The flow is the plain authorization-code one, with two details that matter:
//
//   - The state and the PKCE verifier ride in a signed HttpOnly cookie rather
//     than server memory, so a restart mid-flow does not strand the user and
//     the callback can prove the browser that started is the browser that
//     returned.
//   - The provider endpoints are configurable. The defaults are the official
//     ones; pointing them elsewhere is what makes the flow testable (and lets
//     an operator front Linux.do with their own proxy).

// oauthProviderDef is the built-in shape of a provider. Values an operator can
// change live in oauthProviderConfig; these are only the defaults.
type oauthProviderDef struct {
	id           string
	label        string
	authorizeURL string
	tokenURL     string
	userInfoURL  string
	// emailsURL is GitHub-only: /user does not expose the address, and its
	// verification state matters when an account is created automatically.
	emailsURL string
	scopes    string
	// tokenAuth is how the client authenticates at the token endpoint:
	// "basic" (Authorization header) or "post" (client_secret in the body).
	tokenAuth string
	// avatarField names the userinfo field holding the avatar URL.
	avatarField string
	// nameField names the field holding the display name.
	nameField string
}

func oauthProviderDefs() []oauthProviderDef {
	return []oauthProviderDef{
		{
			id:           "github",
			label:        "GitHub",
			authorizeURL: "https://github.com/login/oauth/authorize",
			tokenURL:     "https://github.com/login/oauth/access_token",
			userInfoURL:  "https://api.github.com/user",
			emailsURL:    "https://api.github.com/user/emails",
			scopes:       "read:user user:email",
			tokenAuth:    "post",
			avatarField:  "avatar_url",
			nameField:    "name",
		},
		{
			id:           "linuxdo",
			label:        "Linux.do",
			authorizeURL: "https://connect.linux.do/oauth2/authorize",
			tokenURL:     "https://connect.linux.do/oauth2/token",
			userInfoURL:  "https://connect.linux.do/api/user",
			scopes:       "read",
			tokenAuth:    "basic",
			avatarField:  "avatar_template",
			nameField:    "name",
		},
	}
}

func oauthProviderByID(id string) (oauthProviderDef, bool) {
	for _, def := range oauthProviderDefs() {
		if def.id == id {
			return def, true
		}
	}
	return oauthProviderDef{}, false
}

// oauthProviderConfig is the stored, operator-editable half of a provider.
// client_secret_enc is a crypto.Encrypter envelope and never leaves the server.
type oauthProviderConfig struct {
	Enabled         bool   `json:"enabled"`
	ClientID        string `json:"client_id"`
	ClientSecretEnc string `json:"client_secret_enc,omitempty"`
	AuthorizeURL    string `json:"authorize_url,omitempty"`
	TokenURL        string `json:"token_url,omitempty"`
	UserInfoURL     string `json:"userinfo_url,omitempty"`
	Scopes          string `json:"scopes,omitempty"`
}

// oauthConfig is team_settings.oauth_json.
type oauthConfig struct {
	// AutoRegister admits anyone the provider vouches for. Off means the
	// provider is only a way to sign in to an account that already exists.
	AutoRegister bool `json:"auto_register"`
	// DefaultPolicyID is the policy a self-registered account lands on.
	DefaultPolicyID int64                          `json:"default_policy_id"`
	Providers       map[string]oauthProviderConfig `json:"providers"`
}

func (h *TeamHandler) oauthConfig() (oauthConfig, error) {
	cfg := oauthConfig{Providers: map[string]oauthProviderConfig{}}
	var raw string
	if err := h.db.QueryRow(`SELECT oauth_json FROM team_settings WHERE id=1`).Scan(&raw); err != nil {
		return cfg, err
	}
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, err
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]oauthProviderConfig{}
	}
	return cfg, nil
}

// resolveProvider merges the stored config into the built-in defaults and
// reports whether the provider is usable at all.
func (h *TeamHandler) resolveProvider(id string) (oauthProviderDef, oauthProviderConfig, string, bool) {
	def, ok := oauthProviderByID(id)
	if !ok {
		return def, oauthProviderConfig{}, "", false
	}
	cfg, err := h.oauthConfig()
	if err != nil {
		return def, oauthProviderConfig{}, "", false
	}
	stored, present := cfg.Providers[id]
	if !present || !stored.Enabled || strings.TrimSpace(stored.ClientID) == "" || strings.TrimSpace(stored.ClientSecretEnc) == "" {
		return def, stored, "", false
	}
	if v := strings.TrimSpace(stored.AuthorizeURL); v != "" {
		def.authorizeURL = v
	}
	if v := strings.TrimSpace(stored.TokenURL); v != "" {
		def.tokenURL = v
	}
	if v := strings.TrimSpace(stored.UserInfoURL); v != "" {
		def.userInfoURL = v
	}
	if v := strings.TrimSpace(stored.Scopes); v != "" {
		def.scopes = v
	}
	secret, err := h.enc.Decrypt(stored.ClientSecretEnc)
	if err != nil {
		return def, stored, "", false
	}
	return def, stored, string(secret), true
}

// oauthCallbackURL is what the operator registers at the provider. It is
// derived from the branding base URL when set, otherwise from the request, so
// it stays correct behind a proxy without a second setting.
func (h *TeamHandler) oauthCallbackURL(r *http.Request, provider string) string {
	settings, _ := h.settings()
	base := strings.TrimRight(strings.TrimSpace(settings.Branding.APIBaseURL), "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/auth/oauth/" + provider + "/callback"
}

// --- request signing -------------------------------------------------------

// oauthCookieName carries state + PKCE verifier across the provider round trip.
const oauthCookieName = "meta-team-oauth"

type oauthFlow struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
}

func (h *TeamHandler) signFlow(flow oauthFlow) string {
	payload, _ := json.Marshal(flow)
	mac := hmac.New(sha256.New, h.enc.KeyMaterial())
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *TeamHandler) parseFlow(raw string) (oauthFlow, bool) {
	var flow oauthFlow
	parts := strings.SplitN(strings.TrimSpace(raw), ".", 2)
	if len(parts) != 2 {
		return flow, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return flow, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return flow, false
	}
	mac := hmac.New(sha256.New, h.enc.KeyMaterial())
	mac.Write(payload)
	if subtle.ConstantTimeCompare(signature, mac.Sum(nil)) != 1 {
		return flow, false
	}
	if json.Unmarshal(payload, &flow) != nil {
		return flow, false
	}
	return flow, true
}

// --- handlers --------------------------------------------------------------

// oauthStart sends the browser to the provider. It is a public endpoint: a
// visitor has no session yet, which is the whole point.
func (h *TeamHandler) oauthStart(w http.ResponseWriter, r *http.Request) {
	if !h.publicAuthNavigate(w, r) {
		return
	}
	providerID := strings.ToLower(strings.TrimSpace(chiParam(r, "provider")))
	def, cfg, _, ok := h.resolveProvider(providerID)
	if !ok {
		teamFail(w, 404, "oauth_provider_unavailable")
		return
	}
	state, err := teamRandom()
	if err != nil {
		teamFail(w, 500, "auth_unavailable")
		return
	}
	verifier, err := teamRandom()
	if err != nil {
		teamFail(w, 500, "auth_unavailable")
		return
	}
	challenge := sha256.Sum256([]byte(verifier))
	flow := oauthFlow{Provider: providerID, State: state, Verifier: verifier}
	// 10 minutes is long enough to log in at the provider, short enough that a
	// stale cookie cannot be replayed into a fresh callback.
	h.cookie(w, r, oauthCookieName, h.signFlow(flow), 600)

	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", cfg.ClientID)
	query.Set("redirect_uri", h.oauthCallbackURL(r, providerID))
	query.Set("state", state)
	if def.scopes != "" {
		query.Set("scope", def.scopes)
	}
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	http.Redirect(w, r, def.authorizeURL+"?"+query.Encode(), http.StatusFound)
}

// oauthCallback finishes the flow: verify, exchange, resolve the member, then
// hand out a session and send the browser to the app.
func (h *TeamHandler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	providerID := strings.ToLower(strings.TrimSpace(chiParam(r, "provider")))
	fail := func(code string) {
		h.cookie(w, r, oauthCookieName, "", -1)
		http.Redirect(w, r, "/app?oauth_error="+url.QueryEscape(code), http.StatusFound)
	}
	if !h.enabled() {
		http.NotFound(w, r)
		return
	}
	if !h.publicAuthNavigate(w, r) {
		return
	}
	if errParam := strings.TrimSpace(r.URL.Query().Get("error")); errParam != "" {
		// The user pressed "cancel" at the provider, most of the time. Report it
		// as an ordinary failure, not as an authentication attempt.
		fail("oauth_cancelled")
		return
	}
	def, cfg, secret, ok := h.resolveProvider(providerID)
	if !ok {
		fail("oauth_provider_unavailable")
		return
	}
	cookie, err := r.Cookie(oauthCookieName)
	if err != nil {
		fail("oauth_state_missing")
		return
	}
	flow, ok := h.parseFlow(cookie.Value)
	if !ok || flow.Provider != providerID {
		fail("oauth_state_invalid")
		return
	}
	// The state the provider echoed must be the state this browser started
	// with: that is what stops an attacker from feeding us their own code.
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(r.URL.Query().Get("state"))), []byte(flow.State)) != 1 {
		fail("oauth_state_invalid")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		fail("oauth_code_missing")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := h.exchangeOAuthCode(ctx, def, cfg, secret, code, flow.Verifier, h.oauthCallbackURL(r, providerID))
	if err != nil {
		log.Printf("team: oauth %s exchange failed: %v", providerID, err)
		fail("oauth_exchange_failed")
		return
	}
	h.cookie(w, r, oauthCookieName, "", -1)

	user, err := h.resolveOAuthMember(providerID, info)
	if err != nil {
		var denied *oauthDenied
		if errors.As(err, &denied) {
			fail(denied.code)
			return
		}
		log.Printf("team: oauth %s member resolve failed: %v", providerID, err)
		fail("oauth_account_failed")
		return
	}
	if _, err := h.startSession(w, r, user); err != nil {
		log.Printf("team: oauth %s session failed: %v", providerID, err)
		fail("oauth_session_failed")
		return
	}
	http.Redirect(w, r, "/app", http.StatusFound)
}

// oauthDenied is a refusal the member should read, as opposed to an internal
// failure. The code travels to the login page.
type oauthDenied struct{ code string }

func (e *oauthDenied) Error() string { return "oauth denied: " + e.code }

// resolveOAuthMember maps a provider identity onto a member, creating the
// account when the owner allowed self-registration.
func (h *TeamHandler) resolveOAuthMember(provider string, info oauthUserInfo) (*TeamUser, error) {
	if strings.TrimSpace(info.Subject) == "" {
		return nil, errors.New("provider returned no subject")
	}
	var userID int64
	err := h.db.QueryRow(`SELECT user_id FROM team_identities WHERE provider=? AND subject=?`, provider, info.Subject).Scan(&userID)
	if err == nil {
		user, err := h.user(userID)
		if err != nil {
			return nil, err
		}
		if user.Status != "active" {
			// A paused member cannot come back through a third-party login.
			return nil, &oauthDenied{code: "oauth_account_paused"}
		}
		_, _ = h.db.Exec(`UPDATE team_identities SET last_login_at=datetime('now'), email=?, name=?, avatar=? WHERE provider=? AND subject=?`,
			info.Email, info.Name, info.Avatar, provider, info.Subject)
		h.auditOAuth(provider, user.ID)
		return user, nil
	}

	cfg, err := h.oauthConfig()
	if err != nil {
		return nil, err
	}
	if !cfg.AutoRegister {
		return nil, &oauthDenied{code: "oauth_register_disabled"}
	}
	policyID := cfg.DefaultPolicyID
	if _, err := h.policy(policyID); err != nil {
		// No policy configured (or it was deleted): the site's first policy is
		// the only sane landing place.
		if err := h.db.QueryRow(`SELECT id FROM team_policies ORDER BY id LIMIT 1`).Scan(&policyID); err != nil {
			return nil, &oauthDenied{code: "oauth_no_policy"}
		}
	}
	username, err := h.uniqueUsername(oauthUsername(provider, info))
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = username
	}
	// No usable password: the account signs in through this provider only, and
	// the owner can still issue a recovery link later.
	secret, err := teamRandom()
	if err != nil {
		return nil, err
	}
	hash, err := hashTeamPassword(secret)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	tx, err := h.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO team_users(username,name,password_hash,role,policy_id) VALUES(?,?,?,'member',?)`,
		username, name, hash, policyID)
	if err != nil {
		return nil, err
	}
	created, _ := result.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO team_identities(user_id,provider,subject,email,name,avatar) VALUES(?,?,?,?,?,?)`,
		created, provider, info.Subject, info.Email, info.Name, info.Avatar); err != nil {
		// The unique index on (provider, subject) is the real arbiter: if a
		// parallel login inserted the same identity first, adopt that account
		// instead of failing the user.
		var existing int64
		if scanErr := h.db.QueryRow(`SELECT user_id FROM team_identities WHERE provider=? AND subject=?`, provider, info.Subject).Scan(&existing); scanErr == nil {
			return h.user(existing)
		}
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	h.auditOAuth(provider, created)
	return h.user(created)
}

func (h *TeamHandler) auditOAuth(provider string, userID int64) {
	id := userID
	_ = h.db.AuditEvent.Insert(&store.AuditEvent{
		ActorKind: "user", ActorID: &id, Action: "team.oauth.login." + provider,
		ResourceKind: "team", ResourceID: &id, Outcome: "success", StatusCode: 200,
	})
}

// oauthUsername derives a login name from the provider profile. Usernames here
// are also the login identifier, so the result must satisfy teamUsername.
func oauthUsername(provider string, info oauthUserInfo) string {
	base := strings.TrimSpace(info.Username)
	if base == "" {
		base = strings.TrimSpace(info.Name)
	}
	if base == "" {
		base = "user"
	}
	var out strings.Builder
	for _, char := range base {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '.', char == '_', char == '-':
			out.WriteRune(char)
		default:
			out.WriteRune('-')
		}
	}
	cleaned := strings.Trim(out.String(), ".-_")
	if len(cleaned) < 3 {
		cleaned = provider + "-" + cleaned
	}
	cleaned = strings.Trim(cleaned, ".-_")
	if len(cleaned) > 48 {
		cleaned = cleaned[:48]
	}
	if len(cleaned) < 3 {
		cleaned = provider + "-user"
	}
	return cleaned
}

// uniqueUsername appends a counter until the name is free, so two people called
// "alice" on different providers both get an account.
func (h *TeamHandler) uniqueUsername(base string) (string, error) {
	candidate := base
	for attempt := 1; attempt <= 50; attempt++ {
		if attempt > 1 {
			suffix := "-" + strconv.Itoa(attempt)
			trimmed := base
			if len(trimmed) > 64-len(suffix) {
				trimmed = trimmed[:64-len(suffix)]
			}
			candidate = trimmed + suffix
		}
		if !teamUsername.MatchString(candidate) {
			return "", errors.New("derived username is invalid")
		}
		var count int
		if err := h.db.QueryRow(`SELECT count(*) FROM team_users WHERE username=?`, candidate).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 && !h.operatorNameReserved(candidate) {
			return candidate, nil
		}
	}
	return "", errors.New("no free username")
}

// --- provider calls --------------------------------------------------------

type oauthUserInfo struct {
	Subject  string
	Username string
	Name     string
	Email    string
	Avatar   string
}

// exchangeOAuthCode runs the token exchange and the userinfo read. Providers
// differ only in these two shapes, so both live here rather than in a package
// per provider.
func (h *TeamHandler) exchangeOAuthCode(ctx context.Context, def oauthProviderDef, cfg oauthProviderConfig, secret, code, verifier, redirectURI string) (oauthUserInfo, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", cfg.ClientID)
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}
	if def.tokenAuth == "basic" {
		form.Set("client_secret", "")
	} else {
		form.Set("client_secret", secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, def.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthUserInfo{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if def.tokenAuth == "basic" {
		req.SetBasicAuth(cfg.ClientID, secret)
	}
	resp, err := h.oauthHTTPClient().Do(req)
	if err != nil {
		return oauthUserInfo{}, err
	}
	body, err := readOAuthBody(resp)
	if err != nil {
		return oauthUserInfo{}, err
	}
	var token struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return oauthUserInfo{}, fmt.Errorf("token response: %w", err)
	}
	if token.AccessToken == "" {
		if token.Error != "" || token.Description != "" {
			return oauthUserInfo{}, fmt.Errorf("token rejected: %s %s", token.Error, token.Description)
		}
		return oauthUserInfo{}, errors.New("token response carried no access_token")
	}

	profile, err := http.NewRequestWithContext(ctx, http.MethodGet, def.userInfoURL, nil)
	if err != nil {
		return oauthUserInfo{}, err
	}
	profile.Header.Set("Accept", "application/json")
	profile.Header.Set("Authorization", "Bearer "+token.AccessToken)
	profileResp, err := h.oauthHTTPClient().Do(profile)
	if err != nil {
		return oauthUserInfo{}, err
	}
	profileBody, err := readOAuthBody(profileResp)
	if err != nil {
		return oauthUserInfo{}, err
	}
	// The subject is whatever the provider considers stable. Both supported
	// providers return a numeric id; the field names differ.
	var raw map[string]any
	if err := json.Unmarshal(profileBody, &raw); err != nil {
		return oauthUserInfo{}, fmt.Errorf("userinfo response: %w", err)
	}
	info := oauthUserInfo{
		Subject:  stringifySubject(raw["id"]),
		Username: firstString(raw, "login", "username"),
		Name:     firstString(raw, def.nameField, "name", "login", "username"),
		Email:    firstString(raw, "email"),
		Avatar:   firstString(raw, def.avatarField, "avatar_url", "avatar"),
	}
	if info.Subject == "" {
		return oauthUserInfo{}, errors.New("userinfo response carried no id")
	}
	// GitHub only reveals the address through a separate endpoint, and only the
	// verified primary address is worth storing.
	if def.emailsURL != "" && info.Email == "" {
		if email, err := h.oauthPrimaryEmail(ctx, def.emailsURL, token.AccessToken); err == nil {
			info.Email = email
		}
	}
	return info, nil
}

func (h *TeamHandler) oauthPrimaryEmail(ctx context.Context, endpoint, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := h.oauthHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	body, err := readOAuthBody(resp)
	if err != nil {
		return "", err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(body, &emails); err != nil {
		return "", err
	}
	for _, item := range emails {
		if item.Primary && item.Verified {
			return item.Email, nil
		}
	}
	return "", errors.New("no verified primary email")
}

// oauthHTTPClient builds the client used for the provider's token and userinfo
// calls. It deliberately ignores HTTP_PROXY/HTTPS_PROXY: inside a container
// those point at the host, which is not where GitHub or Linux.do live, and the
// same contract already governs the outbound policy for upstream calls. The
// operator's own proxy setting is respected, because that one was chosen.
func (h *TeamHandler) oauthHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if h.outboundProxy != nil {
		if raw := strings.TrimSpace(h.outboundProxy()); raw != "" {
			if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
				transport.Proxy = http.ProxyURL(parsed)
			}
		}
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: transport}
}

func readOAuthBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func stringifySubject(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
