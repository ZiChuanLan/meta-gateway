package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/ratelimit"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/totp"
)

// sessionTTL is how long a TOTP-verified admin session token stays valid.
const sessionTTL = 12 * time.Hour

// sessionHandler serves the public login/session exchange and the admin
// TOTP management endpoints.
type sessionHandler struct {
	db            *store.DB
	adminTokens   []string
	adminUsername string
	team          *TeamHandler
	sessionKey    []byte // HMAC key for session tokens (master-key derived)
	enc           encryptor
	// Keep the deployment-wide and per-IP buckets in separate limiter maps.
	// High-cardinality IP churn can evict per-IP buckets without ever resetting
	// the global brute-force budget.
	globalLoginLimiter *ratelimit.Limiter
	loginLimiter       *ratelimit.Limiter
	// tokenLoginBreakGlass re-opens the ADMIN_TOKEN-as-password path after the
	// deployment has claimed an owner account (admin_onboarding.go).
	tokenLoginBreakGlass string
}

type encryptor interface {
	Encrypt(plaintext []byte) (string, error)
	Decrypt(encoded string) ([]byte, error)
}

// RegisterPublic mounts the login exchange outside the admin auth wall
// (the chi router mounted at /admin handles the rest; this path is added
// directly to the root router before the /admin mount).
func (h *sessionHandler) RegisterPublic(r interface {
	Post(string, http.HandlerFunc)
}) {
	r.Post("/admin/session", h.login)
}

// RegisterAdmin mounts TOTP management inside the admin auth wall.
func (h *sessionHandler) RegisterAdmin(r interface {
	Get(string, http.HandlerFunc)
	Post(string, http.HandlerFunc)
}) {
	r.Get("/operator-profile", h.operatorProfile)
	r.Post("/operator-profile", h.saveOperatorProfile)
	// The first-run claim of the deployment administrator: who this gateway is
	// signed in as, and the one call that turns the admin token into a real
	// account (see operator_onboarding.go).
	r.Get("/operator/onboarding", h.onboarding)
	r.Post("/operator/claim", h.claimOwner)
	r.Get("/totp/status", h.status)
	r.Post("/totp/setup", h.setup)
	r.Post("/totp/enable", h.enable)
	r.Post("/totp/disable", h.disable)
}

// login exchanges a raw admin token (plus TOTP code when enabled) for a
// short-lived signed session token. When TOTP is enabled and the code is
// missing/wrong the response is 401 with {"error":"totp_required"} so the
// client can show the second factor step.
func (h *sessionHandler) login(w http.ResponseWriter, r *http.Request) {
	if !teamOriginOK(r) {
		writeError(w, http.StatusForbidden, "csrf_failed")
		return
	}
	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if h.loginLimiter != nil || h.globalLoginLimiter != nil {
		ip := ClientIP(r).String()
		allowedGlobal, waitGlobal := true, time.Duration(0)
		if h.globalLoginLimiter != nil {
			allowedGlobal, waitGlobal = h.globalLoginLimiter.Allow(0)
		}
		allowedIP, waitIP := true, time.Duration(0)
		if h.loginLimiter != nil {
			allowedIP, waitIP = h.loginLimiter.Allow(hashLoginKey("ip:" + ip))
		}
		if !allowedGlobal || !allowedIP {
			wait := waitIP
			if waitGlobal > wait {
				wait = waitGlobal
			}
			seconds := int(wait.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "login rate limit exceeded")
			return
		}
	}
	if req.Username != "" || req.Password != "" {
		username := strings.ToLower(strings.TrimSpace(req.Username))
		// One identity table: the owner account signs in whether or not the
		// member module is running (the personal/team switch is about members, not
		// about how the deployment owner authenticates), while a member account
		// needs the module on. An existing account owns its name either way.
		if h.team != nil {
			u, err := scanTeamUser(h.db.QueryRow(teamUserSelect+` WHERE u.username=?`, username))
			switch {
			case err == nil:
				if !checkTeamPassword(u.PasswordHash, req.Password) || u.Status != "active" {
					writeError(w, http.StatusUnauthorized, "invalid_credentials")
					return
				}
				if h.team.enabled() {
					h.team.issueSession(w, r, u)
					return
				}
				if u.Role != "owner" {
					// The account is real but this gateway is not serving team users
					// right now, so its session would not work.
					writeError(w, http.StatusUnauthorized, "invalid_credentials")
					return
				}
				h.writeOperatorSession(w)
				return
			case !errors.Is(err, sql.ErrNoRows):
				writeError(w, http.StatusInternalServerError, "auth_unavailable")
				return
			}
		}
		// Equalize unknown-account password work with a normal account login.
		checkTeamPassword(teamDummyHash, req.Password)
		adminUsername, lookupErr := h.db.OperatorUsername(h.adminUsername)
		if lookupErr != nil {
			writeError(w, 500, "auth_unavailable")
			return
		}
		if username != adminUsername {
			writeError(w, http.StatusUnauthorized, "invalid_credentials")
			return
		}
		// The deployment's own token as a password: the upgrade path for a
		// version that had no account. It closes itself once an owner credential
		// exists (see operator_onboarding.go).
		if !h.tokenLoginAllowed() {
			writeError(w, http.StatusUnauthorized, "invalid_credentials")
			return
		}
		req.Token = req.Password
	}
	if req.Token != "" && req.Username == "" && req.Password == "" && !h.tokenLoginAllowed() {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	if !auth.ValidAdminToken(req.Token, h.adminTokens) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	state, err := h.db.AdminTOTP.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp state")
		return
	}
	if state.Enabled {
		secret, derr := h.enc.Decrypt(state.SecretEncrypted)
		if derr != nil || !totp.Verify(string(secret), req.TOTPCode, time.Now()) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "totp_required"})
			return
		}
	}
	h.writeOperatorSession(w)
}

// writeOperatorSession signs in the deployment principal — the identity the raw
// admin token has always produced. The owner account uses it too, so a claimed
// credential keeps working on a gateway whose member module is switched off.
func (h *sessionHandler) writeOperatorSession(w http.ResponseWriter) {
	sessionToken, err := auth.SignSessionToken(h.sessionKey, sessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"session_token": sessionToken})
}

func hashLoginKey(value string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// status reports whether TOTP is enabled (never leaks the secret).
func (h *sessionHandler) status(w http.ResponseWriter, r *http.Request) {
	state, err := h.db.AdminTOTP.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": state.Enabled})
}

// setup generates a fresh secret when TOTP is not yet enabled and returns
// the otpauth URI plus the plaintext secret (shown once).
func (h *sessionHandler) setup(w http.ResponseWriter, r *http.Request) {
	state, err := h.db.AdminTOTP.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp state")
		return
	}
	if state.Enabled {
		writeError(w, http.StatusConflict, "totp already enabled")
		return
	}
	secret, err := totp.NewSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp secret")
		return
	}
	encrypted, err := h.enc.Encrypt([]byte(secret))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp secret encrypt")
		return
	}
	if err := h.db.AdminTOTP.SetSecret(encrypted); err != nil {
		writeError(w, http.StatusInternalServerError, "totp persist")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_uri": totp.URI("MetaGateway", "admin", secret),
	})
}

// enable verifies a code against the stored secret and flips the flag on.
func (h *sessionHandler) enable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	state, err := h.db.AdminTOTP.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp state")
		return
	}
	secret, derr := h.enc.Decrypt(state.SecretEncrypted)
	if derr != nil || !totp.Verify(string(secret), req.Code, time.Now()) {
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	if err := h.db.AdminTOTP.SetEnabled(true); err != nil {
		writeError(w, http.StatusInternalServerError, "totp persist")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true})
}

// disable requires a valid current code (so a leaked session token alone
// cannot turn 2FA off) and clears the secret.
func (h *sessionHandler) disable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	state, err := h.db.AdminTOTP.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp state")
		return
	}
	if !state.Enabled {
		writeError(w, http.StatusConflict, "totp not enabled")
		return
	}
	secret, derr := h.enc.Decrypt(state.SecretEncrypted)
	if derr != nil || !totp.Verify(string(secret), req.Code, time.Now()) {
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	if err := h.db.AdminTOTP.Clear(); err != nil {
		writeError(w, http.StatusInternalServerError, "totp persist")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}
