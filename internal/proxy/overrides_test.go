package proxy

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestInjectSystemPrompt(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	out := injectSystemPrompt(body, "You are a gateway.")
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Messages) != 2 || payload.Messages[0].Role != "system" || payload.Messages[0].Content != "You are a gateway." {
		t.Fatalf("messages = %+v", payload.Messages)
	}
	// Idempotent: same prompt again → unchanged.
	out2 := injectSystemPrompt(out, "You are a gateway.")
	var payload2 struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.Unmarshal(out2, &payload2)
	if len(payload2.Messages) != 2 {
		t.Fatalf("idempotency broken: %d messages", len(payload2.Messages))
	}
	// Non-JSON body untouched.
	raw := []byte("not json")
	if string(injectSystemPrompt(raw, "x")) != "not json" {
		t.Fatal("non-JSON body must pass through")
	}
}

func TestMergeHeaderOverrides(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer abc")
	headers.Set("X-Keep", "yes")
	if err := mergeHeaderOverrides(headers, `{"X-Add":"1","X-Keep":"override","Host":"evil.example"}`); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if headers.Get("X-Add") != "1" {
		t.Fatalf("X-Add = %q", headers.Get("X-Add"))
	}
	if headers.Get("X-Keep") != "override" {
		t.Fatalf("X-Keep = %q", headers.Get("X-Keep"))
	}
	if headers.Get("Host") != "" {
		t.Fatalf("Host must be blocked, got %q", headers.Get("Host"))
	}
	if headers.Get("Authorization") != "Bearer abc" {
		t.Fatalf("Authorization must be kept, got %q", headers.Get("Authorization"))
	}
	// Invalid JSON errors.
	if err := mergeHeaderOverrides(headers, `{broken`); err == nil {
		t.Fatal("invalid JSON must error")
	}
	// Empty string is a no-op.
	if err := mergeHeaderOverrides(headers, "  "); err != nil {
		t.Fatalf("empty: %v", err)
	}
}
