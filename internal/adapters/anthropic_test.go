package adapters_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
)

func TestJoinAnthropicPath(t *testing.T) {
	got, err := adapters.JoinAnthropicPath("https://api.anthropic.com", "messages")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("got %s", got)
	}
	got, err = adapters.JoinAnthropicPath("https://api.anthropic.com/v1", "models")
	if err != nil || got != "https://api.anthropic.com/v1/models" {
		t.Fatalf("got %s err=%v", got, err)
	}
}

func TestIsAnthropicFamily(t *testing.T) {
	if !adapters.IsAnthropicFamily("anthropic", "") {
		t.Fatal("expected anthropic family")
	}
	if !adapters.IsAnthropicFamily("claude-official", "") {
		t.Fatal("expected claude-official family")
	}
	if adapters.IsAnthropicFamily("openai-compatible", "") {
		t.Fatal("openai should not be anthropic")
	}
}

func TestChatToAnthropicMessages(t *testing.T) {
	body := []byte(`{
		"model":"claude-3-5-sonnet-latest",
		"messages":[
			{"role":"system","content":"be brief"},
			{"role":"user","content":"hi"}
		],
		"max_tokens":128,
		"stream":false
	}`)
	out, err := adapters.ChatToAnthropicMessages(body)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "claude-3-5-sonnet-latest" {
		t.Fatalf("model=%v", parsed["model"])
	}
	if parsed["system"] != "be brief" {
		t.Fatalf("system=%v", parsed["system"])
	}
	if parsed["max_tokens"].(float64) != 128 {
		t.Fatalf("max_tokens=%v", parsed["max_tokens"])
	}
	messages, _ := parsed["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages=%v", messages)
	}
}

func TestAnthropicForwardAdapterPreservesNativeMessages(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","usage":{"input_tokens":3,"output_tokens":1}}`)
	adapter := adapters.AnthropicForwardAdapter{}
	converted, err := adapter.TransformResponse("messages", body)
	if err != nil {
		t.Fatal(err)
	}
	if string(converted) != string(body) {
		t.Fatalf("native response changed: got %s", converted)
	}

	stream, err := adapter.WrapStream("messages", io.NopCloser(strings.NewReader("event: message_start\n\ndata: {}\n\n")))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	streamBody, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(streamBody), "event: message_start") {
		t.Fatalf("native stream changed: %s", streamBody)
	}
}

func TestAnthropicMessagesToChat(t *testing.T) {
	body := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-sonnet-latest",
		"content":[{"type":"text","text":"hello"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":3,"output_tokens":1}
	}`)
	out, err := adapters.AnthropicMessagesToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"object":"chat.completion"`) {
		t.Fatalf("body=%s", out)
	}
	if !strings.Contains(string(out), `"hello"`) {
		t.Fatalf("missing text: %s", out)
	}
}
