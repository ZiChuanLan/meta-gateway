package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Admin surface for third-party sign-in: provider credentials, the
// self-registration switch, and the per-member bindings.

// oauthProviderView is what the console sees. The secret is reported as a
// boolean — the encrypted envelope never leaves the server, and an operator
// editing other fields must not have to re-type it.
type oauthProviderView struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Enabled      bool   `json:"enabled"`
	ClientID     string `json:"client_id"`
	HasSecret    bool   `json:"has_secret"`
	AuthorizeURL string `json:"authorize_url"`
	TokenURL     string `json:"token_url"`
	UserInfoURL  string `json:"userinfo_url"`
	Scopes       string `json:"scopes"`
	CallbackURL  string `json:"callback_url"`
	// Defaults are shown next to the overridable endpoints so an operator can
	// see what they are replacing (and what to restore).
	DefaultAuthorizeURL string `json:"default_authorize_url"`
	DefaultTokenURL     string `json:"default_token_url"`
	DefaultUserInfoURL  string `json:"default_userinfo_url"`
	DefaultScopes       string `json:"default_scopes"`
}

type oauthSettingsView struct {
	AutoRegister    bool                `json:"auto_register"`
	DefaultPolicyID int64               `json:"default_policy_id"`
	Providers       []oauthProviderView `json:"providers"`
}

func (h *TeamHandler) oauthSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.oauthConfig()
	if err != nil {
		teamFail(w, 500, "settings_unavailable")
		return
	}
	view := oauthSettingsView{AutoRegister: cfg.AutoRegister, DefaultPolicyID: cfg.DefaultPolicyID, Providers: []oauthProviderView{}}
	for _, def := range oauthProviderDefs() {
		stored := cfg.Providers[def.id]
		item := oauthProviderView{
			ID:                  def.id,
			Label:               def.label,
			Enabled:             stored.Enabled,
			ClientID:            stored.ClientID,
			HasSecret:           strings.TrimSpace(stored.ClientSecretEnc) != "",
			AuthorizeURL:        strings.TrimSpace(stored.AuthorizeURL),
			TokenURL:            strings.TrimSpace(stored.TokenURL),
			UserInfoURL:         strings.TrimSpace(stored.UserInfoURL),
			Scopes:              strings.TrimSpace(stored.Scopes),
			CallbackURL:         h.oauthCallbackURL(r, def.id),
			DefaultAuthorizeURL: def.authorizeURL,
			DefaultTokenURL:     def.tokenURL,
			DefaultUserInfoURL:  def.userInfoURL,
			DefaultScopes:       def.scopes,
		}
		view.Providers = append(view.Providers, item)
	}
	teamJSON(w, 200, view)
}

func (h *TeamHandler) putOAuthSettings(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	var req struct {
		AutoRegister    bool  `json:"auto_register"`
		DefaultPolicyID int64 `json:"default_policy_id"`
		Providers       []struct {
			ID           string `json:"id"`
			Enabled      bool   `json:"enabled"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			AuthorizeURL string `json:"authorize_url"`
			TokenURL     string `json:"token_url"`
			UserInfoURL  string `json:"userinfo_url"`
			Scopes       string `json:"scopes"`
		} `json:"providers"`
	}
	if !teamRead(w, r, &req) {
		return
	}
	if req.DefaultPolicyID != 0 {
		if _, err := h.policy(req.DefaultPolicyID); err != nil {
			teamFail(w, 400, "invalid_policy")
			return
		}
	}
	existing, err := h.oauthConfig()
	if err != nil {
		teamFail(w, 500, "settings_unavailable")
		return
	}
	next := oauthConfig{AutoRegister: req.AutoRegister, DefaultPolicyID: req.DefaultPolicyID, Providers: map[string]oauthProviderConfig{}}
	for _, incoming := range req.Providers {
		if _, ok := oauthProviderByID(incoming.ID); !ok {
			teamFail(w, 400, "invalid_provider")
			return
		}
		clientID := strings.TrimSpace(incoming.ClientID)
		secret := strings.TrimSpace(incoming.ClientSecret)
		enc := existing.Providers[incoming.ID].ClientSecretEnc
		switch {
		case clientID == "":
			// No client id means "not configured at all": the stored secret goes
			// with it, so clearing the card really clears the credential.
			enc = ""
		case secret != "":
			// An empty secret means "keep what is stored", the same convention
			// the rest of the console uses for credential fields.
			wrapped, err := h.enc.Encrypt([]byte(secret))
			if err != nil {
				teamFail(w, 500, "save_failed")
				return
			}
			enc = wrapped
		}
		for _, raw := range []string{incoming.AuthorizeURL, incoming.TokenURL, incoming.UserInfoURL} {
			if !safeTeamURL(strings.TrimSpace(raw)) {
				teamFail(w, 400, "invalid_endpoint")
				return
			}
		}
		if len(clientID) > 200 || len(incoming.Scopes) > 200 {
			teamFail(w, 400, "invalid_settings")
			return
		}
		next.Providers[incoming.ID] = oauthProviderConfig{
			Enabled:         incoming.Enabled,
			ClientID:        clientID,
			ClientSecretEnc: enc,
			AuthorizeURL:    strings.TrimSpace(incoming.AuthorizeURL),
			TokenURL:        strings.TrimSpace(incoming.TokenURL),
			UserInfoURL:     strings.TrimSpace(incoming.UserInfoURL),
			Scopes:          strings.TrimSpace(incoming.Scopes),
		}
	}
	raw, _ := json.Marshal(next)
	if _, err := h.db.Exec(`UPDATE team_settings SET oauth_json=? WHERE id=1`, string(raw)); err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	h.audit(r, "team.oauth.update", 0)
	h.oauthSettings(w, r)
}

// oauthBindingView is one linked identity, as shown in the member detail.
type oauthBindingView struct {
	ID        int64  `json:"id"`
	Provider  string `json:"provider"`
	Label     string `json:"label"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	LastLogin string `json:"last_login_at"`
}

func (h *TeamHandler) userIdentities(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	rows, err := h.db.Query(`SELECT id,provider,email,name,created_at,last_login_at FROM team_identities WHERE user_id=? ORDER BY provider`, id)
	if err != nil {
		teamFail(w, 500, "load_failed")
		return
	}
	defer rows.Close()
	items := []oauthBindingView{}
	for rows.Next() {
		var item oauthBindingView
		if rows.Scan(&item.ID, &item.Provider, &item.Email, &item.Name, &item.CreatedAt, &item.LastLogin) != nil {
			teamFail(w, 500, "load_failed")
			return
		}
		item.Label = item.Provider
		if def, ok := oauthProviderByID(item.Provider); ok {
			item.Label = def.label
		}
		items = append(items, item)
	}
	teamJSON(w, 200, items)
}

// deleteIdentity unbinds one provider account. The member keeps their login
// name and password: unlinking removes a way in, not the account.
func (h *TeamHandler) deleteIdentity(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	identityID, ok := pathID(w, r, "identityID")
	if !ok {
		return
	}
	res, err := h.db.Exec(`DELETE FROM team_identities WHERE id=? AND user_id=?`, identityID, userID)
	if err != nil {
		teamFail(w, 500, "save_failed")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		teamFail(w, 404, "not_found")
		return
	}
	h.audit(r, "team.oauth.unlink", userID)
	teamJSON(w, 200, map[string]bool{"ok": true})
}

// oauthLoginOptions lists the providers a visitor can actually use. Only
// enabled, fully configured ones appear: a half-configured provider must not
// offer a button that cannot work.
func (h *TeamHandler) oauthLoginOptions() []map[string]string {
	cfg, err := h.oauthConfig()
	if err != nil {
		return nil
	}
	out := []map[string]string{}
	for _, def := range oauthProviderDefs() {
		stored, present := cfg.Providers[def.id]
		if !present || !stored.Enabled || strings.TrimSpace(stored.ClientID) == "" || strings.TrimSpace(stored.ClientSecretEnc) == "" {
			continue
		}
		label := def.label
		if def.id == "linuxdo" {
			label = "Linux.do"
		}
		out = append(out, map[string]string{"id": def.id, "label": label})
	}
	return out
}

// identitiesFor is used by the member list so a row can show, at a glance,
// whether that account has a third-party login attached.
func (h *TeamHandler) identityCounts() map[int64]int {
	counts := map[int64]int{}
	rows, err := h.db.Query(`SELECT user_id,count(*) FROM team_identities GROUP BY user_id`)
	if err != nil {
		return counts
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var count int
		if rows.Scan(&id, &count) != nil {
			return counts
		}
		counts[id] = count
	}
	return counts
}
