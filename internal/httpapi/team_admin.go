package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *TeamHandler) policy(id int64) (TeamPolicy, error) {
	var p TeamPolicy
	var models, members string
	err := h.db.QueryRow(`SELECT id,name,models_json,members_json,all_models,max_keys,rpm,allow_routing,allow_request_preferences FROM team_policies WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &models, &members, &p.AllModels, &p.MaxKeys, &p.RPM, &p.AllowRouting, &p.AllowRequestPreferences)
	if err == nil {
		err = json.Unmarshal([]byte(models), &p.Models)
	}
	if err == nil {
		err = json.Unmarshal([]byte(members), &p.MemberIDs)
	}
	if p.Models == nil {
		p.Models = []string{}
	}
	if p.MemberIDs == nil {
		p.MemberIDs = []int64{}
	}
	return p, err
}
func (h *TeamHandler) listPolicies(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`SELECT id FROM team_policies ORDER BY id`)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			rows.Close()
			teamFail(w, 500, "load_failed")
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	items := []TeamPolicy{}
	for _, id := range ids {
		p, err := h.policy(id)
		if err != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		items = append(items, p)
	}
	teamJSON(w, 200, items)
}
func (h *TeamHandler) savePolicy(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	var p TeamPolicy
	if !teamRead(w, r, &p) {
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 80 || p.MaxKeys < 1 || p.MaxKeys > 100 || p.RPM < 1 || p.RPM > 100000 || len(p.Models) > 500 || len(p.MemberIDs) > 1000 {
		teamFail(w, 400, "invalid_policy")
		return
	}
	for _, m := range p.Models {
		if strings.TrimSpace(m) == "" || len(m) > 256 {
			teamFail(w, 400, "invalid_policy")
			return
		}
	}
	seen := map[int64]bool{}
	for _, id := range p.MemberIDs {
		var count int
		if id <= 0 || seen[id] || h.db.QueryRow(`SELECT count(*) FROM route_members WHERE id=?`, id).Scan(&count) != nil || count != 1 {
			teamFail(w, 400, "invalid_members")
			return
		}
		seen[id] = true
	}
	if p.Models == nil {
		p.Models = []string{}
	}
	if p.MemberIDs == nil {
		p.MemberIDs = []int64{}
	}
	models, _ := json.Marshal(p.Models)
	members, _ := json.Marshal(p.MemberIDs)
	id := int64(0)
	var err error
	if raw := chiParam(r, "id"); raw != "" {
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			teamFail(w, 400, "invalid_id")
			return
		}
		res, e := h.db.Exec(`UPDATE team_policies SET name=?,models_json=?,members_json=?,all_models=?,max_keys=?,rpm=?,allow_routing=?,allow_request_preferences=? WHERE id=?`, p.Name, string(models), string(members), p.AllModels, p.MaxKeys, p.RPM, p.AllowRouting, p.AllowRequestPreferences, id)
		err = e
		if e == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				teamFail(w, 404, "not_found")
				return
			}
		}
	} else {
		res, e := h.db.Exec(`INSERT INTO team_policies(name,models_json,members_json,all_models,max_keys,rpm,allow_routing,allow_request_preferences) VALUES(?,?,?,?,?,?,?,?)`, p.Name, string(models), string(members), p.AllModels, p.MaxKeys, p.RPM, p.AllowRouting, p.AllowRequestPreferences)
		err = e
		if e == nil {
			id, _ = res.LastInsertId()
		}
	}
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.audit(r, "team.policy.update", id)
	p.ID = id
	teamJSON(w, 200, p)
}
func (h *TeamHandler) deletePolicy(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	res, err := h.db.Exec(`DELETE FROM team_policies WHERE id=? AND id<>1 AND NOT EXISTS(SELECT 1 FROM team_users WHERE policy_id=?) AND NOT EXISTS(SELECT 1 FROM team_invites WHERE policy_id=?)`, id, id, id)
	if err != nil {
		teamFail(w, 409, "policy_in_use")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		teamFail(w, 409, "policy_in_use")
		return
	}
	h.audit(r, "team.policy.delete", id)
	teamJSON(w, 200, map[string]bool{"ok": true})
}
func (h *TeamHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	query := teamUserSelect
	if p := teamActor(r); p != nil && p.User != nil && p.User.Role == "admin" {
		query += ` WHERE u.role='member'`
	}
	rows, err := h.db.Query(query + ` ORDER BY u.id DESC LIMIT 1000`)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	defer rows.Close()
	items := []*TeamUser{}
	for rows.Next() {
		u, e := scanTeamUser(rows)
		if e != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		items = append(items, u)
	}
	teamJSON(w, 200, items)
}
func (h *TeamHandler) managedUser(w http.ResponseWriter, r *http.Request) (*TeamUser, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return nil, false
	}
	u, err := h.user(id)
	if err != nil {
		teamFail(w, 404, "not_found")
		return nil, false
	}
	if p := teamActor(r); p != nil && p.User != nil && p.User.Role == "admin" && u.Role != "member" {
		teamFail(w, 403, "owner_required")
		return nil, false
	}
	return u, true
}
func (h *TeamHandler) updateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := h.managedUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Status   *string `json:"status"`
		PolicyID *int64  `json:"policy_id"`
		Role     *string `json:"role"`
		// The account credit pool. quota_reset zeroes the spend counters in the
		// same write, which is what "top this person up" means in practice.
		QuotaTotal *int64   `json:"quota_total_tokens"`
		QuotaCost  *float64 `json:"quota_total_cost"`
		QuotaReset bool     `json:"quota_reset"`
	}
	if !teamRead(w, r, &req) {
		return
	}
	if req.QuotaTotal != nil {
		if *req.QuotaTotal < 0 || *req.QuotaTotal > teamQuotaMaxTokens {
			teamFail(w, 400, "invalid_quota")
			return
		}
		u.QuotaTotalTokens = *req.QuotaTotal
	}
	if req.QuotaCost != nil {
		if *req.QuotaCost < 0 || *req.QuotaCost > teamQuotaMaxCost {
			teamFail(w, 400, "invalid_quota")
			return
		}
		u.QuotaTotalCost = *req.QuotaCost
	}
	resetUsed := false
	if req.QuotaReset {
		u.QuotaUsedTokens = 0
		u.QuotaUsedCost = 0
		resetUsed = true
	}
	if req.Role != nil && !teamOwner(w, r) {
		return
	}
	if req.Name != nil {
		u.Name = strings.TrimSpace(*req.Name)
		if u.Name == "" || len(u.Name) > 80 {
			teamFail(w, 400, "invalid_request")
			return
		}
	}
	if req.Status != nil {
		if *req.Status != "active" && *req.Status != "paused" {
			teamFail(w, 400, "invalid_request")
			return
		}
		if u.Role == "owner" && *req.Status != "active" {
			teamFail(w, 409, "owner_protected")
			return
		}
		u.Status = *req.Status
	}
	if req.Role != nil {
		if u.Role == "owner" || (*req.Role != "member" && *req.Role != "admin") {
			teamFail(w, 409, "owner_protected")
			return
		}
		u.Role = *req.Role
	}
	if req.PolicyID != nil {
		if _, err := h.policy(*req.PolicyID); err != nil {
			teamFail(w, 400, "invalid_policy")
			return
		}
		u.PolicyID = *req.PolicyID
	}
	// A quota reset rides along with the rest of the update, so "raise the cap
	// and start counting again" is one atomic write rather than two.
	tx, err := h.db.Begin()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	defer tx.Rollback()
	set := `name=?,status=?,role=?,policy_id=?,quota_total_tokens=?,quota_total_cost=?,session_version=session_version+1`
	args := []any{u.Name, u.Status, u.Role, u.PolicyID, u.QuotaTotalTokens, u.QuotaTotalCost}
	if resetUsed {
		set += `,quota_used_tokens=0,quota_used_cost=0`
	}
	_, err = tx.Exec(`UPDATE team_users SET `+set+` WHERE id=?`, append(args, u.ID)...)
	if err == nil {
		_, err = tx.Exec(`DELETE FROM team_sessions WHERE user_id=?`, u.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.audit(r, "team.user.update", u.ID)
	teamJSON(w, 200, u)
}
func (h *TeamHandler) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := h.managedUser(w, r)
	if !ok {
		return
	}
	if u.Role == "owner" {
		teamFail(w, 409, "owner_protected")
		return
	}
	_, err := h.db.Exec(`DELETE FROM team_sessions WHERE user_id=?`, u.ID)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.audit(r, "team.sessions.revoke", u.ID)
	teamJSON(w, 200, map[string]bool{"ok": true})
}
func (h *TeamHandler) userEvents(w http.ResponseWriter, r *http.Request) {
	u, ok := h.managedUser(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(`SELECT action,created_at,COALESCE(actor_id,0) FROM audit_events WHERE resource_kind='team' AND resource_id=? AND action LIKE 'team.user.%' ORDER BY id DESC LIMIT 50`, u.ID)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var action, created string
		var actor int64
		if rows.Scan(&action, &created, &actor) != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		items = append(items, map[string]any{"action": action, "created_at": created, "actor_id": actor})
	}
	teamJSON(w, 200, items)
}

type teamInviteView struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	PolicyID  int64  `json:"policy_id"`
	Role      string `json:"role"`
	ExpiresAt int64  `json:"expires_at"`
	Consumed  bool   `json:"consumed"`
	Revoked   bool   `json:"revoked"`
}

func (h *TeamHandler) listInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`SELECT id,label,policy_id,role,expires_at,consumed_at IS NOT NULL,revoked FROM team_invites WHERE kind='invite' ORDER BY id DESC LIMIT 200`)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	defer rows.Close()
	items := []teamInviteView{}
	for rows.Next() {
		var v teamInviteView
		if rows.Scan(&v.ID, &v.Label, &v.PolicyID, &v.Role, &v.ExpiresAt, &v.Consumed, &v.Revoked) != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		items = append(items, v)
	}
	teamJSON(w, 200, items)
}
func (h *TeamHandler) createInvite(w http.ResponseWriter, r *http.Request) {
	if !h.enabled() {
		teamFail(w, 409, "team_disabled")
		return
	}
	var req struct {
		Label    string `json:"label"`
		PolicyID int64  `json:"policy_id"`
		Role     string `json:"role"`
	}
	if !teamRead(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if req.Role == "admin" && !teamOwner(w, r) {
		return
	}
	if (req.Role != "member" && req.Role != "admin") || len(req.Label) > 80 {
		teamFail(w, 400, "invalid_request")
		return
	}
	if _, err := h.policy(req.PolicyID); err != nil {
		teamFail(w, 400, "invalid_policy")
		return
	}
	raw, err := teamRandom()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	expiry := time.Now().Add(7 * 24 * time.Hour).Unix()
	res, err := h.db.Exec(`INSERT INTO team_invites(token_hash,label,policy_id,role,expires_at) VALUES(?,?,?,?,?)`, teamHash(raw), req.Label, req.PolicyID, req.Role, expiry)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	id, _ := res.LastInsertId()
	h.audit(r, "team.invite.create", id)
	teamJSON(w, 201, map[string]any{"id": id, "path": "/app/accept#invite=" + raw, "expires_at": expiry})
}
func (h *TeamHandler) revokeInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	query := `UPDATE team_invites SET revoked=1 WHERE id=?`
	if p := teamActor(r); p != nil && p.User != nil && p.User.Role == "admin" {
		query += ` AND role='member' AND kind='invite'`
	}
	res, err := h.db.Exec(query, id)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		teamFail(w, 404, "not_found")
		return
	}
	teamJSON(w, 200, map[string]bool{"ok": true})
}
func (h *TeamHandler) createRecovery(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	u, ok := h.managedUser(w, r)
	if !ok {
		return
	}
	raw, err := teamRandom()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE team_invites SET revoked=1 WHERE user_id=? AND kind='recovery'`, u.ID)
	if err == nil {
		_, err = tx.Exec(`INSERT INTO team_invites(token_hash,label,policy_id,kind,user_id,expires_at) VALUES(?,?,?,'recovery',?,?)`, teamHash(raw), u.Name, u.PolicyID, u.ID, time.Now().Add(time.Hour).Unix())
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.audit(r, "team.user.recovery", u.ID)
	teamJSON(w, 201, map[string]string{"path": "/app/recover#recovery=" + raw})
}
