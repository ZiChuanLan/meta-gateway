package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// One-field upstream configuration, exercised through the public relay.
//
// The three behaviours below are what makes "paste the endpoint new-api style"
// work, and each is asserted against a real upstream capture rather than the
// config that was stored:
//
//	A. POST /v1/<unregistered> forwards the client's own path verbatim.
//	B. `base_url` carrying the whole endpoint is split at save time, so the
//	   doubled path (/v1/systemone/v1/chat/completions) never happens.
//	C. The real upstream URL is recorded on the proxy log AND echoed to the
//	   client, so a redirected call stays attributable.
func TestCustomPathAndEndpointBaseURL(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
		body  string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		body = string(raw)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/systemone") {
			// The upstream's own protocol, not OpenAI chat.
			fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.95}},"usage":{"input_tokens":12,"output_tokens":3}}`)
			return
		}
		fmt.Fprint(w, `{"object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	serverURL, token, channelID := setupRelay(t, upstream.URL, "openai-compatible")
	lastPath := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(paths) == 0 {
			return ""
		}
		return paths[len(paths)-1]
	}

	// A. Custom path passthrough: /v1/systemone is not a registered endpoint,
	// so it must reach the upstream unchanged — path, body and response.
	resp, raw := postWithToken(t, serverURL+"/v1/systemone", token,
		`{"model":"gemini-2.5-flash","state":"payouts failed","questions":{"urgent":{"type":"noul","instructions":"Urgent?"}}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("custom path status = %d body=%s", resp.StatusCode, raw)
	}
	if got := lastPath(); !strings.HasSuffix(got, "/v1/systemone") {
		t.Fatalf("upstream path = %q, want a …/v1/systemone suffix", got)
	}
	mu.Lock()
	sent := body
	mu.Unlock()
	if !strings.Contains(sent, `"questions"`) || !strings.Contains(sent, `"state"`) {
		t.Fatalf("custom-path body must be forwarded verbatim, got %s", sent)
	}
	// The upstream's own answer is returned untouched: no OpenAI reshaping is
	// applied on a path the gateway does not model.
	if !strings.Contains(string(raw), `"answers"`) {
		t.Fatalf("custom-path response = %s, want the upstream's own shape", raw)
	}
	// C. The endpoint actually called is echoed back to the client.
	if echoed := resp.Header.Get("X-Meta-Upstream-URL"); !strings.HasSuffix(echoed, "/v1/systemone") {
		t.Fatalf("X-Meta-Upstream-URL = %q, want the upstream endpoint", echoed)
	}

	// The proxy log carries the same URL, so the call is attributable after the
	// fact (proxy_logs.path alone no longer answers "what did we call?").
	logs := get(t, serverURL+"/admin/proxy-logs")
	var rows []map[string]any
	if err := json.Unmarshal(logs, &rows); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row["path"] != "systemone" {
			continue
		}
		found = true
		upstreamURL, _ := row["upstream_url"].(string)
		if !strings.HasSuffix(upstreamURL, "/v1/systemone") {
			t.Fatalf("proxy log upstream_url = %q, want a …/v1/systemone suffix", upstreamURL)
		}
	}
	if !found {
		t.Fatalf("no proxy log row for the custom path: %v", rows)
	}

	// B. base_url holding the whole endpoint is split at save time: the channel
	// keeps a clean root and gains the endpoint override, so the relay builds
	// /v1/systemone instead of /v1/systemone/v1/chat/completions.
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID), map[string]any{
		"base_url": upstream.URL + "/v1/systemone",
	})
	channel := get(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID))
	var stored map[string]any
	if err := json.Unmarshal(channel, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["base_url"] != upstream.URL {
		t.Fatalf("base_url = %v, want the root %q", stored["base_url"], upstream.URL)
	}
	// The stored form is the validator's canonical one: an override is stored
	// without a leading slash (the resolver re-adds it), so "v1/systemone" and
	// "/v1/systemone" are the same endpoint and the row has one representation.
	if stored["upstream_path_override"] != "v1/systemone" {
		t.Fatalf("upstream_path_override = %v, want v1/systemone", stored["upstream_path_override"])
	}
	resp, raw = postWithToken(t, serverURL+"/v1/chat/completions", token,
		`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("split base_url status = %d body=%s", resp.StatusCode, raw)
	}
	if got := lastPath(); got != "/v1/systemone" {
		t.Fatalf("upstream path = %q, want exactly /v1/systemone (no doubled /v1)", got)
	}

	// A path that tries to escape the upstream root is rejected, not forwarded.
	// Measured against the router: an escaped path stays escaped, so `%2F` and
	// `%2E%2E%2F` fail the closed allowlist on '%' and never reach the network.
	for _, bad := range []string{"/v1/..", "/v1/a/../../b", "/v1/a%2Fb", "/v1/%2E%2E%2Fsystemone", "/v1/a%00b"} {
		resp, _ := postWithToken(t, serverURL+bad, token, `{"model":"gemini-2.5-flash"}`)
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("unsafe path %q was forwarded", bad)
		}
	}
	// A query string is not part of the path and must not reach the upstream
	// URL: only the path is rebuilt. The call itself is still relayable.
	resp, _ = postWithToken(t, serverURL+"/v1/systemone?leak=1", token, `{"model":"gemini-2.5-flash"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query-bearing custom path status = %d", resp.StatusCode)
	}
	if got := lastPath(); got != "/v1/systemone" {
		t.Fatalf("upstream path = %q, want /v1/systemone without the client query", got)
	}
	// A registered endpoint is never shadowed by the fallback route.
	resp, raw = postWithToken(t, serverURL+"/v1/embeddings", token, `{"model":"gemini-2.5-flash","input":"hi"}`)
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("registered /v1/embeddings was shadowed by the fallback: %s", raw)
	}
}

// A channel payload rule writes the endpoint field for ONE model, which is how
// a single channel serves models that live on different upstream endpoints
// (TypeSafe's /v1/systemone for jev-latest, the ordinary chat endpoint for the
// rest) without inventing a channel per endpoint.
func TestPayloadRuleRetargetsUpstreamPathPerModel(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer upstream.Close()

	serverURL, token, channelID := setupRelay(t, upstream.URL, "openai-compatible")
	lastPath := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(paths) == 0 {
			return ""
		}
		return paths[len(paths)-1]
	}

	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID), map[string]any{
		"payload_rules": `[{"match":{"model":"gemini-2.5-flash"},"actions":[{"op":"set","path":"upstream_path","value":{"str":"v1/systemone"}}]}]`,
	})

	resp, raw := postWithToken(t, serverURL+"/v1/chat/completions", token,
		`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, raw)
	}
	if got := lastPath(); got != "/v1/systemone" {
		t.Fatalf("upstream path = %q, want /v1/systemone from the payload rule", got)
	}
	if echoed := resp.Header.Get("X-Meta-Upstream-URL"); !strings.HasSuffix(echoed, "/v1/systemone") {
		t.Fatalf("X-Meta-Upstream-URL = %q, want the rule-targeted endpoint", echoed)
	}

	// Clearing the rule returns the model to the channel's own endpoint: the
	// retarget is a per-request decision, not a persisted channel change.
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID), map[string]any{
		"payload_rules": "",
	})
	resp, raw = postWithToken(t, serverURL+"/v1/chat/completions", token,
		`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, raw)
	}
	if got := lastPath(); got != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want the default endpoint again", got)
	}
}

// The Compose E2E mock mounts each channel under a SUB-PATH (`base_url` .../ok,
// upstream serves `/ok/v1/chat/completions`). v3.4.1 shipped a "a base path is
// always an API root" rule that split that prefix into a complete-endpoint
// override, so `/v1/chat/completions` was dropped and every mount-prefix channel
// 404'd. Only the Compose job caught it, which meant a ~20 minute feedback loop;
// this test reproduces the contract locally.
func TestMountPrefixBaseURLKeepsTheV1Root(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The mock's own contract: the channel's prefix, then the API root.
		if !strings.HasPrefix(r.URL.Path, "/ok/v1/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL+"/ok", "openai-compatible")
	resp, raw := postWithToken(t, serverURL+"/v1/chat/completions", token,
		`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mount-prefix channel status = %d body=%s (a 404 means the prefix was split and /v1/chat/completions was dropped)", resp.StatusCode, raw)
	}
	if echoed := resp.Header.Get("X-Meta-Upstream-URL"); !strings.HasSuffix(echoed, "/ok/v1/chat/completions") {
		t.Fatalf("X-Meta-Upstream-URL = %q, want the mounted chat endpoint", echoed)
	}
}

// postWithToken relays a downstream request and returns the response plus body.
func postWithToken(t *testing.T, url, token, payload string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}
