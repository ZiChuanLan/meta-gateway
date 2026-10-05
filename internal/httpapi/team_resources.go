package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/domain"
)

type teamKeyView struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Enabled    bool    `json:"enabled"`
	Hint       string  `json:"hint"`
	Models     string  `json:"models"`
	ExpiresAt  string  `json:"expires_at"`
	AllowedIPs string  `json:"allowed_ips"`
	PlanID     int64   `json:"plan_id"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt string  `json:"last_used_at"`
	UsedTokens int64   `json:"used_tokens"`
	Cost       float64 `json:"cost"`
}

func (h *TeamHandler) keyViews(w http.ResponseWriter, userID int64) {
	rows, err := h.db.Query(`SELECT k.id,k.name,k.enabled,k.team_hint,k.model_allowlist,k.expires_at,k.allowed_ips,k.team_plan_id,k.created_at,
		COALESCE((SELECT MAX(created_at) FROM team_requests q WHERE q.key_id=k.id AND q.user_id=k.user_id),''),
		k.quota_used_tokens,COALESCE((SELECT SUM(cost) FROM usage_records u WHERE u.downstream_key_id=k.id AND u.user_id=k.user_id),0)
		FROM downstream_keys k WHERE k.user_id=? AND k.team_deleted_at='' ORDER BY k.id DESC`, userID)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	defer rows.Close()
	items := []teamKeyView{}
	for rows.Next() {
		var k teamKeyView
		if rows.Scan(&k.ID, &k.Name, &k.Enabled, &k.Hint, &k.Models, &k.ExpiresAt, &k.AllowedIPs, &k.PlanID, &k.CreatedAt, &k.LastUsedAt, &k.UsedTokens, &k.Cost) != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		items = append(items, k)
	}
	teamJSON(w, 200, items)
}
func (h *TeamHandler) myKeys(w http.ResponseWriter, r *http.Request) {
	h.keyViews(w, teamActor(r).User.ID)
}
func (h *TeamHandler) adminUserKeys(w http.ResponseWriter, r *http.Request) {
	u, ok := h.managedUser(w, r)
	if ok {
		h.keyViews(w, u.ID)
	}
}
func (h *TeamHandler) ownedKey(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return 0, false
	}
	var count int
	if h.db.QueryRow(`SELECT count(*) FROM downstream_keys WHERE id=? AND user_id=? AND team_deleted_at=''`, id, teamActor(r).User.ID).Scan(&count) != nil || count != 1 {
		teamFail(w, 404, "not_found")
		return 0, false
	}
	return id, true
}

type teamKeyInput struct {
	Name       string `json:"name"`
	Models     string `json:"models"`
	ExpiresAt  string `json:"expires_at"`
	AllowedIPs string `json:"allowed_ips"`
	PlanID     int64  `json:"plan_id"`
	Enabled    *bool  `json:"enabled"`
}

func (h *TeamHandler) validateKey(w http.ResponseWriter, r *http.Request, in *teamKeyInput) bool {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 || len(in.Models) > 8000 || len(in.AllowedIPs) > 4000 || auth.ValidateKeyExpiry(in.ExpiresAt) != nil || auth.ValidateKeyAllowedIPs(in.AllowedIPs) != nil {
		teamFail(w, 400, "invalid_key")
		return false
	}
	if in.PlanID < 0 {
		teamFail(w, 400, "invalid_plan")
		return false
	}
	if in.PlanID > 0 {
		p, err := h.policy(teamActor(r).User.PolicyID)
		var count int
		if err != nil || !p.AllowRouting || h.db.QueryRow(`SELECT count(*) FROM team_route_plans WHERE id=? AND user_id=?`, in.PlanID, teamActor(r).User.ID).Scan(&count) != nil || count != 1 {
			teamFail(w, 403, "invalid_plan")
			return false
		}
	}
	return true
}
func (h *TeamHandler) createKey(w http.ResponseWriter, r *http.Request) {
	var in teamKeyInput
	if !teamRead(w, r, &in) || !h.validateKey(w, r, &in) {
		return
	}
	hash, raw, err := auth.NewToken()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	encrypted, err := h.enc.Encrypt([]byte(raw))
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	uid := teamActor(r).User.ID
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	// The limit and insertion are one SQLite write, including concurrent creates.
	res, err := h.db.Exec(`INSERT INTO downstream_keys(token_hash,token_enc,name,enabled,scopes,model_allowlist,expires_at,allowed_ips,user_id,team_plan_id,team_hint)
		SELECT ?,?,?,?,'relay',?,?,?,?,?,? FROM team_users u JOIN team_policies p ON p.id=u.policy_id JOIN team_settings s ON s.id=1
		WHERE u.id=? AND u.status='active' AND s.mode='team'
		AND (SELECT count(*) FROM downstream_keys WHERE user_id=u.id AND team_deleted_at='')<p.max_keys`,
		hash, encrypted, in.Name, enabled, in.Models, in.ExpiresAt, in.AllowedIPs, uid, in.PlanID, raw[len(raw)-4:], uid)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		teamFail(w, 409, "key_limit")
		return
	}
	id, _ := res.LastInsertId()
	h.audit(r, "team.key.create", id)
	teamJSON(w, 201, map[string]any{"id": id, "token": raw})
}
func (h *TeamHandler) updateKey(w http.ResponseWriter, r *http.Request) {
	id, ok := h.ownedKey(w, r)
	if !ok {
		return
	}
	var in teamKeyInput
	if !teamRead(w, r, &in) || !h.validateKey(w, r, &in) {
		return
	}
	// Never write back usage counters, user ownership, group selection or prices.
	_, err := h.db.Exec(`UPDATE downstream_keys SET name=?,model_allowlist=?,expires_at=?,allowed_ips=?,team_plan_id=?,enabled=COALESCE(?,enabled)
		WHERE id=? AND user_id=? AND team_deleted_at=''`, in.Name, in.Models, in.ExpiresAt, in.AllowedIPs, in.PlanID, in.Enabled, id, teamActor(r).User.ID)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.db.DownstreamKey.Invalidate(id)
	h.audit(r, "team.key.update", id)
	teamJSON(w, 200, map[string]bool{"ok": true})
}
func (h *TeamHandler) revealKey(w http.ResponseWriter, r *http.Request) {
	id, ok := h.ownedKey(w, r)
	if !ok {
		return
	}
	var encrypted string
	if h.db.QueryRow(`SELECT token_enc FROM downstream_keys WHERE id=? AND user_id=? AND team_deleted_at=''`, id, teamActor(r).User.ID).Scan(&encrypted) != nil {
		teamFail(w, 404, "not_found")
		return
	}
	raw, err := h.enc.Decrypt(encrypted)
	if err != nil {
		teamFail(w, 409, "plaintext_unavailable")
		return
	}
	h.audit(r, "team.key.reveal", id)
	teamJSON(w, 200, map[string]string{"token": string(raw)})
}
func (h *TeamHandler) rotateKey(w http.ResponseWriter, r *http.Request) {
	id, ok := h.ownedKey(w, r)
	if !ok {
		return
	}
	hash, raw, err := auth.NewToken()
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	encrypted, err := h.enc.Encrypt([]byte(raw))
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	_, err = h.db.Exec(`UPDATE downstream_keys SET token_hash=?,token_enc=?,team_hint=? WHERE id=? AND user_id=? AND team_deleted_at=''`, hash, encrypted, raw[len(raw)-4:], id, teamActor(r).User.ID)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.db.DownstreamKey.Invalidate(id)
	h.audit(r, "team.key.rotate", id)
	teamJSON(w, 200, map[string]string{"token": raw})
}
func (h *TeamHandler) deleteKey(w http.ResponseWriter, r *http.Request) {
	id, ok := h.ownedKey(w, r)
	if !ok {
		return
	}
	_, err := h.db.Exec(`UPDATE downstream_keys SET enabled=0,token_enc='',token_hash=?,team_deleted_at=datetime('now') WHERE id=? AND user_id=?`, fmt.Sprintf("revoked-team-key-%d", id), id, teamActor(r).User.ID)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.db.DownstreamKey.Invalidate(id)
	h.audit(r, "team.key.delete", id)
	teamJSON(w, 200, map[string]bool{"ok": true})
}

type teamCandidate struct {
	ID      int64  `json:"id"`
	Model   string `json:"model"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (h *TeamHandler) candidates() (items []teamCandidate, err error) {
	rows, err := h.db.Query(`SELECT m.id,r.model_pattern,c.name,(m.enabled=1 AND r.enabled=1 AND c.status='enabled')
		FROM route_members m JOIN routes r ON r.id=m.route_id JOIN channels c ON c.id=m.channel_id ORDER BY r.model_pattern,m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items = []teamCandidate{}
	for rows.Next() {
		var v teamCandidate
		if err = rows.Scan(&v.ID, &v.Model, &v.Name, &v.Enabled); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func policyAccess(p TeamPolicy) *domain.TeamAccess {
	a := &domain.TeamAccess{Models: p.Models, AllModels: p.AllModels, MemberIDs: map[int64]bool{}, RPM: p.RPM}
	for _, id := range p.MemberIDs {
		a.MemberIDs[id] = true
	}
	return a
}
func (h *TeamHandler) adminCandidates(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	items, err := h.candidates()
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	teamJSON(w, 200, items)
}
func (h *TeamHandler) myCandidates(w http.ResponseWriter, r *http.Request) {
	p, err := h.policy(teamActor(r).User.PolicyID)
	s, _ := h.settings()
	if err != nil || !p.AllowRouting || !s.Branding.ShowRouting {
		teamFail(w, 403, "routing_not_allowed")
		return
	}
	items, err := h.candidates()
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	a := policyAccess(p)
	out := []teamCandidate{}
	for _, v := range items {
		if a.AllowsGrant(v.ID) && a.AllowsModel(v.Model) {
			out = append(out, v)
		}
	}
	teamJSON(w, 200, out)
}
func (h *TeamHandler) myModels(w http.ResponseWriter, r *http.Request) {
	p, err := h.policy(teamActor(r).User.PolicyID)
	if err != nil {
		teamFail(w, 503, "policy_unavailable")
		return
	}
	items, err := h.candidates()
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	a := policyAccess(p)
	seen := map[string]bool{}
	out := []string{}
	for _, v := range items {
		if v.Enabled && a.AllowsGrant(v.ID) && a.AllowsModel(v.Model) && !seen[v.Model] {
			seen[v.Model] = true
			out = append(out, v.Model)
		}
	}
	teamJSON(w, 200, out)
}

type teamPlan struct {
	ID      int64                              `json:"id"`
	Name    string                             `json:"name"`
	Default bool                               `json:"default"`
	Routes  map[string][]domain.TeamRouteEntry `json:"routes"`
}

// Decoding is forgiving about an empty body: creating a goal name first and
// arranging models afterwards is the normal flow on the model page.
func arrangementOf(raw string) (domain.TeamRouteArrangement, bool) {
	out := domain.TeamRouteArrangement{Routes: map[string][]domain.TeamRouteEntry{}}
	if strings.TrimSpace(raw) == "" {
		return out, true
	}
	if json.Unmarshal([]byte(raw), &out) != nil || !out.Valid() {
		return domain.TeamRouteArrangement{}, false
	}
	if out.Routes == nil {
		out.Routes = map[string][]domain.TeamRouteEntry{}
	}
	return out, true
}

func encodeArrangement(routes map[string][]domain.TeamRouteEntry) string {
	if routes == nil {
		routes = map[string][]domain.TeamRouteEntry{}
	}
	raw, _ := json.Marshal(domain.TeamRouteArrangement{Routes: routes})
	return string(raw)
}

// defaultPlanName is only ever shown for a plan the user never named; the
// member UI renders a default plan under its own label.
const defaultPlanName = "Default"

var errPlanUnavailable = errors.New("route plan unavailable")

// modelMembers lists the route members behind one model, so an arrangement can
// only reference upstreams that model actually serves.
func (h *TeamHandler) modelMembers(model string) (map[int64]bool, error) {
	rows, err := h.db.Query(`SELECT m.id FROM route_members m JOIN routes r ON r.id=m.route_id WHERE r.model_pattern=?`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// resolvePlan picks the plan a request works on. Zero means "my own order":
// the user's default plan, created on first write so a member can start
// arranging without first learning what a plan is.
func (h *TeamHandler) resolvePlan(uid, planID int64, create bool) (int64, error) {
	if planID != 0 {
		var count int
		if err := h.db.QueryRow(`SELECT count(*) FROM team_route_plans WHERE id=? AND user_id=?`, planID, uid).Scan(&count); err != nil {
			return 0, err
		}
		if count != 1 {
			return 0, errPlanUnavailable
		}
		return planID, nil
	}
	var existing int64
	if err := h.db.QueryRow(`SELECT id FROM team_route_plans WHERE user_id=? AND is_default=1`, uid).Scan(&existing); err == nil {
		return existing, nil
	}
	if !create {
		return 0, nil
	}
	res, err := h.db.Exec(`INSERT INTO team_route_plans(user_id,name,members_json,is_default) VALUES(?,?,?,1)`, uid, defaultPlanName, "{}")
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (h *TeamHandler) loadArrangement(planID, uid int64) (domain.TeamRouteArrangement, bool) {
	var raw string
	if err := h.db.QueryRow(`SELECT members_json FROM team_route_plans WHERE id=? AND user_id=?`, planID, uid).Scan(&raw); err != nil {
		return domain.TeamRouteArrangement{}, false
	}
	return arrangementOf(raw)
}

// validateArrangement refuses an arrangement that reaches outside what the
// policy granted: every entry must be a granted member of that model's route.
func (h *TeamHandler) validateArrangement(w http.ResponseWriter, p TeamPolicy, routes map[string][]domain.TeamRouteEntry) bool {
	a := policyAccess(p)
	for model, entries := range routes {
		if !a.AllowsModel(model) {
			teamFail(w, 403, "model_not_allowed")
			return false
		}
		allowed, err := h.modelMembers(model)
		if err != nil {
			teamFail(w, 500, "load_failed")
			return false
		}
		seen := map[int64]bool{}
		for _, entry := range entries {
			if entry.ID <= 0 || seen[entry.ID] || entry.Weight < 1 || entry.Weight > 1000 ||
				!allowed[entry.ID] || !a.AllowsGrant(entry.ID) {
				teamFail(w, 403, "invalid_members")
				return false
			}
			seen[entry.ID] = true
		}
	}
	return true
}

func (h *TeamHandler) routingAllowed(w http.ResponseWriter, r *http.Request) (TeamPolicy, bool) {
	p, err := h.policy(teamActor(r).User.PolicyID)
	s, _ := h.settings()
	if err != nil || !p.AllowRouting || !s.Branding.ShowRouting {
		teamFail(w, 403, "routing_not_allowed")
		return p, false
	}
	return p, true
}
func (h *TeamHandler) myPlans(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.routingAllowed(w, r); !ok {
		return
	}
	rows, err := h.db.Query(`SELECT id,name,members_json,is_default FROM team_route_plans WHERE user_id=? ORDER BY is_default DESC,id`, teamActor(r).User.ID)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	defer rows.Close()
	items := []teamPlan{}
	for rows.Next() {
		var p teamPlan
		var raw string
		var isDefault int
		if rows.Scan(&p.ID, &p.Name, &raw, &isDefault) != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		arrangement, ok := arrangementOf(raw)
		if !ok {
			teamFail(w, 500, "load_failed")
			return
		}
		p.Routes = arrangement.Routes
		p.Default = isDefault == 1
		items = append(items, p)
	}
	teamJSON(w, 200, items)
}
func (h *TeamHandler) savePlan(w http.ResponseWriter, r *http.Request) {
	p, ok := h.routingAllowed(w, r)
	if !ok {
		return
	}
	var in teamPlan
	if !teamRead(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 80 {
		teamFail(w, 400, "invalid_plan")
		return
	}
	if len(in.Routes) > 0 && !h.validateArrangement(w, p, in.Routes) {
		return
	}
	raw := encodeArrangement(in.Routes)
	uid := teamActor(r).User.ID
	if rawID := chiParam(r, "id"); rawID != "" {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			teamFail(w, 400, "invalid_id")
			return
		}
		res, err := h.db.Exec(`UPDATE team_route_plans SET name=?,members_json=? WHERE id=? AND user_id=?`, in.Name, raw, id, uid)
		if err != nil {
			teamFail(w, 500, "save_failed")
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			teamFail(w, 404, "not_found")
			return
		}
		in.ID = id
		var isDefault int
		_ = h.db.QueryRow(`SELECT is_default FROM team_route_plans WHERE id=?`, id).Scan(&isDefault)
		in.Default = isDefault == 1
	} else {
		res, err := h.db.Exec(`INSERT INTO team_route_plans(user_id,name,members_json,is_default) SELECT ?,?,?,0 WHERE (SELECT count(*) FROM team_route_plans WHERE user_id=?)<20`, uid, in.Name, raw, uid)
		if err != nil {
			teamFail(w, 500, "save_failed")
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			teamFail(w, 409, "plan_limit")
			return
		}
		in.ID, _ = res.LastInsertId()
	}
	h.audit(r, "team.plan.update", in.ID)
	teamJSON(w, 200, in)
}
func (h *TeamHandler) deletePlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.routingAllowed(w, r); !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	uid := teamActor(r).User.ID
	res, err := h.db.Exec(`DELETE FROM team_route_plans WHERE id=? AND user_id=? AND NOT EXISTS
		(SELECT 1 FROM downstream_keys WHERE team_plan_id=? AND user_id=? AND team_deleted_at='')`, id, uid, id, uid)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		teamFail(w, 409, "plan_in_use")
		return
	}
	teamJSON(w, 200, map[string]bool{"ok": true})
}
func (h *TeamHandler) RelayMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := auth.DownstreamKey(r)
		if key == nil || key.TeamAccess == nil {
			next.ServeHTTP(w, r)
			return
		}
		a := key.TeamAccess
		// Client-supplied X-Request-ID is not a safe cross-user join key.
		// Namespace every accepted team request with a fresh server nonce.
		nonce, err := teamRandom()
		if err != nil {
			teamFail(w, 500, "request_unavailable")
			return
		}
		requestID := fmt.Sprintf("team-%d-%s", a.UserID, nonce)
		r = r.WithContext(context.WithValue(r.Context(), chimw.RequestIDKey, requestID))
		w.Header().Set("X-Request-ID", requestID)
		wrapped := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		completed := false
		defer func() {
			if r.Method != http.MethodPost {
				return
			}
			status := wrapped.Status()
			if status == 0 {
				status = 200
				if !completed {
					status = 500
				}
			}
			_, _ = h.db.Exec(`INSERT OR IGNORE INTO team_requests(request_id,user_id,key_id,path,status,latency_ms) VALUES(?,?,?,?,?,?)`,
				chimw.GetReqID(r.Context()), a.UserID, key.ID, r.URL.Path, status, time.Since(start).Milliseconds())
		}()
		group := &domain.KeyGroup{Name: fmt.Sprintf("team-user-%d", a.UserID), RatePerMinute: a.RPM, RateBurst: a.RPM}
		if allowed, _ := h.userLimiter.Allow(group); !allowed {
			w.Header().Set("Retry-After", "1")
			teamFail(wrapped, 429, "user_rate_limited")
			completed = true
			return
		}
		next.ServeHTTP(wrapped, r)
		completed = true
	})
}
func (h *TeamHandler) myRequests(w http.ResponseWriter, r *http.Request) {
	s, _ := h.settings()
	if !s.Branding.ShowUsage {
		teamFail(w, 403, "usage_disabled")
		return
	}
	since, until, ok := parseTimeRange(w, r.URL.Query())
	if !ok {
		return
	}
	uid := teamActor(r).User.ID
	where := `q.user_id=?`
	args := []any{uid}
	if since != nil {
		where += ` AND q.created_at>=?`
		args = append(args, since.UTC().Format("2006-01-02 15:04:05"))
	}
	if until != nil {
		where += ` AND q.created_at<=?`
		args = append(args, until.UTC().Format("2006-01-02 15:04:05"))
	}
	if raw := r.URL.Query().Get("key_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			teamFail(w, 400, "invalid_id")
			return
		}
		where += ` AND q.key_id=?`
		args = append(args, id)
	}
	modelExpression := `COALESCE((SELECT model FROM usage_records WHERE request_id=q.request_id AND user_id=q.user_id ORDER BY id DESC LIMIT 1),
		(SELECT model FROM proxy_logs WHERE request_id=q.request_id AND downstream_key_id=q.key_id ORDER BY id DESC LIMIT 1),'')`
	if model := strings.TrimSpace(r.URL.Query().Get("model")); model != "" {
		if len(model) > 256 {
			teamFail(w, 400, "invalid_request")
			return
		}
		where += ` AND ` + modelExpression + `=?`
		args = append(args, model)
	}
	switch r.URL.Query().Get("status") {
	case "", "all":
	case "success":
		where += ` AND q.status>=200 AND q.status<300`
	case "error":
		where += ` AND q.status>=400`
	default:
		teamFail(w, 400, "invalid_request")
		return
	}
	if search := strings.TrimSpace(r.URL.Query().Get("q")); search != "" {
		if len(search) > 128 {
			teamFail(w, 400, "invalid_request")
			return
		}
		where += ` AND instr(q.request_id,?)>0`
		args = append(args, search)
	}
	rows, err := h.db.Query(`SELECT q.request_id,q.key_id,q.path,q.status,q.latency_ms,q.created_at,
		`+modelExpression+`,
		(SELECT SUM(total_tokens) FROM usage_records WHERE request_id=q.request_id AND user_id=q.user_id),
		(SELECT SUM(cost) FROM usage_records WHERE request_id=q.request_id AND user_id=q.user_id),
		(SELECT SUM(prompt_tokens) FROM usage_records WHERE request_id=q.request_id AND user_id=q.user_id),
		(SELECT SUM(completion_tokens) FROM usage_records WHERE request_id=q.request_id AND user_id=q.user_id),
		(SELECT SUM(cache_read_tokens) FROM usage_records WHERE request_id=q.request_id AND user_id=q.user_id),
		(SELECT SUM(cache_creation_tokens) FROM usage_records WHERE request_id=q.request_id AND user_id=q.user_id),
		(SELECT COUNT(*) FROM proxy_logs WHERE request_id=q.request_id AND downstream_key_id=q.key_id),
		COALESCE((SELECT name FROM downstream_keys WHERE id=q.key_id AND user_id=q.user_id),''),
		COALESCE((SELECT client_family FROM proxy_logs WHERE request_id=q.request_id AND downstream_key_id=q.key_id ORDER BY id DESC LIMIT 1),'')
		FROM team_requests q WHERE `+where+` ORDER BY q.rowid DESC LIMIT 200`, args...)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	defer rows.Close()
	type item struct {
		RequestID           string   `json:"request_id"`
		KeyID               int64    `json:"key_id"`
		Path                string   `json:"path"`
		Status              int      `json:"status"`
		Latency             int64    `json:"latency_ms"`
		CreatedAt           string   `json:"created_at"`
		Model               string   `json:"model"`
		Tokens              *int64   `json:"tokens"`
		Cost                *float64 `json:"cost"`
		PromptTokens        *int64   `json:"prompt_tokens"`
		CompletionTokens    *int64   `json:"completion_tokens"`
		CacheReadTokens     *int64   `json:"cache_read_tokens"`
		CacheCreationTokens *int64   `json:"cache_creation_tokens"`
		Attempts            int      `json:"attempts"`
		KeyName             string   `json:"key_name"`
		// ClientFamily is the coarse client classification from the relay's
		// User-Agent sniffing, same value the admin log list shows. It rides
		// along so a member can tell "which client sent this" without seeing
		// anyone else's rows.
		ClientFamily string `json:"client_family,omitempty"`
	}
	items := []item{}
	for rows.Next() {
		var v item
		if rows.Scan(&v.RequestID, &v.KeyID, &v.Path, &v.Status, &v.Latency, &v.CreatedAt, &v.Model, &v.Tokens, &v.Cost, &v.PromptTokens, &v.CompletionTokens, &v.CacheReadTokens, &v.CacheCreationTokens, &v.Attempts, &v.KeyName, &v.ClientFamily) != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		items = append(items, v)
	}
	teamJSON(w, 200, items)
}

// --- personal routing arrangement (the model page's editor) ---------------

// teamUpstreamView is one upstream row of the arrangement editor. The site's
// own priority/weight/enabled ride along so "restore the site order" and "what
// did the operator set" are answerable without a second request.
type teamUpstreamView struct {
	ID           int64  `json:"id"`
	ChannelID    int64  `json:"channel_id"`
	Channel      string `json:"channel"`
	Origin       string `json:"origin"`
	Group        string `json:"group"`
	SitePriority int    `json:"site_priority"`
	SiteWeight   int    `json:"site_weight"`
	SiteEnabled  bool   `json:"site_enabled"`
	Weight       int    `json:"weight"`
	Disabled     bool   `json:"disabled"`
}

type teamRouteView struct {
	Model      string             `json:"model"`
	PlanID     int64              `json:"plan_id"`
	Customized bool               `json:"customized"`
	Upstreams  []teamUpstreamView `json:"upstreams"`
}

type teamRouteOrderInput struct {
	PlanID  int64                   `json:"plan_id"`
	Entries []domain.TeamRouteEntry `json:"entries"`
}

type teamRouteList struct {
	PlanID int64    `json:"plan_id"`
	Models []string `json:"models"`
}

// originModel resolves the upstream model name a member rewrites to, falling
// back to the route's own mapping (the alias form). Empty means the request
// keeps the route name.
func originModel(memberMapping, routeMapping string) string {
	for _, raw := range []string{memberMapping, routeMapping} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var parsed struct {
			Real string `json:"real"`
		}
		if json.Unmarshal([]byte(raw), &parsed) == nil && parsed.Real != "" {
			return parsed.Real
		}
	}
	return ""
}

// grantedUpstreams lists the route members the policy granted this user, in the
// site's own order.
func (h *TeamHandler) grantedUpstreams(model string, a *domain.TeamAccess) ([]teamUpstreamView, error) {
	rows, err := h.db.Query(`SELECT m.id,m.channel_id,c.name,m.priority,m.weight,m.enabled,COALESCE(m.group_name,''),
		COALESCE(m.mapping_json,''),COALESCE(r.mapping_json,'')
		FROM route_members m JOIN routes r ON r.id=m.route_id JOIN channels c ON c.id=m.channel_id
		WHERE r.model_pattern=? ORDER BY m.priority DESC,m.weight DESC,m.id`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []teamUpstreamView{}
	for rows.Next() {
		var item teamUpstreamView
		var enabled int
		var memberMapping, routeMapping string
		if err := rows.Scan(&item.ID, &item.ChannelID, &item.Channel, &item.SitePriority, &item.SiteWeight, &enabled, &item.Group, &memberMapping, &routeMapping); err != nil {
			return nil, err
		}
		if !a.AllowsGrant(item.ID) {
			continue
		}
		item.SiteEnabled = enabled != 0
		item.Origin = originModel(memberMapping, routeMapping)
		out = append(out, item)
	}
	return out, rows.Err()
}

// myRouteList answers "which models did I arrange" — the badge on the list.
func (h *TeamHandler) myRouteList(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.routingAllowed(w, r); !ok {
		return
	}
	uid := teamActor(r).User.ID
	planID, err := h.resolvePlan(uid, queryInt64Param(r, "plan_id"), false)
	if err != nil {
		teamFail(w, 404, "plan_not_found")
		return
	}
	out := teamRouteList{PlanID: planID, Models: []string{}}
	if planID != 0 {
		arrangement, ok := h.loadArrangement(planID, uid)
		if !ok {
			teamFail(w, 500, "load_failed")
			return
		}
		for model, entries := range arrangement.Routes {
			if len(entries) > 0 {
				out.Models = append(out.Models, model)
			}
		}
		sort.Strings(out.Models)
	}
	teamJSON(w, 200, out)
}

// myRouteOrder renders one model's upstream list: the user's order when they
// arranged it, the site's order otherwise. Upstreams the user dropped keep
// their place at the end so they can be switched back on.
func (h *TeamHandler) myRouteOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := h.routingAllowed(w, r)
	if !ok {
		return
	}
	model := strings.TrimSpace(chiParam(r, "model"))
	if model == "" || len(model) > 200 {
		teamFail(w, 400, "invalid_model")
		return
	}
	a := policyAccess(p)
	if !a.AllowsModel(model) {
		teamFail(w, 403, "model_not_allowed")
		return
	}
	uid := teamActor(r).User.ID
	planID, err := h.resolvePlan(uid, queryInt64Param(r, "plan_id"), false)
	if err != nil {
		teamFail(w, 404, "plan_not_found")
		return
	}
	entries := []domain.TeamRouteEntry{}
	if planID != 0 {
		arrangement, ok := h.loadArrangement(planID, uid)
		if !ok {
			teamFail(w, 500, "load_failed")
			return
		}
		entries = arrangement.Routes[model]
	}
	all, err := h.grantedUpstreams(model, a)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	customized := len(entries) > 0
	rowByID := make(map[int64]teamUpstreamView, len(all))
	for _, item := range all {
		if !customized {
			// Untouched model: the site's list, exactly as the site set it.
			item.Weight = item.SiteWeight
			item.Disabled = !item.SiteEnabled
		}
		rowByID[item.ID] = item
	}
	upstreams := make([]teamUpstreamView, 0, len(all))
	for _, entry := range entries {
		item, ok := rowByID[entry.ID]
		if !ok {
			continue
		}
		item.Weight = entry.Weight
		item.Disabled = entry.Disabled
		upstreams = append(upstreams, item)
		delete(rowByID, entry.ID)
	}
	for _, item := range all {
		rest, ok := rowByID[item.ID]
		if !ok {
			continue
		}
		if customized {
			// The user's list decides who participates: a granted upstream they
			// left out stays visible but out of routing until switched back on.
			rest.Weight = rest.SiteWeight
			rest.Disabled = true
		}
		upstreams = append(upstreams, rest)
	}
	teamJSON(w, 200, teamRouteView{Model: model, PlanID: planID, Customized: customized, Upstreams: upstreams})
}

// saveRouteOrder stores one model's upstream list. plan_id 0 means the user's
// own order, which is created on first save.
func (h *TeamHandler) saveRouteOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := h.routingAllowed(w, r)
	if !ok {
		return
	}
	model := strings.TrimSpace(chiParam(r, "model"))
	if model == "" || len(model) > 200 {
		teamFail(w, 400, "invalid_model")
		return
	}
	var in teamRouteOrderInput
	if !teamRead(w, r, &in) {
		return
	}
	if len(in.Entries) == 0 || len(in.Entries) > 200 {
		teamFail(w, 400, "invalid_members")
		return
	}
	if !h.validateArrangement(w, p, map[string][]domain.TeamRouteEntry{model: in.Entries}) {
		return
	}
	uid := teamActor(r).User.ID
	planID, err := h.resolvePlan(uid, in.PlanID, true)
	if err != nil {
		teamFail(w, 404, "plan_not_found")
		return
	}
	arrangement, ok := h.loadArrangement(planID, uid)
	if !ok {
		teamFail(w, 500, "load_failed")
		return
	}
	arrangement.Routes[model] = in.Entries
	if len(arrangement.Routes) > 500 {
		teamFail(w, 400, "invalid_plan")
		return
	}
	if _, err := h.db.Exec(`UPDATE team_route_plans SET members_json=? WHERE id=? AND user_id=?`,
		encodeArrangement(arrangement.Routes), planID, uid); err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.audit(r, "team.route.arrange", planID)
	teamJSON(w, 200, map[string]any{"model": model, "plan_id": planID, "entries": in.Entries})
}

// resetRouteOrder drops the user's list for one model, handing it back to the
// site's own order.
func (h *TeamHandler) resetRouteOrder(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.routingAllowed(w, r); !ok {
		return
	}
	model := strings.TrimSpace(chiParam(r, "model"))
	if model == "" || len(model) > 200 {
		teamFail(w, 400, "invalid_model")
		return
	}
	uid := teamActor(r).User.ID
	planID, err := h.resolvePlan(uid, queryInt64Param(r, "plan_id"), false)
	if err != nil {
		teamFail(w, 404, "plan_not_found")
		return
	}
	if planID == 0 {
		teamJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	arrangement, ok := h.loadArrangement(planID, uid)
	if !ok {
		teamFail(w, 500, "load_failed")
		return
	}
	delete(arrangement.Routes, model)
	if _, err := h.db.Exec(`UPDATE team_route_plans SET members_json=? WHERE id=? AND user_id=?`,
		encodeArrangement(arrangement.Routes), planID, uid); err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.audit(r, "team.route.reset", planID)
	teamJSON(w, 200, map[string]bool{"ok": true})
}

// queryInt64Param reads an optional numeric query parameter; a missing or
// malformed value is simply absent (0).
func queryInt64Param(r *http.Request, name string) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}
