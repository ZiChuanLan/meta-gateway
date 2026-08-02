package adapters

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestMessagesToOpenAIChat(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"max_tokens": 256,
		"system": "You are helpful.",
		"messages": [
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi"},
			{"role": "user", "content": [{"type": "text", "text": "How are you?"}]}
		],
		"stream": true
	}`)
	converted, err := MessagesToOpenAIChat(body)
	if err != nil {
		t.Fatalf("MessagesToOpenAIChat: %v", err)
	}
	var outbound struct {
		Model         string `json:"model"`
		Stream        bool   `json:"stream"`
		MaxTokens     int    `json:"max_tokens"`
		Messages      []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if outbound.Model != "gpt-4o" || !outbound.Stream || outbound.MaxTokens != 256 {
		t.Fatalf("outbound = %+v", outbound)
	}
	if len(outbound.Messages) != 4 {
		t.Fatalf("messages = %d, want 4 (system + 3)", len(outbound.Messages))
	}
	if outbound.Messages[0].Role != "system" || outbound.Messages[0].Content != "You are helpful." {
		t.Fatalf("messages[0] = %+v", outbound.Messages[0])
	}
	if outbound.Messages[3].Content != "How are you?" {
		t.Fatalf("messages[3] = %+v (part array must flatten)", outbound.Messages[3])
	}
	if !outbound.StreamOptions.IncludeUsage {
		t.Fatalf("stream_options.include_usage must be set")
	}
}

func TestOpenAIChatToMessages(t *testing.T) {
	body := []byte(`{
		"id": "chatcmpl-abc123",
		"object": "chat.completion",
		"model": "gpt-4o",
		"choices": [{"message": {"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 12, "completion_tokens": 3, "total_tokens": 15}
	}`)
	converted, err := OpenAIChatToMessages(body)
	if err != nil {
		t.Fatalf("OpenAIChatToMessages: %v", err)
	}
	var outbound struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Role       string `json:"role"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if outbound.Type != "message" || outbound.Role != "assistant" {
		t.Fatalf("outbound = %+v", outbound)
	}
	if outbound.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %q", outbound.StopReason)
	}
	if len(outbound.Content) != 1 || outbound.Content[0].Text != "Hello!" {
		t.Fatalf("content = %+v", outbound.Content)
	}
	if outbound.Usage.InputTokens != 12 || outbound.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", outbound.Usage)
	}
}

func TestOpenAIStreamToAnthropicStream(t *testing.T) {
	// OpenAI chunks: role, two text deltas, finish + usage.
	upstream := strings.NewReader(
		"data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
			"data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
			"data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n" +
			"data: [DONE]\n\n",
	)
	stream := NewOpenAIStreamToAnthropicStream(io.NopCloser(upstream))
	raw, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"event: message_start",
		`"type":"message"`,
		"event: content_block_delta",
		`"text":"Hel"`,
		`"type":"text_delta"`,
		`"text":"lo"`,
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		`"input_tokens":5`,
		`"output_tokens":2`,
		"event: message_stop",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q in %s", want, text)
		}
	}
}
