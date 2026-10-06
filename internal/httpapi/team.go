package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/ratelimit"
	"github.com/lan/meta-gateway/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const teamCookie = "mg_team_session"
const teamCSRFCookie = "mg_team_csrf"

type TeamBranding struct {
	Name             string `json:"name"`
	LogoURL          string `json:"logo_url"`
	Accent           string `json:"accent"`
	Notice           string `json:"notice"`
	LoginDescription string `json:"login_description"`
	APIBaseURL       string `json:"api_base_url"`
	ShowUsage        bool   `json:"show_usage"`
	ShowRouting      bool   `json:"show_routing"`
}
type TeamSettings struct {
	Mode     string       `json:"mode"`
	Branding TeamBranding `json:"branding"`
}
type TeamUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	PolicyID  int64  `json:"policy_id"`
	CreatedAt string `json:"created_at"`
	KeyCount  int    `json:"key_count"`
	// The account's own credit pool, granted by the owner or topped up with a
	// credit code. 0 = unlimited, like the per-key quota. Tokens and money are
	// two independent budgets and both are enforced.
	QuotaTotalTokens int64   `json:"quota_total_tokens"`
	QuotaUsedTokens  int64   `json:"quota_used_tokens"`
	QuotaTotalCost   float64 `json:"quota_total_cost"`
	QuotaUsedCost    float64 `json:"quota_used_cost"`
	Version          int     `json:"-"`
	PasswordHash     string  `json:"-"`
}
type TeamPolicy struct {
	ID                      int64    `json:"id"`
	Name                    string   `json:"name"`
	Models                  []string `json:"models"`
	MemberIDs               []int64  `json:"member_ids"`
	AllModels               bool     `json:"all_models"`
	MaxKeys                 int      `json:"max_keys"`
	RPM                     int      `json:"rpm"`
	AllowRouting            bool     `json:"allow_routing"`
	AllowRequestPreferences bool     `json:"allow_request_preferences"`
}
type teamPrincipal struct {
	User        *TeamUser
	SessionHash string
	CSRF        string
}
type teamPrincipalKey struct{}

func teamActor(r *http.Request) *teamPrincipal {
	p, _ := r.Context().Value(teamPrincipalKey{}).(*teamPrincipal)
	return p
}
func chiParam(r *http.Request, name string) string { return chi.URLParam(r, name) }

type TeamHandler struct {
	db                 *store.DB
	enc                *crypto.Encrypter
	mu                 sync.Mutex
	loginLimiter       *ratelimit.Limiter
	globalLoginLimiter *ratelimit.Limiter
	userLimiter        *groupRateLimiter
	requestDefaults    func() TeamRequestDefaults
	// outboundProxy reports the operator's outbound proxy. Empty means direct.
	outboundProxy func() string
	// deploymentAdmin reports the configured deployment administrator — the
	// username it signs in with and the secret that authenticates it. Switching
	// to team mode seeds the owner account from it, so the operator does not have
	// to invent a second identity for themselves.
	deploymentAdmin func() (username, password string)
	// tokenLoginOpen reports whether the admin token still works as a password
	// (i.e. no owner account has been claimed yet). It drives the sign-in page's
	// upgrade entry; see sessionHandler.tokenLoginAllowed.
	tokenLoginOpen func() bool
}

func NewTeamHandler(db *store.DB, enc *crypto.Encrypter) *TeamHandler {
	return &TeamHandler{db: db, enc: enc, loginLimiter: ratelimit.New(15, 5), globalLoginLimiter: ratelimit.New(120, 20), userLimiter: newGroupRateLimiter()}
}
func (h *TeamHandler) settings() (TeamSettings, error) {
	s := TeamSettings{Branding: TeamBranding{Name: "Meta Gateway", Accent: "#275b85", ShowUsage: true, ShowRouting: true}}
	var raw string
	if err := h.db.QueryRow(`SELECT mode,branding_json FROM team_settings WHERE id=1`).Scan(&s.Mode, &raw); err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(raw), &s.Branding); err != nil {
		return s, err
	}
	return s, nil
}
func (h *TeamHandler) enabled() bool { s, err := h.settings(); return err == nil && s.Mode == "team" }
func (h *TeamHandler) ModeGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.enabled() {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (h *TeamHandler) PrincipalSlot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), teamPrincipalKey{}, &teamPrincipal{})))
	})
}
func teamHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func teamRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func teamCSRF(raw string) string { return teamHash("team-csrf:" + raw) }
func (h *TeamHandler) secureCookie(r *http.Request) bool {
	s, _ := h.settings()
	origin, _ := url.Parse(r.Header.Get("Origin"))
	// Browsers send Origin on login/write requests even behind TLS termination.
	// Only the same host can strengthen (never weaken) the Secure requirement.
	return r.TLS != nil || strings.HasPrefix(s.Branding.APIBaseURL, "https://") ||
		(origin != nil && origin.Scheme == "https" && strings.EqualFold(origin.Host, r.Host))
}
func (h *TeamHandler) cookie(w http.ResponseWriter, r *http.Request, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: h.secureCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func teamOriginOK(r *http.Request) bool {
	raw := r.Header.Get("Origin")
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") && (r.TLS == nil || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host) && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
func teamWrite(r *http.Request) bool {
	return r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
}
func (h *TeamHandler) checkCSRF(r *http.Request, expected string) bool {
	origin, _ := url.Parse(r.Header.Get("Origin"))
	if origin != nil && origin.Scheme == "http" && h.secureCookie(r) {
		return false
	}
	return expected != "" && teamOriginOK(r) && hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Meta-CSRF")))
}
func teamRead(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		teamFail(w, 415, "json_required")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		teamFail(w, 400, "invalid_request")
		return false
	}
	return true
}
func teamFail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": code})
}
func teamJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, value)
}

const teamUserSelect = `SELECT u.id,u.username,u.name,u.role,u.status,u.policy_id,u.created_at,u.session_version,u.password_hash,
	u.quota_total_tokens,u.quota_used_tokens,u.quota_total_cost,u.quota_used_cost,
	(SELECT count(*) FROM downstream_keys k WHERE k.user_id=u.id AND k.team_deleted_at='') FROM team_users u`

func scanTeamUser(row interface{ Scan(...any) error }) (*TeamUser, error) {
	u := &TeamUser{}
	err := row.Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.Status, &u.PolicyID, &u.CreatedAt, &u.Version, &u.PasswordHash,
		&u.QuotaTotalTokens, &u.QuotaUsedTokens, &u.QuotaTotalCost, &u.QuotaUsedCost, &u.KeyCount)
	return u, err
}
func (h *TeamHandler) user(id int64) (*TeamUser, error) {
	return scanTeamUser(h.db.QueryRow(teamUserSelect+` WHERE u.id=?`, id))
}
func (h *TeamHandler) session(r *http.Request) (*teamPrincipal, error) {
	if !h.enabled() {
		return nil, errors.New("team disabled")
	}
	c, err := r.Cookie(teamCookie)
	if err != nil || len(c.Value) > 128 {
		return nil, errors.New("no session")
	}
	var id int64
	var version int
	hash := teamHash(c.Value)
	err = h.db.QueryRow(`SELECT user_id,version FROM team_sessions WHERE token_hash=? AND expires_at>?`, hash, time.Now().Unix()).Scan(&id, &version)
	if err != nil {
		return nil, err
	}
	u, err := h.user(id)
	if err != nil || u.Status != "active" || u.Version != version {
		return nil, errors.New("invalid session")
	}
	return &teamPrincipal{User: u, SessionHash: hash, CSRF: teamCSRF(c.Value)}, nil
}
func (h *TeamHandler) UserAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Browser sessions and inference API keys are deliberately distinct.
		if len(r.Header.Values("Authorization")) != 0 {
			teamFail(w, 401, "account_login_required")
			return
		}
		p, err := h.session(r)
		if err != nil {
			teamFail(w, 401, "account_login_required")
			return
		}
		if teamWrite(r) && !h.checkCSRF(r, p.CSRF) {
			teamFail(w, 403, "csrf_failed")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), teamPrincipalKey{}, p)))
	})
}
func (h *TeamHandler) AdminAuth(legacy func(string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if values := r.Header.Values("Authorization"); len(values) != 0 {
				if len(values) != 1 {
					teamFail(w, 401, "unauthorized")
					return
				}
				parts := strings.Fields(values[0])
				if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !legacy(parts[1]) {
					teamFail(w, 401, "unauthorized")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			p, err := h.session(r)
			if err != nil {
				teamFail(w, 401, "unauthorized")
				return
			}
			if p.User.Role != "owner" && p.User.Role != "admin" {
				teamFail(w, 403, "admin_required")
				return
			}
			// Limited administrators only get the team-management API. Everything
			// else, including newly added endpoints, defaults to owner-only.
			if p.User.Role == "admin" && !strings.HasPrefix(r.URL.Path, "/admin/team/") && !(r.Method == http.MethodGet && r.URL.Path == "/admin/mode") {
				teamFail(w, 403, "owner_required")
				return
			}
			if teamWrite(r) && !h.checkCSRF(r, p.CSRF) {
				teamFail(w, 403, "csrf_failed")
				return
			}
			if slot := teamActor(r); slot != nil {
				*slot = *p
			} else {
				r = r.WithContext(context.WithValue(r.Context(), teamPrincipalKey{}, p))
			}
			next.ServeHTTP(w, r)
		})
	}
}
func teamOwner(w http.ResponseWriter, r *http.Request) bool {
	p := teamActor(r)
	if p != nil && p.User != nil && p.User.Role != "owner" {
		teamFail(w, 403, "owner_required")
		return false
	}
	return true
}
func (h *TeamHandler) audit(r *http.Request, action string, id int64) {
	event := &store.AuditEvent{RequestID: chimw.GetReqID(r.Context()), ActorKind: "admin", Action: action, ResourceKind: "team", ResourceID: &id, Outcome: "success", StatusCode: 200}
	if p := teamActor(r); p != nil && p.User != nil {
		event.ActorKind = "user"
		event.ActorID = &p.User.ID
	}
	_ = h.db.AuditEvent.Insert(event)
}
func (h *TeamHandler) RegisterAdmin(r chi.Router) {
	r.Get("/mode", h.getMode)
	r.Patch("/mode", h.patchMode)
	r.Post("/mode/owner", h.bootstrap)
	// Legacy bootstrap URL retains the same implementation, but the control
	// now lives exclusively in Settings → Operating mode.
	r.Post("/team/bootstrap", h.bootstrap)
	r.Group(func(r chi.Router) {
		r.Use(h.ModeGate)
		r.Get("/team/settings", h.getSettings)
		r.Put("/team/settings", h.putSettings)
		r.Get("/team/users", h.listUsers)
		r.Post("/team/users", h.createUser)
		r.Post("/team/users/import", h.importUsers)
		r.Post("/team/users/bulk", h.bulkUsers)
		r.Patch("/team/users/{id}", h.updateUser)
		r.Get("/team/users/{id}/keys", h.adminUserKeys)
		r.Get("/team/users/{id}/events", h.userEvents)
		r.Post("/team/users/{id}/recovery", h.createRecovery)
		r.Post("/team/users/{id}/revoke-sessions", h.revokeUserSessions)
		r.Get("/team/policies", h.listPolicies)
		r.Post("/team/policies", h.savePolicy)
		r.Put("/team/policies/{id}", h.savePolicy)
		r.Delete("/team/policies/{id}", h.deletePolicy)
		r.Get("/team/candidates", h.adminCandidates)
		// Third-party sign-in: provider credentials and the member bindings.
		r.Get("/team/oauth", h.oauthSettings)
		r.Put("/team/oauth", h.putOAuthSettings)
		r.Get("/team/users/{id}/identities", h.userIdentities)
		r.Delete("/team/users/{id}/identities/{identityID}", h.deleteIdentity)
		// Team codes: invitations (possibly multi-use), credit vouchers and the
		// recovery links share one table, so one set of endpoints manages them.
		r.Get("/team/codes", h.listCodes)
		r.Post("/team/codes", h.createCodes)
		r.Delete("/team/codes/{id}", h.revokeCode)
		// Kept for older consoles and their tests: the invitations endpoints are
		// the single-use face of the same table.
		r.Get("/team/invitations", h.listInvites)
		r.Post("/team/invitations", h.createInvite)
		r.Delete("/team/invitations/{id}", h.revokeInvite)
	})
}
func (h *TeamHandler) RegisterPublic(r chi.Router) {
	// Registered OUTSIDE the mode gate on purpose: the sign-in page asks this
	// before anyone has a session, and the answer decides whether the upgrade
	// entry exists. A v3 deployment upgrading to this build is almost always
	// still personal, so gating it on team mode would hide the entry exactly
	// where it is needed. It exposes one bit — "the admin token still works as a
	// password" — and the token itself is of course still required to use it.
	r.Get("/auth/upgrade", h.authUpgrade)
	r.Group(func(a chi.Router) {
		a.Use(h.ModeGate)
		a.Get("/auth/options", h.authOptions)
		a.Post("/auth/login", h.login)
		a.Post("/auth/accept", h.accept)
		a.Post("/auth/recover", h.recoverAccount)
		// Third-party sign-in. The callback is a browser redirect target, so it
		// has to be public; both endpoints are rate limited through
		// publicAuthRequest like the password login.
		a.Get("/auth/oauth/{provider}/start", h.oauthStart)
		a.Get("/auth/oauth/{provider}/callback", h.oauthCallback)
	})
	r.Route("/me", func(me chi.Router) {
		me.Use(h.ModeGate)
		me.Use(h.UserAuth)
		me.Get("/", h.me)
		me.Post("/logout", h.logout)
		me.Post("/password", h.changePassword)
		me.Get("/sessions", h.sessions)
		me.Delete("/sessions/{id}", h.deleteSession)
		me.Get("/keys", h.myKeys)
		me.Post("/keys", h.createKey)
		me.Patch("/keys/{id}", h.updateKey)
		me.Post("/keys/{id}/reveal", h.revealKey)
		me.Post("/keys/{id}/rotate", h.rotateKey)
		me.Delete("/keys/{id}", h.deleteKey)
		me.Get("/models", h.myModels)
		me.Get("/model-catalog", h.myModelCatalog)
		me.Get("/preferences", h.getRequestPreferences)
		me.Put("/preferences", h.putRequestPreferences)
		me.Get("/requests", h.myRequests)
		me.Get("/requests/latency-histogram", h.myLatencyHistogram)
		me.Get("/model-pricing", h.myModelPricing)
		// The member's own overview: the same aggregates the console reads,
		// scoped to this account (see team_usage.go).
		// Money display settings: a member's console renders the same amounts
		// the operator's does, so it needs the same symbol and rate.
		me.Get("/display-settings", h.myDisplaySettings)
		me.Get("/usage/summary", h.myUsageSummary)
		me.Get("/usage/series", h.myUsageSeries)
		me.Get("/usage/top-models", h.myTopModels)
		me.Get("/plans", h.myPlans)
		me.Post("/plans", h.savePlan)
		me.Put("/plans/{id}", h.savePlan)
		me.Delete("/plans/{id}", h.deletePlan)
		me.Get("/candidates", h.myCandidates)
		// Credit vouchers are redeemed by the signed-in member, not by a key at
		// relay time: the pool they fill belongs to the account.
		me.Post("/redeem", h.redeemCredit)
		me.Get("/routes", h.myRouteList)
		me.Get("/routes/{model}", h.myRouteOrder)
		me.Put("/routes/{model}", h.saveRouteOrder)
		me.Delete("/routes/{model}", h.resetRouteOrder)
	})
}
func (h *TeamHandler) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.settings()
	if err != nil {
		teamFail(w, 500, "settings_unavailable")
		return
	}
	var count int
	_ = h.db.QueryRow(`SELECT count(*) FROM team_users WHERE role='owner'`).Scan(&count)
	role := "owner"
	if p := teamActor(r); p != nil && p.User != nil {
		role = p.User.Role
	}
	teamJSON(w, 200, map[string]any{"settings": s, "has_owner": count > 0, "role": role})
}

var teamAccent = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func safeTeamURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.RawQuery == "" && u.Fragment == ""
}
func (h *TeamHandler) putSettings(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	var s TeamSettings
	if !teamRead(w, r, &s) {
		return
	}
	s.Branding.Name = strings.TrimSpace(s.Branding.Name)
	s.Branding.APIBaseURL = strings.TrimRight(strings.TrimSpace(s.Branding.APIBaseURL), "/")
	if (s.Mode != "personal" && s.Mode != "team") || len(s.Branding.Name) < 1 || len(s.Branding.Name) > 80 || !teamAccent.MatchString(s.Branding.Accent) || !safeTeamURL(s.Branding.APIBaseURL) || !safeTeamURL(s.Branding.LogoURL) || len(s.Branding.Notice) > 2000 || len(s.Branding.LoginDescription) > 1000 {
		teamFail(w, 400, "invalid_settings")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	tx, err := h.db.Begin()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	defer tx.Rollback()
	raw, _ := json.Marshal(s.Branding)
	// Branding edits must never overwrite/re-enable the mode from a stale
	// browser tab. Mode has its own owner-only PATCH endpoint.
	var result sql.Result
	result, err = tx.Exec(`UPDATE team_settings SET branding_json=? WHERE id=1 AND mode='team' AND mode=?`, string(raw), s.Mode)
	if err == nil {
		n, _ := result.RowsAffected()
		if n != 1 {
			teamFail(w, 409, "mode_changed")
			return
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.audit(r, "team.settings.update", 1)
	teamJSON(w, 200, s)
}

var teamUsername = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$`)

// hashTeamPassword hashes a member password. There is deliberately no minimum
// length: the operator decides what strength their own deployment needs, and a
// deployment administrator's existing secret must be usable as the owner's
// first password when team mode is switched on. An empty password is still
// refused, and the upper bound keeps a hash from becoming a denial of service.
func hashTeamPassword(password string) (string, error) {
	if len(password) == 0 || len(password) > 1024 {
		return "", errors.New("password_length")
	}
	sum := sha256.Sum256([]byte(password))
	hash, err := bcrypt.GenerateFromPassword([]byte(base64.StdEncoding.EncodeToString(sum[:])), bcrypt.DefaultCost)
	return string(hash), err
}
func checkTeamPassword(hash, password string) bool {
	sum := sha256.Sum256([]byte(password))
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(base64.StdEncoding.EncodeToString(sum[:]))) == nil
}

// A real, constant dummy hash equalizes missing-user password checks.
var teamDummyHash, _ = hashTeamPassword("not-a-real-account-password")
