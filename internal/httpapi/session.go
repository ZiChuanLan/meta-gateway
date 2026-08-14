package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/totp"
)

// sessionTTL is how long a TOTP-verified admin session token stays valid.
const sessionTTL = 12 * time.Hour

// sessionHandler serves the public login/session exchange and the admin
// TOTP management endpoints.
type sessionHandler struct {
	db          *store.DB
	adminTokens []string
	sessionKey  []byte // HMAC key for session tokens (master-key derived)
	enc         encryptor
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
	var req struct {
		Token    string `json:"token"`
		TOTPCode string `json:"totp_code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !auth.ValidAdminToken(req.Token, h.adminTokens) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
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
	sessionToken, err := auth.SignSessionToken(h.sessionKey, sessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"session_token": sessionToken})
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
