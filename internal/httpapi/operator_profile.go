package httpapi

import (
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/totp"
	"net/http"
	"strings"
	"time"
)

func (h *sessionHandler) operatorProfile(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	name, err := h.db.OperatorUsername(h.adminUsername)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	prefs, err := h.db.OperatorPreferences()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	state, err := h.db.AdminTOTP.Get()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"username": name, "configured": prefs.AdminUsername != "", "totp_enabled": state.Enabled})
}
func (h *sessionHandler) saveOperatorProfile(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	if !teamOriginOK(r) {
		writeError(w, 403, "csrf_failed")
		return
	}
	var req struct {
		Username string `json:"username"`
		Token    string `json:"token"`
		TOTP     string `json:"totp_code"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		return
	}
	// Reuse the login brute-force buckets for sensitive credential confirmation.
	if h.loginLimiter != nil {
		if ok, _ := h.loginLimiter.Allow(hashLoginKey("ip:" + ClientIP(r).String())); !ok {
			writeError(w, 429, "login_rate_limited")
			return
		}
	}
	if h.globalLoginLimiter != nil {
		if ok, _ := h.globalLoginLimiter.Allow(0); !ok {
			writeError(w, 429, "login_rate_limited")
			return
		}
	}
	if !auth.ValidAdminToken(req.Token, h.adminTokens) {
		writeError(w, 403, "invalid_credentials")
		return
	}
	state, err := h.db.AdminTOTP.Get()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if state.Enabled {
		secret, err := h.enc.Decrypt(state.SecretEncrypted)
		if err != nil || !totp.Verify(string(secret), req.TOTP, time.Now()) {
			writeError(w, 403, "totp_required")
			return
		}
	}
	name := strings.ToLower(strings.TrimSpace(req.Username))
	if !teamUsername.MatchString(name) {
		writeError(w, 400, "invalid_username")
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE operator_preferences SET id=id WHERE id=1`); err != nil {
		writeStoreError(w, err)
		return
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM team_users WHERE lower(username)=?`, name).Scan(&count); err != nil {
		writeStoreError(w, err)
		return
	}
	if count > 0 {
		writeError(w, 409, "username_taken")
		return
	}
	if _, err = tx.Exec(`UPDATE operator_preferences SET admin_username=? WHERE id=1`, name); err != nil {
		writeStoreError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeStoreError(w, err)
		return
	}
	h.operatorProfile(w, r)
}

func (h *TeamHandler) operatorNameReserved(name string) bool {
	p, err := h.db.OperatorPreferences()
	return err != nil || (p.AdminUsername != "" && strings.EqualFold(strings.TrimSpace(name), p.AdminUsername))
}
