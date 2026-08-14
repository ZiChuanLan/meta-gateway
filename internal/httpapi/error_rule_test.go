package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestErrorRuleHotReload proves the error passthrough rules apply live:
// adding a passthrough rule returns the first channel's 429 immediately
// (no failover, no second-channel hit); rewriting changes the status code;
// disabling the rule restores failover — all without a restart, and the
// passthrough/rewrite paths never cool or trip the breaker on channel A so
// the sequence is order-independent.
func TestErrorRuleHotReload(t *testing.T) {
	var firstHits atomic.Int64
	var secondHits atomic.Int64
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits.Add(1)
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit exceeded","code":"rate_limit_exceeded"}}`)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":"c2","object":"chat.completion","model":"gemini-2.5-flash","choices":[]}`)
	}))
	defer second.Close()

	serverURL, token, _ := setupRelayPair(t, first.URL, second.URL)
	send := func() (int, string) {
		req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions",
			strings.NewReader(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	updateRule := func(id int64, body map[string]any) {
		payload, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/admin/error-rules/%d", serverURL, id), bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer admin-test")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			out, _ := io.ReadAll(resp.Body)
			t.Fatalf("update rule status=%d body=%s", resp.StatusCode, out)
		}
	}
	createRule := func(body map[string]any) int64 {
		payload, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, serverURL+"/admin/error-rules", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer admin-test")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("create rule status=%d body=%s", resp.StatusCode, out)
		}
		var rule struct{ ID int64 }
		json.Unmarshal(out, &rule)
		return rule.ID
	}

	// Phase 1 — passthrough rule applies to the very next request (hot
	// reload): client gets channel A's 429 directly, channel B never sees it.
	ruleID := createRule(map[string]any{
		"name": "rl-passthrough", "status_code": 429, "keyword": "rate limit",
		"model_glob": "gemini-*", "channel_id": 0, "action": "passthrough", "enabled": true,
	})
	status, body := send()
	if status != http.StatusTooManyRequests || !strings.Contains(body, "rate limit") {
		t.Fatalf("passthrough status=%d body=%s, want 429", status, body)
	}
	if secondHits.Load() != 0 {
		t.Fatalf("second channel hit after passthrough: %d, want 0", secondHits.Load())
	}

	// Phase 2 — hot reload #2: rewrite the same rule → 402 on the next
	// request.
	updateRule(ruleID, map[string]any{
		"name": "rl-passthrough", "status_code": 429, "keyword": "rate limit",
		"model_glob": "gemini-*", "channel_id": 0, "action": "rewrite", "rewrite_to": 402, "enabled": true,
	})
	status, _ = send()
	if status != http.StatusPaymentRequired {
		t.Fatalf("rewrite status=%d, want 402", status)
	}
	if secondHits.Load() != 0 {
		t.Fatalf("second channel hit after rewrite: %d, want 0", secondHits.Load())
	}

	// Phase 3 — hot reload #3: disable the rule → default failover restored:
	// channel A's 429 now fails over to channel B, which answers 200.
	updateRule(ruleID, map[string]any{
		"name": "rl-passthrough", "status_code": 429, "keyword": "rate limit",
		"model_glob": "gemini-*", "channel_id": 0, "action": "passthrough", "enabled": false,
	})
	status, _ = send()
	if status != http.StatusOK {
		t.Fatalf("disabled-rule status=%d, want 200 (failover)", status)
	}
	if secondHits.Load() != 1 {
		t.Fatalf("second channel hits after disable = %d, want 1", secondHits.Load())
	}
}
