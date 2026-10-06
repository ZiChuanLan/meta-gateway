package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/totp"
)

func (h *TeamHandler) bootstrap(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if !teamRead(w, r, &req) {
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.Name = strings.TrimSpace(req.Name)
	if !teamUsername.MatchString(req.Username) || len(req.Name) == 0 || len(req.Name) > 80 {
		teamFail(w, 400, "invalid_username")
		return
	}
	state, err := h.db.AdminTOTP.Get()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	if state.Enabled {
		secret, e := h.enc.Decrypt(state.SecretEncrypted)
		if e != nil || !totp.Verify(string(secret), req.TOTP, time.Now()) {
			teamFail(w, 403, "totp_required")
			return
		}
	}
	if h.operatorNameReserved(req.Username) {
		teamFail(w, 409, "username_taken")
		return
	}
	hash, err := hashTeamPassword(req.Password)
	if err != nil {
		teamFail(w, 400, "password_length")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	result, err := h.db.Exec(`INSERT INTO team_users(username,name,password_hash,role,policy_id)
		SELECT ?,?,?,'owner',1 WHERE NOT EXISTS(SELECT 1 FROM team_users WHERE role='owner')`, req.Username, req.Name, hash)
	if err != nil {
		teamFail(w, 409, "username_taken")
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		teamFail(w, 409, "owner_exists")
		return
	}
	id, _ := result.LastInsertId()
	h.audit(r, "team.owner.create", id)
	teamJSON(w, 201, map[string]int64{"id": id})
}
func (h *TeamHandler) authUpgrade(w http.ResponseWriter, r *http.Request) {
	if !teamOriginOK(r) {
		teamFail(w, 403, "csrf_failed")
		return
	}
	open := h.tokenLoginOpen != nil && h.tokenLoginOpen()
	teamJSON(w, 200, map[string]any{"upgrade_login": open})
}
func (h *TeamHandler) authOptions(w http.ResponseWriter, r *http.Request) {
	if !teamOriginOK(r) {
		teamFail(w, 403, "csrf_failed")
		return
	}
	raw, err := teamRandom()
	if err != nil {
		teamFail(w, 500, "auth_unavailable")
		return
	}
	h.cookie(w, r, teamCSRFCookie, raw, 3600)
	s, _ := h.settings()
	// The login page renders one button per usable provider, so the visitor
	// never sees an option that could not work. The currency rides along because
	// the member app prints money before it has a session.
	//
	// `upgrade_login` tells an upgrading deployment that its admin token still
	// works as a password (i.e. no owner account has been claimed yet). Showing
	// that entry is the whole reason it is public: it reveals nothing usable —
	// the token is still required — and without it the upgrade path would be
	// invisible on the one page where it is needed.
	upgradeLogin := false
	if h.tokenLoginOpen != nil {
		upgradeLogin = h.tokenLoginOpen()
	}
	teamJSON(w, 200, map[string]any{
		"branding":      s.Branding,
		"csrf":          teamCSRF(raw),
		"oauth":         h.oauthLoginOptions(),
		"currency":      h.currency(),
		"upgrade_login": upgradeLogin,
	})
}

// currency renders the site's money presentation for the member app. It never
// fails: a display preference must not be able to break a login page.
func (h *TeamHandler) currency() currencyView {
	if h.db == nil || h.db.Display == nil {
		return currencyView{Symbol: store.DefaultCurrencySymbol, Rate: 1}
	}
	return currencyFor(h.db.Display.Get())
}
func (h *TeamHandler) publicAuthRequest(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(teamCSRFCookie)
	if err != nil || len(c.Value) > 128 || !h.checkCSRF(r, teamCSRF(c.Value)) {
		teamFail(w, 403, "csrf_failed")
		return false
	}
	ok, _ := h.globalLoginLimiter.Allow(0)
	ipok, _ := h.loginLimiter.Allow(hashLoginKey("team:" + ClientIP(r).String()))
	if !ok || !ipok {
		w.Header().Set("Retry-After", "30")
		teamFail(w, 429, "login_rate_limited")
		return false
	}
	return true
}

// publicAuthNavigate guards the endpoints a browser reaches by top-level
// navigation: the OAuth start and its callback. Those are reached with a plain
// link or a provider redirect, so there is no way to demand the CSRF header the
// in-page calls carry. The protections that matter are here anyway — the Origin
// check, the rate limits, and, for the callback, the state bound to a signed
// cookie the attacker cannot forge. Neither endpoint can attach an existing
// account to someone else's session: a first-time identity creates its own
// account or is refused.
func (h *TeamHandler) publicAuthNavigate(w http.ResponseWriter, r *http.Request) bool {
	if !teamOriginOK(r) {
		teamFail(w, 403, "csrf_failed")
		return false
	}
	ok, _ := h.globalLoginLimiter.Allow(0)
	ipok, _ := h.loginLimiter.Allow(hashLoginKey("team:" + ClientIP(r).String()))
	if !ok || !ipok {
		w.Header().Set("Retry-After", "30")
		teamFail(w, 429, "login_rate_limited")
		return false
	}
	return true
}

// startSession opens a session for one member and sets its cookie, without
// writing a response body. Callers that answer with JSON (the password login)
// and callers that answer with a redirect (the OAuth callback) share it.
func (h *TeamHandler) startSession(w http.ResponseWriter, r *http.Request, u *TeamUser) (string, error) {
	raw, err := teamRandom()
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	tx, err := h.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM team_sessions WHERE expires_at<=? OR token_hash IN
		(SELECT token_hash FROM team_sessions WHERE user_id=? ORDER BY created_at DESC LIMIT -1 OFFSET 9)`, now, u.ID)
	if err == nil {
		var result sql.Result
		result, err = tx.Exec(`INSERT INTO team_sessions(token_hash,user_id,version,created_at,expires_at)
		SELECT ?,id,session_version,?,? FROM team_users WHERE id=? AND status='active' AND session_version=?`, teamHash(raw), now, now+12*3600, u.ID, u.Version)
		if err == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				return "", errSessionRefused
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return "", err
	}
	h.cookie(w, r, teamCookie, raw, 12*3600)
	return teamCSRF(raw), nil
}

// errSessionRefused means the row would not be inserted: the account was paused
// or its session version moved between the read and the write.
var errSessionRefused = errors.New("session refused")

func (h *TeamHandler) issueSession(w http.ResponseWriter, r *http.Request, u *TeamUser) {
	csrf, err := h.startSession(w, r, u)
	if err != nil {
		if errors.Is(err, errSessionRefused) {
			teamFail(w, 401, "invalid_credentials")
			return
		}
		teamFail(w, 500, "auth_unavailable")
		return
	}
	teamJSON(w, 200, map[string]any{"user": u, "csrf": csrf})
}
func (h *TeamHandler) login(w http.ResponseWriter, r *http.Request) {
	if !h.publicAuthRequest(w, r) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !teamRead(w, r, &req) {
		return
	}
	u, err := scanTeamUser(h.db.QueryRow(teamUserSelect+` WHERE u.username=?`, strings.TrimSpace(req.Username)))
	hash := teamDummyHash
	if err == nil {
		hash = u.PasswordHash
	}
	valid := checkTeamPassword(hash, req.Password)
	if !valid || err != nil || u.Status != "active" {
		teamFail(w, 401, "invalid_credentials")
		return
	}
	h.issueSession(w, r, u)
}
func (h *TeamHandler) accept(w http.ResponseWriter, r *http.Request) {
	if !h.publicAuthRequest(w, r) {
		return
	}
	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !teamRead(w, r, &req) {
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.Name = strings.TrimSpace(req.Name)
	if !teamUsername.MatchString(req.Username) || len(req.Name) == 0 || len(req.Name) > 80 {
		teamFail(w, 400, "invalid_username")
		return
	}
	if h.operatorNameReserved(req.Username) {
		teamFail(w, 409, "username_taken")
		return
	}
	hash, err := hashTeamPassword(req.Password)
	if err != nil {
		teamFail(w, 400, "password_length")
		return
	}
	h.mu.Lock()
	tx, err := h.db.Begin()
	if err != nil {
		h.mu.Unlock()
		teamFail(w, 500, "save_failed")
		return
	}
	defer tx.Rollback()
	var policyID, quotaTokens int64
	var quotaCost float64
	var role string
	// Both invitation shapes go through this one statement: a single-use link
	// (max_uses = 1) and a batch code handed to a whole class. "Used up" is
	// used_count >= max_uses, so nothing has to rewrite the row later.
	consume := func(hash string) error {
		return tx.QueryRow(`UPDATE team_invites
			SET used_count = used_count + 1, consumed_at = COALESCE(consumed_at, ?)
			WHERE token_hash=? AND kind='invite' AND revoked=0 AND expires_at>? AND used_count < max_uses
			RETURNING policy_id, role, quota_tokens, quota_cost`,
			time.Now().Unix(), hash, time.Now().Unix()).Scan(&policyID, &role, &quotaTokens, &quotaCost)
	}
	// A failed UPDATE matches no row and therefore changes nothing, so trying
	// the normalized spelling first is safe. Short codes are typed by hand (so
	// case is normalized); tokens issued before that format are case-sensitive.
	var consumeErr error
	for _, candidate := range teamCodeCandidates(req.Token) {
		if consumeErr = consume(candidate); consumeErr == nil {
			break
		}
	}
	if consumeErr != nil {
		h.mu.Unlock()
		teamFail(w, 400, "invitation_invalid")
		return
	}
	// An invitation may carry a starting credit — tokens, money, or both: the
	// operator hands out the account and its first allowance in one code.
	result, err := tx.Exec(`INSERT INTO team_users(username,name,password_hash,role,policy_id,quota_total_tokens,quota_total_cost) VALUES(?,?,?,?,?,?,?)`, req.Username, req.Name, hash, role, policyID, quotaTokens, quotaCost)
	if err != nil {
		h.mu.Unlock()
		teamFail(w, 409, "username_taken")
		return
	}
	id, _ := result.LastInsertId()
	err = tx.Commit()
	h.mu.Unlock()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	u, err := h.user(id)
	if err != nil {
		teamFail(w, 500, "auth_unavailable")
		return
	}
	h.issueSession(w, r, u)
}
func (h *TeamHandler) recoverAccount(w http.ResponseWriter, r *http.Request) {
	if !h.publicAuthRequest(w, r) {
		return
	}
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !teamRead(w, r, &req) {
		return
	}
	hash, err := hashTeamPassword(req.Password)
	if err != nil {
		teamFail(w, 400, "password_length")
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
	var id int64
	err = tx.QueryRow(`UPDATE team_invites SET consumed_at=? WHERE token_hash=? AND kind='recovery' AND consumed_at IS NULL AND revoked=0 AND expires_at>? RETURNING user_id`, time.Now().Unix(), teamHash(req.Token), time.Now().Unix()).Scan(&id)
	if err != nil {
		teamFail(w, 400, "invitation_invalid")
		return
	}
	_, err = tx.Exec(`UPDATE team_users SET password_hash=?,session_version=session_version+1 WHERE id=?`, hash, id)
	if err == nil {
		_, err = tx.Exec(`DELETE FROM team_sessions WHERE user_id=?`, id)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE team_invites SET revoked=1 WHERE user_id=? AND kind='recovery' AND consumed_at IS NULL`, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.cookie(w, r, teamCookie, "", -1)
	teamJSON(w, 200, map[string]bool{"ok": true})
}
func (h *TeamHandler) me(w http.ResponseWriter, r *http.Request) {
	p := teamActor(r)
	policy, err := h.policy(p.User.PolicyID)
	if err != nil {
		teamFail(w, 503, "policy_unavailable")
		return
	}
	s, _ := h.settings()
	credit := creditFor(p.User.QuotaTotalTokens, p.User.QuotaUsedTokens, p.User.QuotaTotalCost, p.User.QuotaUsedCost)
	teamJSON(w, 200, map[string]any{"user": p.User, "policy": policy, "csrf": p.CSRF, "branding": s.Branding, "credit": credit, "currency": h.currency()})
}
func (h *TeamHandler) logout(w http.ResponseWriter, r *http.Request) {
	_, err := h.db.Exec(`DELETE FROM team_sessions WHERE token_hash=?`, teamActor(r).SessionHash)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.cookie(w, r, teamCookie, "", -1)
	teamJSON(w, 200, map[string]bool{"ok": true})
}
func (h *TeamHandler) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if !teamRead(w, r, &req) {
		return
	}
	p := teamActor(r)
	if !checkTeamPassword(p.User.PasswordHash, req.Current) {
		teamFail(w, 403, "invalid_credentials")
		return
	}
	hash, err := hashTeamPassword(req.Password)
	if err != nil {
		teamFail(w, 400, "password_length")
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE team_users SET password_hash=?,session_version=session_version+1 WHERE id=?`, hash, p.User.ID)
	if err == nil {
		_, err = tx.Exec(`DELETE FROM team_sessions WHERE user_id=?`, p.User.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.cookie(w, r, teamCookie, "", -1)
	h.audit(r, "team.password.change", p.User.ID)
	teamJSON(w, 200, map[string]bool{"ok": true})
}
func (h *TeamHandler) sessions(w http.ResponseWriter, r *http.Request) {
	p := teamActor(r)
	rows, err := h.db.Query(`SELECT token_hash,created_at,expires_at FROM team_sessions WHERE user_id=? AND expires_at>? ORDER BY created_at DESC LIMIT 20`, p.User.ID, time.Now().Unix())
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id string
		var created, expiry int64
		if rows.Scan(&id, &created, &expiry) != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		items = append(items, map[string]any{"id": id, "created_at": created, "expires_at": expiry, "current": id == p.SessionHash})
	}
	teamJSON(w, 200, items)
}
func (h *TeamHandler) deleteSession(w http.ResponseWriter, r *http.Request) {
	p := teamActor(r)
	id := chiParam(r, "id")
	res, err := h.db.Exec(`DELETE FROM team_sessions WHERE token_hash=? AND user_id=?`, id, p.User.ID)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		teamFail(w, 404, "not_found")
		return
	}
	if id == p.SessionHash {
		h.cookie(w, r, teamCookie, "", -1)
	}
	teamJSON(w, 200, map[string]bool{"ok": true})
}
