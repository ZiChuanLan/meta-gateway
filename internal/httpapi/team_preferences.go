package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

type TeamRequestDefaults struct {
	FailoverEnabled bool `json:"failover_enabled"`
	RetryTimes      int  `json:"retry_times"`
}

func (h *TeamHandler) getMode(w http.ResponseWriter, r *http.Request) {
	s, err := h.settings()
	if err != nil {
		teamFail(w, 500, "settings_unavailable")
		return
	}
	var count int
	if h.db.QueryRow(`SELECT count(*) FROM team_users WHERE role='owner' AND status='active'`).Scan(&count) != nil {
		teamFail(w, 500, "settings_unavailable")
		return
	}
	role := "owner"
	if p := teamActor(r); p != nil && p.User != nil {
		role = p.User.Role
	}
	teamJSON(w, 200, map[string]any{"mode": s.Mode, "has_owner": count > 0, "role": role})
}

func (h *TeamHandler) patchMode(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	var input struct {
		Mode string `json:"mode"`
	}
	if !teamRead(w, r, &input) {
		return
	}
	if input.Mode != "personal" && input.Mode != "team" {
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
	// Acquire SQLite's write lock before checking the owner invariant.
	if _, err = tx.Exec(`UPDATE team_settings SET mode=mode WHERE id=1`); err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM team_users WHERE role='owner' AND status='active'`).Scan(&count); err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	if input.Mode == "team" && count == 0 {
		// The operator is already signed in as the deployment administrator, so
		// take that identity instead of making them type a second account for
		// themselves. If the deployment has no admin secret to derive a first
		// password from, fall back to the explicit form.
		username, password := "", ""
		if h.deploymentAdmin != nil {
			username, password = h.deploymentAdmin()
		}
		username = strings.ToLower(strings.TrimSpace(username))
		if username == "" || password == "" || !teamUsername.MatchString(username) {
			teamFail(w, 409, "create_owner_first")
			return
		}
		hash, hashErr := hashTeamPassword(password)
		if hashErr != nil {
			teamFail(w, 409, "create_owner_first")
			return
		}
		// The operator and the owner are now the same identity, so the
		// reservation that keeps a team account from shadowing the operator
		// credential has nothing left to guard — and it is exactly what would
		// reject this insert, since the trigger fires while admin_username is
		// set. Clearing it is the whole reason this is one account and not two.
		if _, err = tx.Exec(`UPDATE operator_preferences SET admin_username='' WHERE id=1`); err != nil {
			teamFail(w, 500, "save_failed")
			return
		}
		if _, err = tx.Exec(`INSERT INTO team_users(username,name,password_hash,role,policy_id)
			VALUES(?,?,?,'owner',1)`, username, username, hash); err != nil {
			teamFail(w, 409, "username_taken")
			return
		}
	}
	_, err = tx.Exec(`UPDATE team_settings SET mode=? WHERE id=1`, input.Mode)
	if err == nil && input.Mode == "personal" {
		_, err = tx.Exec(`DELETE FROM team_sessions`)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.audit(r, "team.mode.update", 1)
	h.getMode(w, r)
}

func (h *TeamHandler) getRequestPreferences(w http.ResponseWriter, r *http.Request) {
	u := teamActor(r).User
	p, err := h.policy(u.PolicyID)
	if err != nil {
		teamFail(w, 503, "policy_unavailable")
		return
	}
	var raw string
	if h.db.QueryRow(`SELECT preferences_json FROM team_users WHERE id=?`, u.ID).Scan(&raw) != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	var preferences domain.UserRequestPreferences
	if json.Unmarshal([]byte(raw), &preferences) != nil || !preferences.Valid() {
		teamFail(w, 500, "invalid_preferences")
		return
	}
	if preferences.Failover == "" {
		preferences.Failover = "inherit"
	}
	defaults := TeamRequestDefaults{}
	if h.requestDefaults != nil {
		defaults = h.requestDefaults()
	}
	active := domain.UserRequestPreferences{Failover: "inherit"}
	if p.AllowRequestPreferences {
		active = preferences
	}
	effective := TeamRequestDefaults{FailoverEnabled: defaults.FailoverEnabled && active.Failover != "off", RetryTimes: defaults.RetryTimes}
	if active.MaxRetries != nil && *active.MaxRetries < effective.RetryTimes {
		effective.RetryTimes = *active.MaxRetries
	}
	if !effective.FailoverEnabled {
		effective.RetryTimes = 0
	}
	teamJSON(w, 200, map[string]any{"preferences": preferences, "can_edit": p.AllowRequestPreferences, "site": defaults, "effective": effective})
}

func (h *TeamHandler) putRequestPreferences(w http.ResponseWriter, r *http.Request) {
	var preferences domain.UserRequestPreferences
	if !teamRead(w, r, &preferences) {
		return
	}
	if !preferences.Valid() {
		teamFail(w, 400, "invalid_preferences")
		return
	}
	if preferences.Failover == "" {
		preferences.Failover = "inherit"
	}
	raw, _ := json.Marshal(preferences)
	uid := teamActor(r).User.ID
	result, err := h.db.Exec(`UPDATE team_users SET preferences_json=? WHERE id=? AND status='active'
		AND EXISTS(SELECT 1 FROM team_policies p WHERE p.id=team_users.policy_id AND p.allow_request_preferences=1)
		AND EXISTS(SELECT 1 FROM team_settings s WHERE s.id=1 AND s.mode='team')`, string(raw), uid)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		teamFail(w, 403, "preferences_not_allowed")
		return
	}
	h.audit(r, "team.preferences.update", uid)
	h.getRequestPreferences(w, r)
}

type teamModelView struct {
	Name             string `json:"name"`
	Vendor           string `json:"vendor"`
	Kind             string `json:"kind"`
	ContextWindow    int64  `json:"context_window"`
	InputModalities  string `json:"input_modalities"`
	OutputModalities string `json:"output_modalities"`
	Endpoints        string `json:"endpoints"`
	Candidates       int    `json:"candidates"`
	SupportsThinking int    `json:"supports_thinking"`
}

func (h *TeamHandler) myModelCatalog(w http.ResponseWriter, r *http.Request) {
	p, err := h.policy(teamActor(r).User.PolicyID)
	if err != nil {
		teamFail(w, 503, "policy_unavailable")
		return
	}
	access := policyAccess(p)
	rows, err := h.db.Query(`SELECT rm.id,r.model_pattern,
		COALESCE(NULLIF(mm.vendor,''),mc.provider,''),COALESCE(mc.kind,''),COALESCE(mm.context_window,0),
		COALESCE(NULLIF(mm.input_modalities,''),mc.input_modalities,''),COALESCE(NULLIF(mm.output_modalities,''),mc.output_modalities,''),
		COALESCE(mc.endpoints,''),COALESCE(mm.supports_thinking,-1)
		FROM route_members rm JOIN routes r ON r.id=rm.route_id JOIN channels c ON c.id=rm.channel_id
		LEFT JOIN model_metadata mm ON mm.model_name=r.model_pattern LEFT JOIN model_capabilities mc ON mc.model=r.model_pattern
		WHERE rm.enabled=1 AND r.enabled=1 AND c.status='enabled' ORDER BY r.model_pattern,rm.id`)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	defer rows.Close()
	items := []teamModelView{}
	index := map[string]int{}
	for rows.Next() {
		var id int64
		var item teamModelView
		if rows.Scan(&id, &item.Name, &item.Vendor, &item.Kind, &item.ContextWindow, &item.InputModalities, &item.OutputModalities, &item.Endpoints, &item.SupportsThinking) != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		if !access.AllowsGrant(id) || !access.AllowsModel(item.Name) {
			continue
		}
		if i, ok := index[item.Name]; ok {
			items[i].Candidates++
		} else {
			item.Candidates = 1
			index[item.Name] = len(items)
			items = append(items, item)
		}
	}
	if rows.Err() != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	teamJSON(w, 200, items)
}
