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

// TestModelNotFoundBlacklistAndClear exercises the full loop:
// upstream 404 model_not_found → blacklist entry written → the next request
// skips that channel → manual clear restores routing.
func TestModelNotFoundBlacklistAndClear(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "models") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"data":[{"id":"m1"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"message":"model 'ghost-model' does not exist","type":"invalid_request_error","code":"model_not_found"}}`)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "openai")
	post := func() (*http.Response, string) {
			req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(
			`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`,
		))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body)
	}

	// First request: upstream 404s with model_not_found; the proxy must
	// blacklist channel×model (route exists for gemini-2.5-flash, so the
	// request actually reaches the upstream).
	resp, _ := post()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("first status = %d, want 404", resp.StatusCode)
	}

	// Blacklist entry exists.
	blocks := getJSON(t, serverURL+"/admin/model-blocks")
	items, ok := blocks["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("no model blocks after 404: %v", blocks)
	}
	first := items[0].(map[string]any)
	model := first["model"].(string)
	if model != "gemini-2.5-flash" {
		t.Fatalf("blocked model = %q, want gemini-2.5-flash", model)
	}

	// Second request: the same channel must be skipped (the single-channel
	// setup has no fallback, so the response is a 503 with the blacklist
	// reason rather than another upstream round-trip).
	resp, body := post()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d body=%s, want 503 (blacklisted)", resp.StatusCode, body)
	}
	if !strings.Contains(body, "model not available") {
		t.Fatalf("second body = %s, want blacklist reason", body)
	}

	// Manual clear restores routing.
	channelID := int64(first["channel_id"].(float64))
	delReq, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/admin/model-blocks?channel_id=%d&model=%s", serverURL, channelID, model), nil)
	delReq.Header.Set("Authorization", "Bearer admin-test")
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil || delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %v err=%v", delResp, err)
	}
	delResp.Body.Close()

	resp, _ = post()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after clear status = %d, want 404 (routing restored)", resp.StatusCode)
	}
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("GET %s: %s", url, body)
	}
	return out
}
