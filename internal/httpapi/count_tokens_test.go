package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// count_tokens forwards to the upstream's real endpoint (Anthropic-native
// channels) and returns the upstream count verbatim.
func TestCountTokensForwardsToAnthropicUpstream(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotAnthropicVersion string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("x-api-key")
		gotAnthropicVersion = r.Header.Get("anthropic-version")
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"role":"user"`) {
			t.Errorf("upstream body missing messages: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"input_tokens": 42}`)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "anthropic")

	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/messages/count_tokens", strings.NewReader(
		`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	if gotPath != "/v1/messages/count_tokens" {
		t.Fatalf("upstream path = %q, want /v1/messages/count_tokens", gotPath)
	}
	if gotAuth != "test-gemini-key-abcdef" {
		t.Fatalf("upstream x-api-key = %q", gotAuth)
	}
	if gotAnthropicVersion != "2023-06-01" {
		t.Fatalf("upstream anthropic-version = %q", gotAnthropicVersion)
	}
	var out struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response %s: %v", body, err)
	}
	if out.InputTokens != 42 {
		t.Fatalf("input_tokens = %d, want 42 (body %s)", out.InputTokens, body)
	}
}

// OpenAI-compatible channels have no count_tokens surface: the upstream 404
// is passed through and clients degrade gracefully.
func TestCountTokensOpenAIChannelUpstream404(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "openai-compatible")
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/messages/count_tokens", strings.NewReader(
		`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 passthrough", resp.StatusCode)
	}
}
