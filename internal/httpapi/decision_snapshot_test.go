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

// Every routed request persists a decision snapshot that survives after the
// fact: candidates, scores, reasons and the selected channel.
func TestDecisionSnapshotPersistedPerRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"cmpl-1","object":"chat.completion","model":"gemini-2.5-flash","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	defer upstream.Close()

	serverURL, token, channelID := setupRelay(t, upstream.URL, "openai-compatible")

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
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("chat status = %d body=%s", resp.StatusCode, body)
	}

	// Find the request id from the logs, then fetch its snapshot.
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
	requestID, _ := logs[0]["request_id"].(string)
	if requestID == "" {
		t.Fatal("no request_id in log")
	}

	snapReq, _ := http.NewRequest(http.MethodGet, serverURL+"/admin/decision-snapshot?request_id="+requestID, nil)
	snapReq.Header.Set("Authorization", "Bearer admin-test")
	snapResp, err := http.DefaultClient.Do(snapReq)
	if err != nil {
		t.Fatal(err)
	}
	defer snapResp.Body.Close()
	if snapResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(snapResp.Body)
		t.Fatalf("snapshot status = %d body=%s", snapResp.StatusCode, body)
	}
	var snap map[string]any
	if err := json.NewDecoder(snapResp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if got := snap["selected_channel_id"]; int64(got.(float64)) != channelID {
		t.Fatalf("selected_channel_id = %v, want %d", got, channelID)
	}
	if got := snap["model"]; got != "gemini-2.5-flash" {
		t.Fatalf("model = %v", got)
	}
	var payload map[string]any
	raw, ok := snap["payload"].(map[string]any)
	if !ok {
		// RawMessage embeds as an object in JSON responses.
		t.Fatalf("payload type = %T", snap["payload"])
	}
	payload = raw
	candidates, _ := payload["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatalf("payload candidates empty: %+v", payload)
	}
	first, _ := candidates[0].(map[string]any)
	if first == nil || first["eligible"] != true {
		t.Fatalf("candidate[0] = %+v, want eligible=true", first)
	}
}

// Unknown request ids 404 instead of returning garbage.
func TestDecisionSnapshotNotFound(t *testing.T) {
	serverURL, _, _ := setupRelay(t, "http://127.0.0.1:1", "openai-compatible")
	req, _ := http.NewRequest(http.MethodGet, serverURL+"/admin/decision-snapshot?request_id=nope-123", nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
