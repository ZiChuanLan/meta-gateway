package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// Meter the actual upstream protocol before downstream translation. The stored
// token counters remain upstream counters; only the amount uses exclusive cache
// buckets. Both JSON and SSE exercise the shipped HTTP accounting callback.
func TestBillingCacheAcrossProtocolPipeline(t *testing.T) {
	for _, family := range []string{"openai-compatible", "anthropic", "gemini"} {
		for _, path := range []string{"chat/completions", "messages", "responses"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", family, path, stream), func(t *testing.T) {
					raw := `{"id":"chatcmpl-billing","model":"gpt-test","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100,"prompt_tokens_details":{"cached_tokens":800}}}`
					sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":100,\"total_tokens\":1100,\"prompt_tokens_details\":{\"cached_tokens\":800}}}\n\ndata: [DONE]\n\n"
					wantPrompt, wantTotal := 1000, 1100
					if family == "anthropic" {
						raw = `{"id":"msg-billing","type":"message","role":"assistant","model":"gpt-test","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":200,"output_tokens":100,"cache_read_input_tokens":800}}`
						sse = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-billing\",\"model\":\"gpt-test\",\"usage\":{\"input_tokens\":200,\"cache_read_input_tokens\":800}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":100}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
						wantPrompt, wantTotal = 200, 300
					} else if family == "gemini" {
						raw = `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1000,"candidatesTokenCount":100,"totalTokenCount":1100,"cachedContentTokenCount":800}}`
						sse = "data: " + raw + "\n\n"
					}
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, sse)
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, raw)
						}
					}))
					t.Cleanup(upstream.Close)
					base, token, db := setupServer(t, upstream.URL)
					if _, err := db.Exec("UPDATE channels SET type_hint=?", family); err != nil {
						t.Fatal(err)
					}
					if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "gpt-test", PricePromptPer1k: 1, PriceCompletionPer1k: 2, PriceCachePer1k: .1}); err != nil {
						t.Fatal(err)
					}
					body := map[string]any{"model": "gpt-test", "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "max_tokens": 100}
					if path == "responses" {
						body = map[string]any{"model": "gpt-test", "input": "hi", "stream": stream}
					}
					payload, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					req, err := http.NewRequest(http.MethodPost, base+"/v1/"+path, strings.NewReader(string(payload)))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer "+token)
					req.Header.Set("Content-Type", "application/json")
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					received, err := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					// Preserve the upstreams' registered Responses capability boundary.
					if path == "responses" && family != "openai-compatible" {
						if resp.StatusCode != http.StatusNotImplemented {
							t.Fatalf("unsupported Responses HTTP %d: %s", resp.StatusCode, received)
						}
						var count int
						if err := db.QueryRow("SELECT count(*) FROM usage_records").Scan(&count); err != nil || count != 0 {
							t.Fatalf("unsupported request charged: count=%d err=%v", count, err)
						}
						return
					}
					if resp.StatusCode != 200 {
						t.Fatalf("HTTP %d: %s", resp.StatusCode, received)
					}
					if !strings.Contains(string(received), "hi") {
						t.Fatalf("response lost content: %s", received)
					}
					// The final bytes may reach the client just before the callback commits.
					deadline := time.Now().Add(2 * time.Second)
					var cost float64
					var prompt, total, cache int
					for {
						err = db.QueryRow("SELECT cost,prompt_tokens,total_tokens,cache_read_tokens FROM usage_records ORDER BY id DESC LIMIT 1").Scan(&cost, &prompt, &total, &cache)
						if err == nil || time.Now().After(deadline) {
							break
						}
						time.Sleep(5 * time.Millisecond)
					}
					if err != nil {
						t.Fatal(err)
					}
					if math.Abs(cost-.48) > 1e-9 {
						t.Fatalf("cost=%v want .48", cost)
					}
					if prompt != wantPrompt || total != wantTotal || cache != 800 {
						t.Fatalf("quota counters prompt=%d total=%d cache=%d", prompt, total, cache)
					}
					var keyCost float64
					var keyTokens int
					if err = db.QueryRow("SELECT quota_used_cost,quota_used_tokens FROM downstream_keys ORDER BY id LIMIT 1").Scan(&keyCost, &keyTokens); err != nil {
						t.Fatal(err)
					}
					if math.Abs(keyCost-.48) > 1e-9 || keyTokens != wantTotal {
						t.Fatalf("quota amount=%v tokens=%d", keyCost, keyTokens)
					}
				})
			}
		}
	}
}

func TestBillingHTTPFixedFeeWithoutUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	}))
	t.Cleanup(upstream.Close)
	base, token, db := setupServer(t, upstream.URL)
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "gpt-test", PricePerRequest: .05}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("response=%d err=%v", resp.StatusCode, err)
	}
	var cost float64
	var tokens int
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = db.QueryRow("SELECT cost,total_tokens FROM usage_records ORDER BY id DESC LIMIT 1").Scan(&cost, &tokens)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if cost != .05 || tokens != 0 {
		t.Fatalf("cost=%v tokens=%d", cost, tokens)
	}
}
