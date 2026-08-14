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

// A channel declaring max_reasoning_effort must receive a downgraded body:
// the client asks for max, the upstream must see high, and the proxy log
// records the mapping.
func TestReasoningEffortDowngradedPerChannelCapability(t *testing.T) {
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"cmpl-1","object":"chat.completion","model":"gemini-2.5-flash","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	defer upstream.Close()

	serverURL, token, channelID := setupRelay(t, upstream.URL, "openai-compatible")

	// Declare the channel capability: this upstream rejects reasoning_effort=max.
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID), map[string]any{
		"max_reasoning_effort": "high",
	})
	var ch struct {
		MaxReasoningEffort string `json:"max_reasoning_effort"`
	}
	json.Unmarshal(get(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID)), &ch)
	t.Logf("channel max_reasoning_effort after PUT = %q", ch.MaxReasoningEffort)

	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(
		`{"model":"gemini-2.5-flash","reasoning_effort":"max","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("chat status = %d body=%s", resp.StatusCode, body)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(received), &sent); err != nil {
		t.Fatalf("upstream body invalid: %v", err)
	}
	if got := sent["reasoning_effort"]; got != "high" {
		t.Fatalf("upstream reasoning_effort = %v, want high (body=%s)", got, received)
	}

	// Proxy log records the mapping.
	logsReq, _ := http.NewRequest(http.MethodGet, serverURL+"/admin/proxy-logs?limit=1", nil)
	logsReq.Header.Set("Authorization", "Bearer admin-test")
	logsResp, err := http.DefaultClient.Do(logsReq)
	if err != nil {
		t.Fatal(err)
	}
	defer logsResp.Body.Close()
	var logs []map[string]any
	if err := json.NewDecoder(logsResp.Body).Decode(&logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("no proxy logs")
	}
	if got := logs[0]["mapped_reasoning_effort"]; got != "max→high" {
		t.Fatalf("mapped_reasoning_effort = %v, want max→high (log %+v)", got, logs[0])
	}
	if got := logs[0]["reasoning_effort"]; got != "max" {
		t.Fatalf("reasoning_effort = %v, want original max", got)
	}
}

// Channels without a declared capability pass the request through untouched.
func TestReasoningEffortPassthroughWithoutCapability(t *testing.T) {
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"cmpl-2","object":"chat.completion","model":"gemini-2.5-flash","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "openai-compatible")

	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(
		`{"model":"gemini-2.5-flash","reasoning_effort":"max","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d", resp.StatusCode)
	}
	if !strings.Contains(received, `"reasoning_effort":"max"`) {
		t.Fatalf("upstream body was modified: %s", received)
	}
}
