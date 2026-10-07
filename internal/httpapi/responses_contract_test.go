package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A Responses request that needs a native upstream (stored conversation, or
// tools that are not functions) cannot be replayed as a chat completion. The
// channel has no /v1/responses surface, so all the gateway holds is the
// upstream's 404 — and passing that through is worse than useless: the client
// reads "404 page not found" as "this gateway has no Responses endpoint" and
// retries forever (production, 2026-10-07: a client retried every 2s). The
// gateway has to say what it actually knows.
func TestResponsesRefusalReplacesTheUpstreamNotFound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/responses") {
			http.Error(w, "not supported", http.StatusNotFound)
			return
		}
		t.Fatalf("the fallback must not run for a refused request, got %s", r.URL.Path)
	}))
	defer upstream.Close()
	base, token, _, _ := setupImageRelayWithStore(t, upstream.URL, "public")

	payload := map[string]any{
		"model":                "public",
		"previous_response_id": "resp_123",
		"input":                []map[string]any{{"role": "user", "content": "hello"}},
	}
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/responses", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("status=404 body=%s: the upstream's page-not-found was passed through", body)
	}
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s, want 501 for an unsupported feature", resp.StatusCode, body)
	}
	// The console's error catalog owns the wording; what matters here is that the
	// client is told the gateway could not serve it, not that a page was missing.
	if !strings.Contains(string(body), "not supported") || strings.Contains(string(body), "page not found") {
		t.Fatalf("body=%s, want the gateway's own reason", body)
	}
}

func TestResponsesHTTPFallbackPreservesArgumentsAndUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "json"
		if stream {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			captured := make(chan []byte, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/responses") {
					http.Error(w, "not supported", 404)
					return
				}
				body, _ := io.ReadAll(r.Body)
				captured <- body
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"id\":\"chat-1\",\"model\":\"public\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
					io.WriteString(w, "data: {\"id\":\"chat-1\",\"model\":\"public\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":0,\"total_tokens\":1000}}\n\ndata: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"chat-1","model":"public","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":0,"total_tokens":1000}}`)
				}
			}))
			defer upstream.Close()
			base, token, routeID, db := setupImageRelayWithStore(t, upstream.URL, "public")
			members, err := db.RouteMember.ListByRoute(routeID)
			if err != nil || len(members) == 0 {
				t.Fatal(err)
			}
			member := members[0]
			member.PricePromptPer1k = 3
			if err = db.RouteMember.Update(&member); err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"model": "public", "stream": stream, "input": []map[string]any{
				{"role": "user", "content": "hello"},
				{"type": "function_call", "call_id": "call-1", "name": "tool", "arguments": `{"x":1}`},
				{"type": "function_call_output", "call_id": "call-1", "output": "done"},
			}}
			raw, _ := json.Marshal(payload)
			req, _ := http.NewRequest(http.MethodPost, base+"/v1/responses", strings.NewReader(string(raw)))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			if stream && (!strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") || !strings.Contains(string(body), "response.completed")) {
				t.Fatalf("bad SSE: %s %s", resp.Header, body)
			}
			if !stream && !strings.Contains(string(body), `"object":"response"`) {
				t.Fatalf("bad JSON: %s", body)
			}
			upstreamBody := <-captured
			if !strings.Contains(string(upstreamBody), `"arguments":"{\"x\":1}"`) {
				t.Fatalf("arguments lost: %s", upstreamBody)
			}
			var count int
			var cost float64
			if err = db.QueryRow(`SELECT count(*),COALESCE(sum(cost),0) FROM usage_records`).Scan(&count, &cost); err != nil {
				t.Fatal(err)
			}
			if count != 1 || cost != 3 {
				t.Fatalf("bill: count=%d cost=%v", count, cost)
			}
		})
	}
}
