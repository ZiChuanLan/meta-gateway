package adapters_test

import (
	"io"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
)

func TestAnthropicToOpenAIStream(t *testing.T) {
	anthropicSSE := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_x","type":"message","role":"assistant","model":"claude-3-5","content":[]}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":3,"output_tokens":2}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	stream := adapters.NewAnthropicToOpenAIStream(io.NopCloser(strings.NewReader(anthropicSSE)))
	out, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	text := string(out)
	if !strings.Contains(text, `"object":"chat.completion.chunk"`) {
		t.Fatalf("missing chunk object: %s", text)
	}
	if !strings.Contains(text, `"content":"Hel"`) && !strings.Contains(text, `"content":"Hello"`) {
		// role+first content may be combined or split
		if !strings.Contains(text, "Hel") || !strings.Contains(text, "lo") {
			t.Fatalf("missing content deltas: %s", text)
		}
	}
	if !strings.Contains(text, "data: [DONE]") {
		t.Fatalf("missing DONE: %s", text)
	}
	if !strings.Contains(text, `"finish_reason":"stop"`) {
		t.Fatalf("missing finish: %s", text)
	}
}
