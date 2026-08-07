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

func TestAnthropicToOpenAIStreamMergesSplitUsage(t *testing.T) {
	anthropicSSE := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_split","type":"message","role":"assistant","model":"claude-3-5","content":[],"usage":{"input_tokens":50}}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`,
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
	text := string(out)
	if !strings.Contains(text, `"prompt_tokens":50`) || !strings.Contains(text, `"completion_tokens":20`) {
		t.Fatalf("split usage was not preserved: %s", text)
	}
}

func TestAnthropicToOpenAIStreamCacheUsage(t *testing.T) {
	// message_delta usage carries Anthropic cache accounting; the converter must
	// pass it through as internal pipeline aliases so downstream metering can
	// record cache reads/creations.
	anthropicSSE := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_c","type":"message","role":"assistant","model":"claude-3-5","content":[]}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":12,"cache_creation_input_tokens":8}}`,
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
	if !strings.Contains(text, `"prompt_tokens":50`) || !strings.Contains(text, `"completion_tokens":20`) {
		t.Fatalf("missing base usage: %s", text)
	}
	if !strings.Contains(text, `"cache_read_tokens":12`) {
		t.Fatalf("missing cache_read passthrough: %s", text)
	}
	if !strings.Contains(text, `"cache_creation_tokens":8`) {
		t.Fatalf("missing cache_creation passthrough: %s", text)
	}
}
