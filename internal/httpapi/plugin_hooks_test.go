package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/plugins"
	"github.com/lan/meta-gateway/internal/store"
)

// Plugin intercept hooks, end to end over real HTTP: a fake sidecar plugin
// answers the hook protocol, a fake upstream records what it received, and the
// assertions are about what actually crossed the wire.
//
// What these tests protect, in order of how easy it would be to break silently:
//
//  1. A route hook rewrites the model BEFORE selection, so a client can send a
//     virtual name ("auto") that no route matches.
//  2. A broken plugin cannot break a request (fail-open), and its failures do
//     not turn into upstream faults.
//  3. A plugin's own nested call is never intercepted by that plugin again.
//  4. Request hooks rewrite the outbound body; response hooks rewrite the body
//     the client receives.

// hookPluginConfig configures the fake plugin's behavior. It is read-only once
// the server starts, which is what keeps the handlers race-free.
type hookPluginConfig struct {
	ID string
	// MatchModels is the route hook's declaration (default: ["auto"]).
	MatchModels []string
	// RouteModel is what the route hook answers with ("" = decline).
	RouteModel string
	// RouteStatus forces a non-200 hook response (failure injection).
	RouteStatus int
	// RouteDelay makes the route hook slower than its own timeout.
	RouteDelay time.Duration
	// DeclareRequest / DeclareResponse add the later points to the manifest.
	DeclareRequest  bool
	DeclareResponse bool
	// RequestBody replaces the upstream body at the request hook ("" = decline).
	RequestBody string
	// ResponseBody replaces the client-visible body at the response hook.
	ResponseBody string
	// Permissions overrides the manifest permissions (default: intercept).
	Permissions []string
	// OmitHooks declares no hooks at all (a plain sidecar).
	OmitHooks bool
}

// hookPluginServer is the fake plugin.
type hookPluginServer struct {
	*httptest.Server
	cfg hookPluginConfig

	routeCalls    atomic.Int64
	requestCalls  atomic.Int64
	responseCalls atomic.Int64

	mu             sync.Mutex
	lastRouteInput map[string]any
	lastReqInput   map[string]any
	lastRespInput  map[string]any
}

func (p *hookPluginServer) counts() (route, request, response int64) {
	return p.routeCalls.Load(), p.requestCalls.Load(), p.responseCalls.Load()
}

func (p *hookPluginServer) routeInput() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastRouteInput
}

func (p *hookPluginServer) requestInput() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastReqInput
}

func (p *hookPluginServer) responseInput() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastRespInput
}

func writeHookJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (p *hookPluginServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/plugin.json":
		manifest := map[string]any{
			"id":      p.cfg.ID,
			"version": "1.0.0",
			"name":    "Hook Test Plugin",
		}
		permissions := p.cfg.Permissions
		if permissions == nil {
			permissions = []string{plugins.InterceptPermission}
		}
		manifest["permissions"] = permissions
		if !p.cfg.OmitHooks {
			matchModels := p.cfg.MatchModels
			if matchModels == nil {
				matchModels = []string{"auto"}
			}
			hooks := map[string]any{
				"route": map[string]any{
					"path":         "/hooks/route",
					"match_models": matchModels,
					"timeout_ms":   500,
				},
			}
			if p.cfg.DeclareRequest {
				hooks["request"] = map[string]any{
					"path":         "/hooks/request",
					"match_models": []string{"gpt-real"},
					"timeout_ms":   500,
				}
			}
			if p.cfg.DeclareResponse {
				hooks["response"] = map[string]any{
					"path":         "/hooks/response",
					"match_models": []string{"gpt-real"},
					"timeout_ms":   500,
				}
			}
			manifest["hooks"] = hooks
		}
		writeHookJSON(w, manifest)
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	case "/hooks/route":
		p.routeCalls.Add(1)
		p.storeInput(r, &p.lastRouteInput)
		if p.cfg.RouteDelay > 0 {
			time.Sleep(p.cfg.RouteDelay)
		}
		if p.cfg.RouteStatus != 0 && p.cfg.RouteStatus != http.StatusOK {
			w.WriteHeader(p.cfg.RouteStatus)
			return
		}
		if p.cfg.RouteModel == "" {
			writeHookJSON(w, map[string]any{"handled": false, "reason": "no opinion"})
			return
		}
		writeHookJSON(w, map[string]any{
			"handled":    true,
			"model":      p.cfg.RouteModel,
			"reason":     "test routing decision",
			"confidence": 0.87,
		})
	case "/hooks/request":
		p.requestCalls.Add(1)
		p.storeInput(r, &p.lastReqInput)
		if p.cfg.RequestBody == "" {
			writeHookJSON(w, map[string]any{"handled": false})
			return
		}
		writeHookJSON(w, map[string]any{
			"handled": true,
			"body":    json.RawMessage(p.cfg.RequestBody),
			"reason":  "rewrote the upstream request",
		})
	case "/hooks/response":
		p.responseCalls.Add(1)
		p.storeInput(r, &p.lastRespInput)
		if p.cfg.ResponseBody == "" {
			writeHookJSON(w, map[string]any{"handled": false})
			return
		}
		writeHookJSON(w, map[string]any{
			"handled": true,
			"body":    json.RawMessage(p.cfg.ResponseBody),
			"reason":  "rewrote the answer",
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (p *hookPluginServer) storeInput(r *http.Request, slot *map[string]any) {
	raw, _ := io.ReadAll(r.Body)
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return
	}
	p.mu.Lock()
	*slot = decoded
	p.mu.Unlock()
}

func newHookPlugin(t *testing.T, cfg hookPluginConfig) *hookPluginServer {
	t.Helper()
	plugin := &hookPluginServer{cfg: cfg}
	plugin.Server = httptest.NewServer(http.HandlerFunc(plugin.handle))
	t.Cleanup(plugin.Server.Close)
	return plugin
}

// echoUpstream answers any chat completion and records what it was asked for.
type echoUpstream struct {
	*httptest.Server
	mu     sync.Mutex
	models []string
	bodies []string
}

func newEchoUpstream(t *testing.T) *echoUpstream {
	t.Helper()
	upstream := &echoUpstream{}
	upstream.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &payload)
		upstream.mu.Lock()
		upstream.models = append(upstream.models, payload.Model)
		upstream.bodies = append(upstream.bodies, string(raw))
		upstream.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"cmpl-1","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"upstream answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`, payload.Model)
	}))
	t.Cleanup(upstream.Server.Close)
	return upstream
}

func (u *echoUpstream) snapshot() (models []string, bodies []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.models...), append([]string(nil), u.bodies...)
}

func (u *echoUpstream) lastModel() string {
	models, _ := u.snapshot()
	if len(models) == 0 {
		return ""
	}
	return models[len(models)-1]
}

// setupPluginGateway boots a gateway whose plugin host is wired in, seeds one
// channel + one route for "gpt-real", registers the fake plugin, and returns
// the server URL plus a downstream token.
func setupPluginGateway(t *testing.T, upstreamURL string, plugin *hookPluginServer) (string, string) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("plugin-hook-test-master-key-32-chars!!")
	if err != nil {
		t.Fatal(err)
	}
	pluginService, err := plugins.NewService(filepath.Join(dataDir, "plugins"), db.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AdminToken:                  "admin-test",
		AdminTokens:                 []string{"admin-test"},
		MetricsToken:                "metrics-test",
		BackupDir:                   filepath.Join(dataDir, "backups"),
		MaxAdminBodyBytes:           1 << 20,
		AuditRetentionDays:          90,
		AuditRetentionRows:          100000,
		OutboundAllowCIDRs:          []string{"127.0.0.1/32"},
		CrossChannelFailoverEnabled: true,
	}
	// NewWithDependencies (not New) so the plugin host is wired in; the cleanup
	// stops the background schedulers this also starts, which the package's
	// TestMain guard would otherwise report as a leak.
	handler := httpapi.NewWithDependencies(cfg, db, enc, httpapi.Dependencies{PluginService: pluginService})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		httpapi.StopBackground(ctx)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{
		"name": "hook-site", "base_url": upstreamURL, "platform": "openai", "status": "enabled",
	}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/sites/%d/credentials", server.URL, site.ID), map[string]any{
		"kind": "api_key", "secret": "test-upstream-key-abcdef", "status": "enabled",
	}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "hook-ch",
		"base_url": upstreamURL, "type_hint": "openai", "status": "enabled",
	}), &channel)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{
		"model_pattern": "gpt-real", "enabled": true,
	}), &route)
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": channel.ID, "priority": 1, "weight": 100, "enabled": true,
	})
	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{
		"name": "hook-key", "scopes": "relay",
	}), &key)

	post(t, server.URL+"/admin/plugins/register", map[string]any{
		"url": plugin.URL, "api_key": "plugin-test-key",
	})
	return server.URL, key.Token
}

// relayChatHeaders is relayChat plus the extra request headers and the response
// headers, so a test can assert on the decision echo.
func relayChatHeaders(t *testing.T, serverURL, token, body string, extra map[string]string) (int, []byte, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range extra {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, resp.Header
}

// getDownstream issues an authenticated public GET with a downstream token.
func getDownstream(t *testing.T, serverURL, token, path string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, serverURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func TestPluginRouteHookRoutesVirtualModel(t *testing.T) {
	upstream := newEchoUpstream(t)
	plugin := newHookPlugin(t, hookPluginConfig{ID: "hook-router", RouteModel: "gpt-real"})
	serverURL, token := setupPluginGateway(t, upstream.URL, plugin)

	status, body, header := relayChatHeaders(t, serverURL, token,
		`{"model":"auto","messages":[{"role":"user","content":"write me a function"}]}`, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	if got := upstream.lastModel(); got != "gpt-real" {
		t.Fatalf("upstream model = %q, want gpt-real (the plugin's rewrite)", got)
	}
	if !strings.Contains(string(body), "upstream answer") {
		t.Fatalf("client body = %s, want the upstream answer", body)
	}
	if route, _, _ := plugin.counts(); route != 1 {
		t.Fatalf("route hook calls = %d, want 1", route)
	}
	input := plugin.routeInput()
	if input["model"] != "auto" {
		t.Fatalf("hook input model = %v, want the client's virtual name", input["model"])
	}
	// The catalogue is what makes the decision possible: a router cannot pick a
	// model that is absent from available_models.
	available, _ := input["available_models"].([]any)
	foundReal := false
	for _, item := range available {
		if item == "gpt-real" {
			foundReal = true
		}
	}
	if !foundReal {
		t.Fatalf("available_models = %v, want the routable model list", input["available_models"])
	}
	echo := header.Get(httpapi.HookDecisionEchoHeader)
	if !strings.Contains(echo, "gpt-real") || !strings.Contains(echo, "hook-router") {
		t.Fatalf("%s = %q, want the rewrite attributed to the plugin", httpapi.HookDecisionEchoHeader, echo)
	}

	// The virtual name has no route of its own, so it must be discoverable.
	modelsStatus, modelsBody := getDownstream(t, serverURL, token, "/v1/models")
	if modelsStatus != http.StatusOK {
		t.Fatalf("/v1/models status %d body %s", modelsStatus, modelsBody)
	}
	if !strings.Contains(string(modelsBody), `"auto"`) {
		t.Fatalf("/v1/models = %s, want the plugin's virtual model listed", modelsBody)
	}

	// The console readout: which plugin serves which models at which stage.
	var hookList struct {
		Hooks []struct {
			PluginID    string   `json:"plugin_id"`
			Point       string   `json:"point"`
			MatchModels []string `json:"match_models"`
			TimeoutMs   int      `json:"timeout_ms"`
			Tripped     bool     `json:"tripped"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(get(t, serverURL+"/admin/plugins/hooks"), &hookList); err != nil {
		t.Fatalf("decode hooks: %v", err)
	}
	if len(hookList.Hooks) != 1 {
		t.Fatalf("hooks = %+v, want the one declared route hook", hookList.Hooks)
	}
	if hookList.Hooks[0].PluginID != "hook-router" || hookList.Hooks[0].Point != "route" {
		t.Fatalf("hook = %+v, want the route hook attributed to its plugin", hookList.Hooks[0])
	}
	if len(hookList.Hooks[0].MatchModels) != 1 || hookList.Hooks[0].MatchModels[0] != "auto" {
		t.Fatalf("match_models = %v, want the declared virtual name", hookList.Hooks[0].MatchModels)
	}
	if hookList.Hooks[0].TimeoutMs <= 0 {
		t.Fatalf("timeout_ms = %d, want the effective timeout reported", hookList.Hooks[0].TimeoutMs)
	}
}

func TestPluginHookFailsOpenWhenSidecarBreaks(t *testing.T) {
	upstream := newEchoUpstream(t)
	plugin := newHookPlugin(t, hookPluginConfig{ID: "hook-broken", RouteModel: "gpt-real", RouteStatus: http.StatusInternalServerError})
	serverURL, token := setupPluginGateway(t, upstream.URL, plugin)

	// "auto" has no route, so a failed hook leaves the request exactly where it
	// would have been without the plugin: a 404 from routing, not a 500 from
	// the plugin layer, and nothing sent upstream.
	status, body, _ := relayChatHeaders(t, serverURL, token,
		`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`, nil)
	if status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404 (fail-open, not a plugin error)", status, body)
	}
	if models, _ := upstream.snapshot(); len(models) != 0 {
		t.Fatalf("upstream saw %v, want no traffic for an unroutable model", models)
	}
}

func TestPluginHookTimesOutAndFailsOpen(t *testing.T) {
	upstream := newEchoUpstream(t)
	// The declaration allows 500ms; the plugin sleeps well past it.
	plugin := newHookPlugin(t, hookPluginConfig{ID: "hook-slow", RouteModel: "gpt-real", RouteDelay: 3 * time.Second})
	serverURL, token := setupPluginGateway(t, upstream.URL, plugin)

	start := time.Now()
	status, _, _ := relayChatHeaders(t, serverURL, token,
		`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`, nil)
	elapsed := time.Since(start)
	if status != http.StatusNotFound {
		t.Fatalf("status %d, want 404 after the hook timed out", status)
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("request took %s, want it bounded by the hook's own timeout", elapsed)
	}
}

func TestPluginHookSkipsItsOwnNestedCall(t *testing.T) {
	upstream := newEchoUpstream(t)
	plugin := newHookPlugin(t, hookPluginConfig{ID: "hook-nested", RouteModel: "gpt-real"})
	serverURL, token := setupPluginGateway(t, upstream.URL, plugin)

	// A plugin's nested gateway call forwards the origin marker it received.
	// Its own hook must not fire again for that request.
	status, _, _ := relayChatHeaders(t, serverURL, token,
		`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{
			plugins.XHookOriginHeader: "hook-nested",
			plugins.XHookDepthHeader:  "1",
		})
	if status != http.StatusNotFound {
		t.Fatalf("status %d, want 404: the plugin must not intercept its own nested call", status)
	}
	if route, _, _ := plugin.counts(); route != 0 {
		t.Fatalf("route hook calls = %d, want 0 for the plugin's own nested call", route)
	}
}

func TestPluginRequestHookRewritesUpstreamBody(t *testing.T) {
	upstream := newEchoUpstream(t)
	plugin := newHookPlugin(t, hookPluginConfig{
		ID:             "hook-rewriter",
		DeclareRequest: true,
		RequestBody:    `{"model":"gpt-real","messages":[{"role":"user","content":"rewritten by the plugin"}]}`,
	})
	serverURL, token := setupPluginGateway(t, upstream.URL, plugin)

	status, body, _ := relayChatHeaders(t, serverURL, token,
		`{"model":"gpt-real","messages":[{"role":"user","content":"original"}]}`, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	if _, request, _ := plugin.counts(); request != 1 {
		t.Fatalf("request hook calls = %d, want 1", request)
	}
	_, bodies := upstream.snapshot()
	if len(bodies) == 0 {
		t.Fatal("upstream received nothing")
	}
	if !strings.Contains(bodies[len(bodies)-1], "rewritten by the plugin") {
		t.Fatalf("upstream body = %s, want the plugin's rewrite", bodies[len(bodies)-1])
	}
	if strings.Contains(bodies[len(bodies)-1], `"content":"original"`) {
		t.Fatalf("upstream body = %s, want the original content replaced", bodies[len(bodies)-1])
	}
}

func TestPluginResponseHookRewritesClientBody(t *testing.T) {
	upstream := newEchoUpstream(t)
	plugin := newHookPlugin(t, hookPluginConfig{
		ID:              "hook-responder",
		DeclareResponse: true,
		ResponseBody:    `{"id":"cmpl-1","object":"chat.completion","model":"gpt-real","choices":[{"index":0,"message":{"role":"assistant","content":"rewritten by the plugin"},"finish_reason":"stop"}]}`,
	})
	serverURL, token := setupPluginGateway(t, upstream.URL, plugin)

	status, body, _ := relayChatHeaders(t, serverURL, token,
		`{"model":"gpt-real","messages":[{"role":"user","content":"hi"}]}`, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	if _, _, response := plugin.counts(); response != 1 {
		t.Fatalf("response hook calls = %d, want 1", response)
	}
	if !strings.Contains(string(body), "rewritten by the plugin") {
		t.Fatalf("client body = %s, want the plugin's rewrite", body)
	}
}

func TestPluginHooksStayOffUnmatchedModels(t *testing.T) {
	upstream := newEchoUpstream(t)
	// The plugin declares a route hook for "auto" and nothing else.
	plugin := newHookPlugin(t, hookPluginConfig{ID: "hook-selector", RouteModel: "gpt-real"})
	serverURL, token := setupPluginGateway(t, upstream.URL, plugin)

	// A model the plugin never named must not reach the plugin process at all:
	// this is what keeps the hot path free of hook latency for ordinary traffic.
	status, body, _ := relayChatHeaders(t, serverURL, token,
		`{"model":"gpt-real","messages":[{"role":"user","content":"hi"}]}`, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	if route, request, response := plugin.counts(); route != 0 || request != 0 || response != 0 {
		t.Fatalf("hook calls route=%d request=%d response=%d, want 0/0/0 for an unmatched model", route, request, response)
	}
}

func TestPluginHooksRequireInterceptPermission(t *testing.T) {
	// Declares a route hook but does not ask for relay:intercept.
	plugin := newHookPlugin(t, hookPluginConfig{
		ID:          "hook-unpermitted",
		RouteModel:  "gpt-real",
		Permissions: []string{"admin_api:hook-unpermitted"},
	})
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	pluginService, err := plugins.NewService(filepath.Join(dataDir, "plugins"), db.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pluginService.RegisterSidecar(plugin.URL, "k", nil)
	if err == nil {
		t.Fatal("registration succeeded, want a rejection: hooks without relay:intercept must not load")
	}
	if !strings.Contains(err.Error(), plugins.InterceptPermission) {
		t.Fatalf("error = %v, want it to name the missing permission", err)
	}
}
