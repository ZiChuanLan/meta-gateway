package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The upstream x-request-id header must be captured into proxy_logs so the
// log page can cross-reference the upstream's own request logs.
func TestUpstreamRequestIDCapturedIntoLogs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "upstream-req-abc-123")
		_, _ = fmt.Fprint(w, `{"id":"cmpl-1","object":"chat.completion","model":"gemini-2.5-flash","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "openai-compatible")

	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(
		`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`,
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

	// Find the newest log row for this request and assert the captured ID.
	logsReq, _ := http.NewRequest(http.MethodGet, serverURL+"/admin/proxy-logs?limit=20", nil)
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
	latest := logs[0]
	if got := latest["upstream_request_id"]; got != "upstream-req-abc-123" {
		t.Fatalf("upstream_request_id = %v, want upstream-req-abc-123 (log %+v)", got, latest)
	}
	if got := latest["model"]; got != "gemini-2.5-flash" {
		t.Fatalf("model = %v", got)
	}
}

// Channels that do not echo x-request-id leave the column empty (no crash).
func TestUpstreamRequestIDAbsentWhenUpstreamOmits(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"cmpl-2","object":"chat.completion","model":"gemini-2.5-flash","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "openai-compatible")
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(
		`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	logsReq, _ := http.NewRequest(http.MethodGet, serverURL+"/admin/proxy-logs?limit=1", nil)
	logsReq.Header.Set("Authorization", "Bearer admin-test")
	logsResp, _ := http.DefaultClient.Do(logsReq)
	defer logsResp.Body.Close()
	var logs []map[string]any
	if err := json.NewDecoder(logsResp.Body).Decode(&logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("no proxy logs")
	}
	if got := logs[0]["upstream_request_id"]; got != "" && got != nil {
		t.Fatalf("upstream_request_id = %v, want empty", got)
	}
}
