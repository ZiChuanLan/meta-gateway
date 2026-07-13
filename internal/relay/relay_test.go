package relay_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/relay"
)

// mockUpstream simulates an OpenAI-compatible chat completions endpoint.
func mockUpstream(t *testing.T, stream bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		model, _ := req["model"].(string)
		_ = model

		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "data: {\"id\":\"chatcmpl-abc\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-3.5-turbo\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\ndata: [DONE]\n\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      "chatcmpl-abc",
				"object":  "chat.completion",
				"model":   "gpt-3.5-turbo",
				"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "Hello!"}}},
			})
		}
	}))
}

func TestChatCompletionsNonStream(t *testing.T) {
	upstream := mockUpstream(t, false)
	defer upstream.Close()

	r := relay.New()
	reqBody := `{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"hi"}]}`
	result := r.ChatCompletions(upstream.URL+"/v1/chat/completions", "test-api-key", []byte(reqBody), false)

	if result.Err != nil {
		t.Fatal(result.Err)
	}
	defer result.Body.Close()

	if result.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", result.StatusCode)
	}
	if result.LatencyMs <= 0 {
		t.Error("expected positive latency")
	}

	body, _ := io.ReadAll(result.Body)
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["model"] != "gpt-3.5-turbo" {
		t.Fatalf("expected model gpt-3.5-turbo, got %v", resp["model"])
	}
}

func TestChatCompletionsStream(t *testing.T) {
	upstream := mockUpstream(t, true)
	defer upstream.Close()

	r := relay.New()
	reqBody := `{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"hi"}],"stream":true}`
	result := r.ChatCompletions(upstream.URL+"/v1/chat/completions", "test-api-key", []byte(reqBody), true)

	if result.Err != nil {
		t.Fatal(result.Err)
	}
	defer result.Body.Close()

	if result.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", result.StatusCode)
	}

	body, _ := io.ReadAll(result.Body)
	if !strings.Contains(string(body), "data:") {
		t.Fatalf("expected SSE data, got: %s", string(body))
	}
	if !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("expected [DONE] terminator")
	}
}

func TestModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{
				{"id": "gpt-3.5-turbo", "object": "model"},
				{"id": "gpt-4", "object": "model"},
			},
		})
	}))
	defer upstream.Close()

	r := relay.New()
	body, err := r.Models(upstream.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, got %T", resp["data"])
	}
	if len(data) != 2 {
		t.Fatalf("expected 2 models, got %d", len(data))
	}
}
