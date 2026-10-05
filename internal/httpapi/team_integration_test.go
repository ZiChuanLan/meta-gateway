package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

type teamTestEnv struct {
	server *httptest.Server
	db     *store.DB
	enc    *crypto.Encrypter
	t      *testing.T
}
type teamTestBrowser struct {
	env    *teamTestEnv
	client *http.Client
	csrf   string
	userID int64
}

func newTeamTestEnv(t *testing.T) *teamTestEnv {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("team-integration-master-key")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewTestRouter(t, &config.Config{AdminToken: "team-admin-secret", MetricsToken: "metrics", OutboundAllowCIDRs: []string{"127.0.0.0/8"}, RetryTimes: 2}, db, enc))
	t.Cleanup(server.Close)
	return &teamTestEnv{server, db, enc, t}
}
func (e *teamTestEnv) call(client *http.Client, method, path string, body any, bearer, csrf string) (int, []byte, http.Header) {
	e.t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, e.server.URL+path, bytes.NewReader(raw))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Origin", e.server.URL)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if csrf != "" {
		req.Header.Set("X-Meta-CSRF", csrf)
	}
	resp, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp.StatusCode, data, resp.Header
}
func (e *teamTestEnv) admin(method, path string, body any, want int) []byte {
	e.t.Helper()
	code, data, _ := e.call(http.DefaultClient, method, path, body, "team-admin-secret", "")
	if code != want {
		e.t.Fatalf("%s %s: %d, want %d: %s", method, path, code, want, data)
	}
	return data
}
func (e *teamTestEnv) enable() {
	e.t.Helper()
	e.admin("POST", "/admin/team/bootstrap", map[string]any{"username": "owner", "name": "Owner", "password": "owner-password-123"}, 201)
	e.setMode("team")
}
func (e *teamTestEnv) setMode(mode string) {
	e.admin("PATCH", "/admin/mode", map[string]string{"mode": mode}, 200)
}
func (e *teamTestEnv) browser() *teamTestBrowser {
	jar, _ := cookiejar.New(nil)
	b := &teamTestBrowser{env: e, client: &http.Client{Jar: jar}}
	var body struct{ CSRF string }
	code, data, _ := e.call(b.client, "GET", "/auth/options", nil, "", "")
	if code != 200 {
		e.t.Fatalf("options %d: %s", code, data)
	}
	json.Unmarshal(data, &body)
	b.csrf = body.CSRF
	return b
}
func (b *teamTestBrowser) request(method, path string, body any, want int) []byte {
	b.env.t.Helper()
	code, data, _ := b.env.call(b.client, method, path, body, "", b.csrf)
	if code != want {
		b.env.t.Fatalf("%s %s: %d, want %d: %s", method, path, code, want, data)
	}
	return data
}
func (e *teamTestEnv) member(name, role string) *teamTestBrowser {
	e.t.Helper()
	raw := e.admin("POST", "/admin/team/invitations", map[string]any{"label": name, "policy_id": 1, "role": role}, 201)
	var invite struct{ Path string }
	json.Unmarshal(raw, &invite)
	token := strings.Split(invite.Path, "invite=")[1]
	b := e.browser()
	raw = b.request("POST", "/auth/accept", map[string]any{"username": name, "name": name, "password": "member-password-123", "token": token}, 200)
	var result struct {
		User TeamUser
		CSRF string
	}
	json.Unmarshal(raw, &result)
	b.csrf = result.CSRF
	b.userID = result.User.ID
	return b
}
func (b *teamTestBrowser) login(name, password string) {
	opts := b.request("GET", "/auth/options", nil, 200)
	var o struct{ CSRF string }
	json.Unmarshal(opts, &o)
	b.csrf = o.CSRF
	raw := b.request("POST", "/auth/login", map[string]string{"username": name, "password": password}, 200)
	var r struct {
		User TeamUser
		CSRF string
	}
	json.Unmarshal(raw, &r)
	b.csrf = r.CSRF
	b.userID = r.User.ID
}
func (b *teamTestBrowser) key(name string) (int64, string) {
	raw := b.request("POST", "/me/keys", teamKeyInput{Name: name}, 201)
	var k struct {
		ID    int64
		Token string
	}
	json.Unmarshal(raw, &k)
	return k.ID, k.Token
}
func TestTeamModeLifecycleAndRoles(t *testing.T) {
	e := newTeamTestEnv(t)
	// /app was the member app; there is one console now, so its paths redirect
	// there — carrying the rest of the path and the query, which is what keeps
	// invitation and recovery links working. A client that follows redirects
	// would land on the console and hide the difference, so this one does not.
	noFollow := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for path, want := range map[string]string{
		"/app":      "/console",
		"/app/keys": "/console/keys",
	} {
		code, _, headers := e.call(noFollow, "GET", path, nil, "", "")
		if code != http.StatusTemporaryRedirect {
			t.Fatalf("personal %s=%d, want a redirect", path, code)
		}
		if got := headers.Get("Location"); got != want {
			t.Fatalf("personal %s -> %q, want %q", path, got, want)
		}
	}
	// The rest of the team surface is still absent while the mode is personal.
	for _, path := range []string{"/auth/options", "/me", "/me/keys"} {
		code, _, _ := e.call(http.DefaultClient, "GET", path, nil, "", "")
		if code != 404 {
			t.Fatalf("personal %s=%d", path, code)
		}
	}
	e.enable()
	e.admin("POST", "/admin/team/bootstrap", map[string]any{"username": "other-owner", "name": "Other", "password": "owner-password-123"}, 409)
	member := e.member("alice", "member")
	admin := e.member("manager", "admin")
	member.request("GET", "/admin/team/settings", nil, 403)
	member.request("GET", "/admin/sites", nil, 403)
	admin.request("GET", "/admin/team/users", nil, 200)
	admin.request("GET", "/admin/sites", nil, 403)
	admin.request("PUT", "/admin/team/settings", TeamSettings{Mode: "personal"}, 403)
	admin.request("POST", "/admin/team/invitations", map[string]any{"label": "escalation", "policy_id": 1, "role": "admin"}, 403)
	admin.request("PATCH", fmt.Sprintf("/admin/team/users/%d", member.userID), map[string]string{"role": "admin"}, 403)
	code, _, _ := e.call(member.client, "GET", "/admin/team/settings", nil, "invalid-explicit-token", member.csrf)
	if code != 401 {
		t.Fatal("invalid explicit bearer fell back to cookie")
	}
	id, token := member.key("personal-key")
	member.request("GET", fmt.Sprintf("/admin/downstream-keys/%d/reveal", id), nil, 403)
	e.setMode("personal")
	member.request("GET", "/me", nil, 404)
	code, _, _ = e.call(http.DefaultClient, "GET", "/v1/models", nil, token, "")
	if code != 401 {
		t.Fatalf("team key bypassed personal mode: %d", code)
	}
	var n int
	if e.db.QueryRow(`SELECT count(*) FROM team_users`).Scan(&n) != nil || n != 3 {
		t.Fatal("mode switch removed users")
	}
	e.setMode("team")
	member.request("GET", "/me", nil, 401)
	member.login("alice", "member-password-123")
	member.request("GET", "/me/keys", nil, 200)
}
func TestTeamOwnershipCSRFAndKeyLifecycle(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	a := e.member("alice", "member")
	b := e.member("bob", "member")
	id, raw := a.key("private")
	for _, op := range []struct {
		method, suffix string
		body           any
	}{
		{"POST", "/reveal", map[string]any{}}, {"POST", "/rotate", map[string]any{}}, {"PATCH", "", teamKeyInput{Name: "stolen"}}, {"DELETE", "", nil},
	} {
		b.request(op.method, fmt.Sprintf("/me/keys/%d%s", id, op.suffix), op.body, 404)
	}
	b.request("POST", "/me/keys", map[string]any{"name": "evil", "user_id": a.userID}, 400)
	code, _, _ := e.call(a.client, "POST", "/me/keys", teamKeyInput{Name: "csrf"}, "", "")
	if code != 403 {
		t.Fatal("missing csrf accepted")
	}
	req, _ := http.NewRequest("POST", e.server.URL+"/me/keys", strings.NewReader(`{"name":"evil"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("X-Meta-CSRF", a.csrf)
	response, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-origin accepted")
	}
	a.request("GET", "/me/keys", nil, 200)
	list := a.request("GET", "/me/keys", nil, 200)
	if bytes.Contains(list, []byte(raw)) || bytes.Contains(list, []byte("password_hash")) {
		t.Fatal("list leaked secret")
	}
	_, _, headers := e.call(a.client, "POST", fmt.Sprintf("/me/keys/%d/reveal", id), map[string]any{}, "", a.csrf)
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatal("reveal cacheable")
	}
	var result struct{ Token string }
	json.Unmarshal(a.request("POST", fmt.Sprintf("/me/keys/%d/rotate", id), map[string]any{}, 200), &result)
	code, _, _ = e.call(http.DefaultClient, "GET", "/v1/models", nil, raw, "")
	if code != 401 {
		t.Fatal("rotated key still valid")
	}
	if result.Token == raw {
		t.Fatal("rotation reused key")
	}
	a.request("DELETE", fmt.Sprintf("/me/keys/%d", id), nil, 200)
	code, _, _ = e.call(http.DefaultClient, "GET", "/v1/models", nil, result.Token, "")
	if code != 401 {
		t.Fatal("deleted key still valid")
	}
	k, err := e.db.DownstreamKey.GetByID(id)
	if err != nil || k == nil || k.TeamDeletedAt == "" {
		t.Fatal("key tombstone missing")
	}
	if string(k.TokenEnc) != "" {
		t.Fatal("deleted secret retained")
	}
	code, _, _ = e.call(http.DefaultClient, "GET", "/me/keys", nil, result.Token, "")
	if code != 401 {
		t.Fatal("API key became account session")
	}
	a.request("POST", "/me/logout", map[string]any{}, 200)
	a.request("GET", "/me", nil, 401)
}
func TestTeamConcurrentKeyLimitAndInviteSingleUse(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	b := e.member("alice", "member")
	e.admin("PUT", "/admin/team/policies/1", TeamPolicy{ID: 1, Name: "Limited", MaxKeys: 2, RPM: 60, Models: []string{}, MemberIDs: []int64{}}, 200)
	var successes atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _ := e.call(b.client, "POST", "/me/keys", teamKeyInput{Name: "parallel"}, "", b.csrf)
			if code == 201 {
				successes.Add(1)
			} else if code != 409 {
				t.Errorf("unexpected create status %d", code)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 2 {
		t.Fatalf("concurrent count=%d, want 2", successes.Load())
	}
	raw := e.admin("POST", "/admin/team/invitations", map[string]any{"policy_id": 1}, 201)
	var inv struct{ Path string }
	json.Unmarshal(raw, &inv)
	c := e.browser()
	body := map[string]any{"token": strings.Split(inv.Path, "invite=")[1], "username": "new-person", "name": "New", "password": "member-password-123"}
	c.request("POST", "/auth/accept", body, 200)
	c2 := e.browser()
	body["username"] = "another-person"
	c2.request("POST", "/auth/accept", body, 400)
}
func TestTeamRoutingGrantRevocationAndUsageOwnership(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	var hitA, hitB atomic.Int64
	upstream := func(hit *atomic.Int64) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit.Add(1)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"test","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
		}))
		t.Cleanup(s.Close)
		return s
	}
	aUp, bUp := upstream(&hitA), upstream(&hitB)
	secret, _ := e.enc.Encrypt([]byte("upstream-only-secret"))
	site, _ := e.db.Site.Create(&domain.Site{Name: "Upstream", Status: domain.StatusEnabled})
	cred, _ := e.db.Credential.Create(&domain.Credential{SiteID: site, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	channel := func(name, url string) int64 {
		id, err := e.db.Channel.Create(&domain.Channel{SiteID: &site, CredentialID: &cred, Name: name, BaseURL: url, TypeHint: "openai", Status: domain.StatusEnabled})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	route, _ := e.db.Route.Create(&domain.Route{ModelPattern: "team-model", Enabled: true})
	am, _ := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel("A", aUp.URL), Priority: 20, Weight: 100, Enabled: true})
	bm, _ := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel("B", bUp.URL), Priority: 10, Weight: 100, Enabled: true})
	p := TeamPolicy{ID: 1, Name: "Granted", Models: []string{"team-model"}, MemberIDs: []int64{am, bm}, MaxKeys: 5, RPM: 120, AllowRouting: true}
	e.admin("PUT", "/admin/team/policies/1", p, 200)
	alice := e.member("alice", "member")
	bob := e.member("bob", "member")
	kid, token := alice.key("app")
	// Arrange the model the way the model page does: the array IS the order.
	arrange := func(entries ...domain.TeamRouteEntry) {
		alice.request("PUT", "/me/routes/team-model", teamRouteOrderInput{Entries: entries}, 200)
	}
	call := func() int {
		code, _, _ := e.call(http.DefaultClient, "POST", "/v1/chat/completions", map[string]any{"model": "team-model", "messages": []any{map[string]string{"role": "user", "content": "hello"}}}, token, "")
		return code
	}
	// Site order puts A (priority 20) ahead of B (priority 10).
	if got := call(); got != 200 {
		t.Fatalf("forward=%d", got)
	}
	if hitA.Load() != 1 || hitB.Load() != 0 {
		t.Fatalf("site order not used before any arrangement: A=%d B=%d", hitA.Load(), hitB.Load())
	}
	// Nothing bound this key to a plan: the arrangement still applies, which is
	// what makes "my order" work without configuring every key.
	arrange(domain.TeamRouteEntry{ID: bm, Weight: 100}, domain.TeamRouteEntry{ID: am, Weight: 100})
	if got := call(); got != 200 {
		t.Fatalf("arranged forward=%d", got)
	}
	if hitB.Load() != 1 {
		t.Fatalf("personal order did not select B first: A=%d B=%d", hitA.Load(), hitB.Load())
	}
	// Swapping the order swaps the first choice, with no priority number typed.
	arrange(domain.TeamRouteEntry{ID: am, Weight: 100}, domain.TeamRouteEntry{ID: bm, Weight: 100})
	if got := call(); got != 200 || hitA.Load() != 2 {
		t.Fatalf("reordered forward=%d A=%d", got, hitA.Load())
	}
	// A switched-off upstream is out of routing but stays in the list.
	arrange(domain.TeamRouteEntry{ID: am, Weight: 100, Disabled: true}, domain.TeamRouteEntry{ID: bm, Weight: 100})
	if got := call(); got != 200 || hitB.Load() != 2 {
		t.Fatalf("disabled upstream still used: code=%d B=%d", got, hitB.Load())
	}
	// A plan made on the fly is the user's default one.
	var plans []teamPlan
	json.Unmarshal(alice.request("GET", "/me/plans", nil, 200), &plans)
	if len(plans) != 1 || !plans[0].Default || len(plans[0].Routes["team-model"]) != 2 {
		t.Fatalf("default plan not created as expected: %+v", plans)
	}
	bob.request("PUT", fmt.Sprintf("/me/plans/%d", plans[0].ID), teamPlan{Name: "stolen"}, 404)
	bob.request("POST", "/me/keys", teamKeyInput{Name: "stolen-plan", PlanID: plans[0].ID}, 403)
	// Restoring the site order drops the arrangement entirely.
	alice.request("DELETE", "/me/routes/team-model", nil, 200)
	if got := call(); got != 200 || hitA.Load() != 3 {
		t.Fatalf("restore site order: code=%d A=%d", got, hitA.Load())
	}
	var ownerID int64
	var tokens int
	if err := e.db.QueryRow(`SELECT user_id,total_tokens FROM usage_records WHERE downstream_key_id=?`, kid).Scan(&ownerID, &tokens); err != nil || ownerID != alice.userID || tokens != 3 {
		t.Fatalf("usage ownership %d %d %v", ownerID, tokens, err)
	}
	records := alice.request("GET", "/me/requests", nil, 200)
	if !bytes.Contains(records, []byte(`"tokens":3`)) {
		t.Fatalf("request usage %s", records)
	}
	if string(bob.request("GET", "/me/requests", nil, 200)) != "[]\n" {
		t.Fatal("another user's requests leaked")
	}
	// A grant narrowed after the arrangement was saved still wins: the
	// arrangement names B, but the policy no longer grants it.
	arrange(domain.TeamRouteEntry{ID: am, Weight: 100}, domain.TeamRouteEntry{ID: bm, Weight: 100})
	p.MemberIDs = []int64{am}
	e.admin("PUT", "/admin/team/policies/1", p, 200)
	if got := call(); got != 200 || hitA.Load() != 4 {
		t.Fatalf("revoked member used: code=%d A=%d", got, hitA.Load())
	}
	// Revoking A as well leaves the arrangement with nothing callable.
	p.MemberIDs = []int64{}
	e.admin("PUT", "/admin/team/policies/1", p, 200)
	if got := call(); got == 200 {
		t.Fatal("empty authorization intersection remained callable")
	}
	e.admin("PATCH", fmt.Sprintf("/admin/team/users/%d", alice.userID), map[string]string{"status": "paused"}, 200)
	if got := call(); got != 401 {
		t.Fatalf("paused user key=%d", got)
	}
}

func TestTeamPasswordRecoveryRevokesSessions(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	b := e.member("alice", "member")
	raw := e.admin("POST", fmt.Sprintf("/admin/team/users/%d/recovery", b.userID), map[string]any{}, 201)
	var recovery struct{ Path string }
	json.Unmarshal(raw, &recovery)
	other := e.browser()
	other.request("POST", "/auth/recover", map[string]string{"token": strings.Split(recovery.Path, "recovery=")[1], "password": "replacement-password-123"}, 200)
	b.request("GET", "/me", nil, 401)
	b.login("alice", "replacement-password-123")
	b.request("POST", "/me/password", map[string]string{"current_password": "wrong", "password": "another-password-123"}, 403)
	b.request("POST", "/me/password", map[string]string{"current_password": "replacement-password-123", "password": "another-password-123"}, 200)
	b.request("GET", "/me", nil, 401)
}

func TestTeamLegacyPersonalKeyRemainsIndependent(t *testing.T) {
	e := newTeamTestEnv(t)
	hash, raw, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.db.DownstreamKey.Create(&domain.DownstreamKey{Name: "legacy", TokenHash: hash, Enabled: true, Scopes: "relay"})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"personal", "team", "personal"} {
		if mode == "team" {
			e.enable()
		} else {
			e.setMode(mode)
		}
		code, _, _ := e.call(http.DefaultClient, "GET", "/v1/models", nil, raw, "")
		if code != 200 {
			t.Fatalf("legacy key in %s=%d", mode, code)
		}
	}
	// Cookie controls are explicit, and a same-host HTTPS origin strengthens Secure.
	h := NewTeamHandler(e.db, e.enc)
	r := httptest.NewRequest("POST", "http://gateway.example/auth/login", nil)
	r.Header.Set("Origin", "https://gateway.example")
	w := httptest.NewRecorder()
	h.cookie(w, r, teamCookie, "example", int(time.Hour.Seconds()))
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatal("session cookie missing protections")
	}
}
