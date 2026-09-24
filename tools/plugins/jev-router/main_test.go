package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The plugin's own tests. The gateway side has its own suite; these cover the
// judgment layer: what gets sent to Jev, and what happens when Jev is slow,
// wrong, or unreachable.

func configHeader(t *testing.T, cfg map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// fakeJev is a TypeSafe API stand-in that records the request it received.
type fakeJev struct {
	*httptest.Server
	status int
	answer string
	// received is the decoded request body of the last call.
	received map[string]any
	headers  http.Header
	calls    int
}

func newFakeJev(t *testing.T, status int, choice string, confidence float64) *fakeJev {
	t.Helper()
	jev := &fakeJev{status: status, answer: choice}
	jev.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jev.calls++
		jev.headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &jev.received)
		if jev.status != http.StatusOK {
			w.WriteHeader(jev.status)
			fmt.Fprint(w, `{"error":"rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"model":"jev-1.13.0","answers":{"choice":{"type":"choice","choice":%q,"confidence":%v,"probabilities":{%q:%v,"gpt-5":%v}}},"usage":{"input_tokens":42,"output_tokens":7}}`,
			choice, confidence, choice, confidence, 1-confidence)
	}))
	t.Cleanup(jev.Server.Close)
	return jev
}

// routeHook drives the handler the way the gateway does.
func routeHook(t *testing.T, srv *server, cfg map[string]any, prompt string, available []string) hookOutput {
	t.Helper()
	payload := map[string]any{
		"hook":    "route",
		"model":   virtualModel,
		"stream":  false,
		"body":    map[string]any{"model": virtualModel, "messages": []map[string]any{{"role": "user", "content": prompt}}},
		"headers": map[string]string{"X-Meta-Client": "claude-code"},
	}
	if available != nil {
		payload["available_models"] = available
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/hooks/route", bytes.NewReader(raw))
	req.Header.Set("X-Plugin-Config", configHeader(t, cfg))
	rec := httptest.NewRecorder()
	srv.handleRouteHook(rec, req)
	var out hookOutput
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode hook output: %v (body %s)", err, rec.Body.String())
	}
	return out
}

func newTestServer() *server {
	return &server{client: http.DefaultClient}
}

func TestParseCandidates(t *testing.T) {
	parsed := parseCandidates("claude-sonnet-4: 代码与长上下文\n\n# 注释\n gpt-5 \ndeepseek-chat: 便宜\nclaude-sonnet-4: 重复")
	if len(parsed) != 3 {
		t.Fatalf("candidates = %+v, want 3 entries (blank, comment and duplicate dropped)", parsed)
	}
	if parsed[0].Model != "claude-sonnet-4" || parsed[0].Description != "代码与长上下文" {
		t.Fatalf("first candidate = %+v", parsed[0])
	}
	if parsed[1].Model != "gpt-5" || parsed[1].Description != "" {
		t.Fatalf("bare model line = %+v, want no description", parsed[1])
	}
}

func TestSummarizeExtractsNewestUserMessage(t *testing.T) {
	body := json.RawMessage(`{"model":"auto","messages":[{"role":"user","content":"旧问题"},{"role":"assistant","content":"回答"},{"role":"user","content":"新问题"}]}`)
	if got := summarize(body, 100); got != "新问题" {
		t.Fatalf("summarize = %q, want the newest user message", got)
	}
	// Anthropic-style content blocks.
	blocks := json.RawMessage(`{"messages":[{"role":"user","content":[{"type":"text","text":"第一段"},{"type":"text","text":"第二段"}]}]}`)
	if got := summarize(blocks, 100); got != "第一段\n第二段" {
		t.Fatalf("summarize(blocks) = %q", got)
	}
	// Truncation is by rune, not byte: a multi-byte prompt must not be cut
	// mid-character.
	long := strings.Repeat("汉", 50)
	got := summarize(json.RawMessage(`{"messages":[{"role":"user","content":"`+long+`"}]}`), 10)
	if len([]rune(got)) != 11 || !strings.HasSuffix(got, "…") {
		t.Fatalf("summarize(long) = %q (%d runes), want 10 runes + ellipsis", got, len([]rune(got)))
	}
}

func TestSelectCandidatesIntersectsAvailableModels(t *testing.T) {
	configured := parseCandidates("claude-sonnet-4: 代码\ngpt-5: 推理\nold-model: 已下线")
	available := map[string]struct{}{"claude-sonnet-4": {}, "gpt-5": {}, "deepseek-chat": {}}
	selected := selectCandidates(configured, available)
	if len(selected) != 2 {
		t.Fatalf("selected = %+v, want only routable candidates", selected)
	}
	// No configuration: every routable model becomes a candidate.
	all := selectCandidates(nil, available)
	if len(all) != 3 {
		t.Fatalf("all = %+v, want every available model", all)
	}
}

func TestRouteHookSendsStrictSystemOnePayload(t *testing.T) {
	jev := newFakeJev(t, http.StatusOK, "claude-sonnet-4", 0.92)
	out := routeHook(t, newTestServer(), map[string]any{
		"api_key":    "test-key",
		"base_url":   jev.URL,
		"candidates": "claude-sonnet-4: 代码\ngpt-5: 推理",
	}, "帮我重构这个函数", []string{"claude-sonnet-4", "gpt-5"})

	if !out.Handled || out.Model != "claude-sonnet-4" {
		t.Fatalf("output = %+v, want the Jev choice", out)
	}
	if out.Confidence == nil || *out.Confidence != 0.92 {
		t.Fatalf("confidence = %v, want 0.92 passed through", out.Confidence)
	}
	if out.Reason == "" {
		t.Fatal("reason is empty, want a human-readable basis")
	}
	// The API rejects any key beyond these three (measured: even temperature
	// earns a 422), so the wire shape is part of the contract.
	if len(jev.received) != 3 {
		t.Fatalf("jev request keys = %v, want exactly state/model/questions", jev.received)
	}
	for _, key := range []string{"state", "model", "questions"} {
		if _, ok := jev.received[key]; !ok {
			t.Fatalf("jev request is missing %q: %v", key, jev.received)
		}
	}
	if got := jev.headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestRouteHookFallsBackWhenJevIsUncertain(t *testing.T) {
	jev := newFakeJev(t, http.StatusOK, "claude-sonnet-4", 0.31)
	out := routeHook(t, newTestServer(), map[string]any{
		"api_key":        "test-key",
		"base_url":       jev.URL,
		"candidates":     "claude-sonnet-4: 代码\ngpt-5: 推理",
		"default_model":  "gpt-5",
		"min_confidence": 0.5,
	}, "???", []string{"claude-sonnet-4", "gpt-5"})

	if !out.Handled || out.Model != "gpt-5" {
		t.Fatalf("output = %+v, want the configured fallback on low confidence", out)
	}
	if out.Confidence == nil || *out.Confidence != 0.31 {
		t.Fatalf("confidence = %v, want the original Jev value preserved", out.Confidence)
	}
}

func TestRouteHookFallsBackWhenJevFails(t *testing.T) {
	jev := newFakeJev(t, http.StatusTooManyRequests, "", 0)
	srv := newTestServer()
	out := routeHook(t, srv, map[string]any{
		"api_key":       "test-key",
		"base_url":      jev.URL,
		"candidates":    "claude-sonnet-4: 代码",
		"default_model": "claude-sonnet-4",
	}, "hi", []string{"claude-sonnet-4"})

	if !out.Handled || out.Model != "claude-sonnet-4" {
		t.Fatalf("output = %+v, want the fallback when Jev answers 429", out)
	}
	srv.mu.Lock()
	failures := srv.failures
	srv.mu.Unlock()
	if failures != 1 {
		t.Fatalf("failures = %d, want the Jev error counted", failures)
	}
}

func TestRouteHookDeclinesWhenNothingIsRoutable(t *testing.T) {
	jev := newFakeJev(t, http.StatusOK, "claude-sonnet-4", 0.9)
	called := jev.calls
	// Candidates configured, but the gateway can route none of them.
	out := routeHook(t, newTestServer(), map[string]any{
		"api_key":    "test-key",
		"base_url":   jev.URL,
		"candidates": "claude-sonnet-4: 代码",
	}, "hi", []string{"deepseek-chat"})

	if out.Handled {
		t.Fatalf("output = %+v, want a decline so the gateway's own error stands", out)
	}
	if jev.calls != called {
		t.Fatal("Jev was called even though no candidate is routable")
	}
}

func TestRouteHookDeclinesWithoutAPIKey(t *testing.T) {
	out := routeHook(t, newTestServer(), map[string]any{"candidates": "claude-sonnet-4: 代码"}, "hi", []string{"claude-sonnet-4"})
	if out.Handled {
		t.Fatalf("output = %+v, want a decline without an API key", out)
	}
}

func TestRouteHookRejectsUnroutableChoice(t *testing.T) {
	// Jev picks a model the gateway cannot route (stale candidate list).
	jev := newFakeJev(t, http.StatusOK, "claude-sonnet-4", 0.95)
	out := routeHook(t, newTestServer(), map[string]any{
		"api_key":       "test-key",
		"base_url":      jev.URL,
		"candidates":    "claude-sonnet-4: 代码\ngpt-5: 推理",
		"default_model": "gpt-5",
	}, "hi", []string{"gpt-5"})

	// claude-sonnet-4 was filtered out of the candidate list before the call,
	// so Jev could not have chosen it — the fallback is what the gateway needs.
	if !out.Handled || out.Model != "gpt-5" {
		t.Fatalf("output = %+v, want the routable fallback", out)
	}
}

func TestManifestDeclaresHookAndPermission(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer().handleManifest(rec, httptest.NewRequest(http.MethodGet, "/plugin.json", nil))
	var manifest struct {
		ID          string   `json:"id"`
		Permissions []string `json:"permissions"`
		Hooks       struct {
			Route struct {
				Path        string   `json:"path"`
				MatchModels []string `json:"match_models"`
				TimeoutMs   int      `json:"timeout_ms"`
			} `json:"route"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ID != pluginID {
		t.Fatalf("id = %q", manifest.ID)
	}
	if len(manifest.Permissions) != 1 || manifest.Permissions[0] != "relay:intercept" {
		t.Fatalf("permissions = %v, want relay:intercept", manifest.Permissions)
	}
	if manifest.Hooks.Route.Path != "/hooks/route" {
		t.Fatalf("route hook path = %q", manifest.Hooks.Route.Path)
	}
	if len(manifest.Hooks.Route.MatchModels) != 1 || manifest.Hooks.Route.MatchModels[0] != virtualModel {
		t.Fatalf("match_models = %v, want the virtual name", manifest.Hooks.Route.MatchModels)
	}
	// The hook budget must exceed the plugin's own Jev budget, or the gateway
	// would time out before the plugin has a chance to fall back.
	if manifest.Hooks.Route.TimeoutMs <= defaultJevTimeoutMs {
		t.Fatalf("hook timeout = %d, want more than the plugin's %dms Jev budget", manifest.Hooks.Route.TimeoutMs, defaultJevTimeoutMs)
	}
}

func TestDecodeConfigReadsGatewayPayload(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"api_key": "k", "model": "jev-preview"})
	cfg := decodeConfig(base64.StdEncoding.EncodeToString(raw))
	if cfg.APIKey != "k" || cfg.Model != "jev-preview" {
		t.Fatalf("config = %+v", cfg)
	}
	if decodeConfig("").APIKey != "" {
		t.Fatal("an absent config header must decode to an empty config")
	}
	if decodeConfig("not-base64!!").APIKey != "" {
		t.Fatal("a corrupt config header must fail open to an empty config")
	}
}

// newSequencedJev answers a different choice on each call, so the two-step
// scenario decision can be exercised end to end. Calls past the end of the
// list repeat the last choice.
func newSequencedJev(t *testing.T, choices ...string) *fakeJev {
	t.Helper()
	jev := &fakeJev{status: http.StatusOK}
	index := 0
	jev.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jev.calls++
		jev.headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &jev.received)
		choice := choices[len(choices)-1]
		if index < len(choices) {
			choice = choices[index]
		}
		index++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"model":"jev-1.13.0","answers":{"choice":{"type":"choice","choice":%q,"confidence":0.9,"probabilities":{%q:0.9}}},"usage":{"input_tokens":10,"output_tokens":2}}`, choice, choice)
	}))
	t.Cleanup(jev.Server.Close)
	return jev
}

func TestVirtualModelIsNamespaced(t *testing.T) {
	// Every router plugin wants to answer "auto"; a client has to be able to
	// say which one it means.
	if virtualModel != "auto-jev" {
		t.Fatalf("virtualModel = %q, want the plugin-namespaced name", virtualModel)
	}
}

func TestParseScenarios(t *testing.T) {
	parsed := parseScenarios("code (写代码、调试与重构): claude-sonnet-4, gpt-5\nsimple: deepseek-chat\n\n# note\nno colon here\ncode: other-model\n")
	if len(parsed) != 2 {
		t.Fatalf("scenarios = %+v, want 2 (blank, comment, malformed and duplicate dropped)", parsed)
	}
	if parsed[0].Name != "code" || parsed[0].Hint != "写代码、调试与重构" {
		t.Fatalf("first = %+v", parsed[0])
	}
	if len(parsed[0].Models) != 2 || parsed[0].Models[1].Model != "gpt-5" {
		t.Fatalf("models = %+v", parsed[0].Models)
	}
	if parsed[1].Name != "simple" || parsed[1].Hint != "" {
		t.Fatalf("second = %+v, want no hint", parsed[1])
	}
}

func TestParseScenarioLineKeepsColonsInsideTheHint(t *testing.T) {
	// The hint is prose and may carry its own colon; the separator is the first
	// colon left once the parentheses are gone.
	parsed, ok := parseScenarioLine(`code (规则: 写代码、调试): claude-sonnet-4`)
	if !ok {
		t.Fatal("line rejected")
	}
	if parsed.Name != "code" || parsed.Hint != "规则: 写代码、调试" {
		t.Fatalf("parsed = %+v", parsed)
	}
	if len(parsed.Models) != 1 || parsed.Models[0].Model != "claude-sonnet-4" {
		t.Fatalf("models = %+v", parsed.Models)
	}
}

func TestUsableScenariosDropsWhatTheGatewayCannotRoute(t *testing.T) {
	available := map[string]struct{}{"gpt-5": {}, "deepseek-chat": {}}
	parsed := parseScenarios("code: claude-sonnet-4\ngp: gpt-5, claude-sonnet-4\ncheap: deepseek-chat")
	usable := usableScenarios(parsed, available)
	if len(usable) != 2 {
		t.Fatalf("usable = %+v, want the code scenario dropped for having nothing routable", usable)
	}
	for _, item := range usable {
		for _, model := range item.Models {
			if _, ok := available[model.Model]; !ok {
				t.Fatalf("scenario %s kept unroutable model %s", item.Name, model.Model)
			}
		}
	}
}

func TestScenarioRoutingTakesTwoSteps(t *testing.T) {
	jev := newSequencedJev(t, "code", "gpt-5")
	out := routeHook(t, newTestServer(), map[string]any{
		"api_key":   "test-key",
		"base_url":  jev.URL,
		"scenarios": "code (写代码): claude-sonnet-4, gpt-5\nsimple: deepseek-chat",
	}, "帮我重构这个函数", []string{"claude-sonnet-4", "gpt-5", "deepseek-chat"})

	if !out.Handled || out.Model != "gpt-5" {
		t.Fatalf("output = %+v, want the model chosen inside the code scenario", out)
	}
	if jev.calls != 2 {
		t.Fatalf("jev calls = %d, want 2 (scenario, then model)", jev.calls)
	}
	if !strings.Contains(out.Reason, "code") {
		t.Fatalf("reason = %q, want the scenario named", out.Reason)
	}
}

func TestSingleModelScenarioSkipsTheSecondCall(t *testing.T) {
	jev := newSequencedJev(t, "simple")
	out := routeHook(t, newTestServer(), map[string]any{
		"api_key":   "test-key",
		"base_url":  jev.URL,
		"scenarios": "code: claude-sonnet-4\nsimple: deepseek-chat",
	}, "你好", []string{"claude-sonnet-4", "deepseek-chat"})

	if !out.Handled || out.Model != "deepseek-chat" {
		t.Fatalf("output = %+v, want the scenario's only routable model", out)
	}
	if jev.calls != 1 {
		t.Fatalf("jev calls = %d, want 1: a one-model scenario needs no second question", jev.calls)
	}
}

func TestSecondStepOnlyOffersTheScenarioShortlist(t *testing.T) {
	jev := newSequencedJev(t, "code", "gpt-5")
	routeHook(t, newTestServer(), map[string]any{
		"api_key":   "test-key",
		"base_url":  jev.URL,
		"scenarios": "code: claude-sonnet-4, gpt-5\nsimple: deepseek-chat",
	}, "重构", []string{"claude-sonnet-4", "gpt-5", "deepseek-chat"})

	questions, ok := jev.received["questions"].(map[string]any)
	if !ok {
		t.Fatalf("questions = %v", jev.received["questions"])
	}
	choice, ok := questions["choice"].(map[string]any)
	if !ok {
		t.Fatalf("question = %v", questions["choice"])
	}
	criteria, ok := choice["criteria"].(map[string]any)
	if !ok {
		t.Fatalf("criteria = %v", choice["criteria"])
	}
	// Without this the narrower question is a fiction: the second call would
	// still be weighing every model in the gateway.
	if len(criteria) != 2 {
		t.Fatalf("second call criteria = %v, want only the code scenario's models", criteria)
	}
	if _, leaked := criteria["deepseek-chat"]; leaked {
		t.Fatal("second call offered a model outside the chosen scenario")
	}
}

func TestScenarioRoutingFallsBackOnUnknownScenario(t *testing.T) {
	jev := newSequencedJev(t, "not-a-scenario")
	out := routeHook(t, newTestServer(), map[string]any{
		"api_key":       "test-key",
		"base_url":      jev.URL,
		"scenarios":     "code: claude-sonnet-4",
		"default_model": "claude-sonnet-4",
	}, "hi", []string{"claude-sonnet-4"})

	if !out.Handled || out.Model != "claude-sonnet-4" {
		t.Fatalf("output = %+v, want the fallback when the scenario is unknown", out)
	}
}

func TestFlatCandidatesStillWorkWithoutScenarios(t *testing.T) {
	jev := newFakeJev(t, http.StatusOK, "gpt-5", 0.9)
	out := routeHook(t, newTestServer(), map[string]any{
		"api_key":    "test-key",
		"base_url":   jev.URL,
		"candidates": "claude-sonnet-4: 代码\ngpt-5: 推理",
	}, "帮我重构这个函数", []string{"claude-sonnet-4", "gpt-5"})

	if !out.Handled || out.Model != "gpt-5" {
		t.Fatalf("output = %+v, want the flat shortlist to keep working", out)
	}
	if jev.calls != 1 {
		t.Fatalf("jev calls = %d, want 1 without scenarios", jev.calls)
	}
}

func TestParseScenariosAcceptsTheEditorJSON(t *testing.T) {
	// The console's group editor writes JSON; the line format stays supported
	// for a hand-pasted shortlist. Both must land on the same structure.
	raw := `[{"name":"code","hint":"写代码、调试与重构","models":["claude-sonnet-4","gpt-5"]},{"name":"simple","models":["deepseek-chat"]}]`
	parsed := parseScenarios(raw)
	if len(parsed) != 2 {
		t.Fatalf("scenarios = %+v, want 2", parsed)
	}
	if parsed[0].Name != "code" || parsed[0].Hint != "写代码、调试与重构" {
		t.Fatalf("first = %+v", parsed[0])
	}
	if len(parsed[0].Models) != 2 || parsed[0].Models[0].Model != "claude-sonnet-4" {
		t.Fatalf("models = %+v", parsed[0].Models)
	}
	if parsed[1].Name != "simple" || parsed[1].Hint != "" {
		t.Fatalf("second = %+v, want no hint", parsed[1])
	}
}

func TestParseScenariosJSONDropsUnusableEntries(t *testing.T) {
	raw := `[{"name":"","models":["a"]},{"name":"ok","models":["a"]},{"name":"nomodels","models":[]},{"name":"blank","models":[""]}]`
	parsed := parseScenarios(raw)
	if len(parsed) != 1 || parsed[0].Name != "ok" {
		t.Fatalf("scenarios = %+v, want only the usable one", parsed)
	}
}

func TestMalformedJSONFallsBackToTheLineFormat(t *testing.T) {
	// A typo in a JSON entry must not silently erase every scenario: the line
	// parser gets the value instead.
	raw := "[{oops\ncode (写代码): claude-sonnet-4"
	parsed := parseScenarios(raw)
	if len(parsed) != 1 || parsed[0].Name != "code" {
		t.Fatalf("scenarios = %+v, want the line format to be read", parsed)
	}
}

func TestDuplicateScenarioNamesAreDroppedInBothFormats(t *testing.T) {
	if got := parseScenarios("code: a\ncode: b"); len(got) != 1 || got[0].Models[0].Model != "a" {
		t.Fatalf("line format = %+v, want the first occurrence kept", got)
	}
	if got := parseScenarios(`[{"name":"code","models":["a"]},{"name":"code","models":["b"]}]`); len(got) != 1 || got[0].Models[0].Model != "a" {
		t.Fatalf("json format = %+v, want the first occurrence kept", got)
	}
}
