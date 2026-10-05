package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// oauthMockProvider is a stand-in for GitHub/Linux.do: one token endpoint and
// one userinfo endpoint, whose URLs the config points at so the flow can be
// driven end to end without touching the internet.
type oauthMockProvider struct {
	server   *httptest.Server
	subject  string
	login    string
	name     string
	avatar   string
	email    string
	tokenHit int
}

func newOAuthMockProvider(t *testing.T) *oauthMockProvider {
	t.Helper()
	mock := &oauthMockProvider{subject: "4242", login: "octocat", name: "Octo Cat", avatar: "https://example.test/a.png", email: "octo@example.test"}
	mock.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			mock.tokenHit++
			_ = r.ParseForm()
			if r.Form.Get("code") == "" {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"mock-token","token_type":"bearer"}`))
		case "/user":
			if r.Header.Get("Authorization") != "Bearer mock-token" {
				http.Error(w, `{"message":"bad credentials"}`, http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": mock.subject, "login": mock.login, "name": mock.name,
				"avatar_url": mock.avatar, "email": mock.email,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mock.server.Close)
	return mock
}

// oauthBrowser is an HTTP client that keeps cookies and does not follow
// redirects, so the flow's Location headers and Set-Cookie values are visible.
func oauthBrowser(t *testing.T, env *teamTestEnv) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func configureOAuth(t *testing.T, e *teamTestEnv, mock *oauthMockProvider, autoRegister bool, defaultPolicy int64) {
	t.Helper()
	e.admin("PUT", "/admin/team/oauth", map[string]any{
		"auto_register":     autoRegister,
		"default_policy_id": defaultPolicy,
		"providers": []map[string]any{{
			"id":            "github",
			"enabled":       true,
			"client_id":     "mock-client",
			"client_secret": "mock-secret",
			"authorize_url": mock.server.URL + "/authorize",
			"token_url":     mock.server.URL + "/token",
			"userinfo_url":  mock.server.URL + "/user",
		}},
	}, 200)
}

// startOAuth runs the start endpoint and returns the state the provider would
// have echoed back.
func startOAuth(t *testing.T, e *teamTestEnv, client *http.Client) string {
	t.Helper()
	code, _, header := e.call(client, "GET", "/auth/oauth/github/start", nil, "", "")
	if code != http.StatusFound {
		t.Fatalf("start = %d, want 302", code)
	}
	location := header.Get("Location")
	if !strings.Contains(location, "state=") || !strings.Contains(location, "code_challenge=") {
		t.Fatalf("authorize redirect is missing state/PKCE: %s", location)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query().Get("state")
}

func callbackOAuth(t *testing.T, e *teamTestEnv, client *http.Client, state, code string) (int, string) {
	t.Helper()
	target := "/auth/oauth/github/callback?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	status, _, header := e.call(client, "GET", target, nil, "", "")
	return status, header.Get("Location")
}

func TestOAuthLoginCreatesAndReusesMember(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	mock := newOAuthMockProvider(t)
	configureOAuth(t, e, mock, true, 1)

	// The login page only offers fully configured providers.
	options := e.admin("GET", "/auth/options", nil, 200)
	var advertised struct {
		OAuth []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"oauth"`
	}
	json.Unmarshal(options, &advertised)
	if len(advertised.OAuth) != 1 || advertised.OAuth[0].ID != "github" || advertised.OAuth[0].Label != "GitHub" {
		t.Fatalf("advertised providers = %+v", advertised.OAuth)
	}

	client := oauthBrowser(t, e)
	state := startOAuth(t, e, client)
	status, location := callbackOAuth(t, e, client, state, "auth-code-1")
	if status != http.StatusFound || location != "/app" {
		t.Fatalf("callback = %d %s, want 302 /app", status, location)
	}
	// The session cookie must already work.
	users := teamUserRows(t, e)
	var createdID int64
	for _, user := range users {
		if user.Username == "octocat" {
			createdID = user.ID
		}
	}
	if createdID == 0 {
		t.Fatalf("no account created for the provider identity: %+v", users)
	}
	me, _, _ := e.call(client, "GET", "/me", nil, "", "")
	if me != 200 {
		t.Fatalf("session from the OAuth callback is not usable: %d", me)
	}
	bindings := e.admin("GET", "/admin/team/users/"+itoa64(createdID)+"/identities", nil, 200)
	var identities []oauthBindingView
	json.Unmarshal(bindings, &identities)
	if len(identities) != 1 || identities[0].Provider != "github" || identities[0].Label != "GitHub" {
		t.Fatalf("bindings = %+v", identities)
	}
	if identities[0].Email != "octo@example.test" {
		t.Fatalf("binding lost the provider email: %+v", identities[0])
	}

	// Signing in again reuses the account instead of creating a second one.
	state = startOAuth(t, e, client)
	if status, _ := callbackOAuth(t, e, client, state, "auth-code-2"); status != http.StatusFound {
		t.Fatalf("second callback = %d", status)
	}
	names := []string{}
	for _, user := range teamUserRows(t, e) {
		if strings.HasPrefix(user.Username, "octocat") {
			names = append(names, user.Username)
		}
	}
	if len(names) != 1 {
		t.Fatalf("second login created another account: %+v", names)
	}
	if mock.tokenHit < 2 {
		t.Fatalf("token endpoint hit %d times, want at least 2", mock.tokenHit)
	}
}

func TestOAuthRequiresConfiguredSelfRegistration(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	mock := newOAuthMockProvider(t)
	// A policy with no route grants: usable, but not what a member lands on by
	// default.
	e.admin("PUT", "/admin/team/policies/1", TeamPolicy{ID: 1, Name: "Default", Models: []string{}, MemberIDs: []int64{}, MaxKeys: 5, RPM: 120}, 200)
	configureOAuth(t, e, mock, false, 1)

	client := oauthBrowser(t, e)
	state := startOAuth(t, e, client)
	status, location := callbackOAuth(t, e, client, state, "auth-code-1")
	if status != http.StatusFound || !strings.Contains(location, "oauth_error=oauth_register_disabled") {
		t.Fatalf("unconfigured self-registration = %d %s", status, location)
	}
	for _, user := range teamUserRows(t, e) {
		if user.Username == "octocat" {
			t.Fatal("an account was created although self-registration is off")
		}
	}
}

func TestOAuthRejectsForgedState(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	mock := newOAuthMockProvider(t)
	configureOAuth(t, e, mock, true, 1)

	client := oauthBrowser(t, e)
	startOAuth(t, e, client)
	// The state the provider echoes back is what proves the callback belongs to
	// a flow this browser started.
	status, location := callbackOAuth(t, e, client, "not-the-state", "auth-code-1")
	if status != http.StatusFound || !strings.Contains(location, "oauth_error=oauth_state_invalid") {
		t.Fatalf("forged state = %d %s", status, location)
	}
	if len(teamUserRows(t, e)) != 1 { // owner only
		t.Fatal("a forged state still produced an account")
	}
}

func TestOAuthRejectsPausedAccount(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	mock := newOAuthMockProvider(t)
	configureOAuth(t, e, mock, true, 1)

	client := oauthBrowser(t, e)
	state := startOAuth(t, e, client)
	callbackOAuth(t, e, client, state, "auth-code-1")
	var created int64
	for _, user := range teamUserRows(t, e) {
		if user.Username == "octocat" {
			created = user.ID
		}
	}
	if created == 0 {
		t.Fatal("account was not created")
	}
	// Pausing the member stops the third-party login as well. A fresh browser
	// keeps this test inside the login rate limit.
	e.admin("PATCH", "/admin/team/users/"+itoa64(created), map[string]any{"status": "paused"}, 200)
	fresh := oauthBrowser(t, e)
	state = startOAuth(t, e, fresh)
	status, location := callbackOAuth(t, e, fresh, state, "auth-code-2")
	if !strings.Contains(location, "oauth_error=oauth_account_paused") {
		t.Fatalf("paused account login = %d %s", status, location)
	}
}

func TestOAuthSettingsKeepSecretsAndUnlinkIdentities(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	mock := newOAuthMockProvider(t)
	configureOAuth(t, e, mock, true, 1)

	// The secret is never echoed; an update that leaves it blank keeps it.
	view := e.admin("GET", "/admin/team/oauth", nil, 200)
	if strings.Contains(string(view), "mock-secret") {
		t.Fatal("the provider secret was returned to the console")
	}
	var settings oauthSettingsView
	json.Unmarshal(view, &settings)
	var github *oauthProviderView
	for index := range settings.Providers {
		if settings.Providers[index].ID == "github" {
			github = &settings.Providers[index]
		}
	}
	if github == nil {
		t.Fatalf("github provider missing from the settings view: %+v", settings)
	}
	if !github.HasSecret || github.ClientID != "mock-client" {
		t.Fatalf("github provider view = %+v", *github)
	}
	if github.CallbackURL == "" || !strings.HasSuffix(github.CallbackURL, "/auth/oauth/github/callback") {
		t.Fatalf("callback url = %q", github.CallbackURL)
	}
	e.admin("PUT", "/admin/team/oauth", map[string]any{
		"auto_register": true, "default_policy_id": 1,
		"providers": []map[string]any{{
			"id": "github", "enabled": true, "client_id": "mock-client",
			"authorize_url": mock.server.URL + "/authorize",
			"token_url":     mock.server.URL + "/token",
			"userinfo_url":  mock.server.URL + "/user",
		}},
	}, 200)
	view = e.admin("GET", "/admin/team/oauth", nil, 200)
	json.Unmarshal(view, &settings)
	for _, provider := range settings.Providers {
		if provider.ID == "github" && !provider.HasSecret {
			t.Fatal("a blank secret field wiped the stored credential")
		}
	}

	client := oauthBrowser(t, e)
	state := startOAuth(t, e, client)
	callbackOAuth(t, e, client, state, "auth-code-1")
	var created int64
	for _, user := range teamUserRows(t, e) {
		if user.Username == "octocat" {
			created = user.ID
		}
	}
	var identities []oauthBindingView
	json.Unmarshal(e.admin("GET", "/admin/team/users/"+itoa64(created)+"/identities", nil, 200), &identities)
	if len(identities) != 1 {
		t.Fatalf("bindings before unlink = %+v", identities)
	}
	e.admin("DELETE", "/admin/team/users/"+itoa64(created)+"/identities/"+itoa64(identities[0].ID), nil, 200)
	json.Unmarshal(e.admin("GET", "/admin/team/users/"+itoa64(created)+"/identities", nil, 200), &identities)
	if len(identities) != 0 {
		t.Fatalf("bindings after unlink = %+v", identities)
	}
	// Unlinking removes a way in, not the account.
	if userByID(t, e, created) == nil {
		t.Fatal("unlinking deleted the account")
	}

	// Clearing the client id is the console's "remove this provider" action:
	// the stored secret goes with it, so nothing is left behind to be reused.
	e.admin("PUT", "/admin/team/oauth", map[string]any{
		"auto_register": false, "default_policy_id": 0,
		"providers": []map[string]any{{"id": "github", "enabled": false}},
	}, 200)
	json.Unmarshal(e.admin("GET", "/admin/team/oauth", nil, 200), &settings)
	for _, provider := range settings.Providers {
		if provider.ID == "github" && (provider.HasSecret || provider.ClientID != "" || provider.Enabled) {
			t.Fatalf("clearing the client id left the provider configured: %+v", provider)
		}
	}
	options := e.admin("GET", "/auth/options", nil, 200)
	if strings.Contains(string(options), "github") {
		t.Fatal("a cleared provider is still offered on the login page")
	}
}

func itoa64(value int64) string { return strconv.FormatInt(value, 10) }
