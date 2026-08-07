package adapters

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestComposeDownstreamSemantics verifies the composition rules: OpenAI
// clients keep the upstream adapter; Anthropic-native upstreams stay verbatim;
// Anthropic clients on non-Anthropic upstreams get a composed adapter.
func TestComposeDownstreamSemantics(t *testing.T) {
	if got := ComposeDownstream(GeminiForwardAdapter{}, ""); got.Name() != "gemini" {
		t.Fatalf("openai downstream must keep upstream, got %q", got.Name())
	}
	if got := ComposeDownstream(GeminiForwardAdapter{}, "openai"); got.Name() != "gemini" {
		t.Fatalf("openai downstream must keep upstream, got %q", got.Name())
	}
	if got := ComposeDownstream(AnthropicForwardAdapter{}, "anthropic"); got.Name() != "anthropic" {
		t.Fatalf("anthropic-native upstream must stay verbatim, got %q", got.Name())
	}
	composed := ComposeDownstream(GeminiForwardAdapter{}, "anthropic")
	if composed.Name() != "composed:anthropic->gemini" {
		t.Fatalf("anthropic downstream on gemini upstream must compose, got %q", composed.Name())
	}
}

// TestComposedAnthropicToGeminiRequest verifies the request chain
// Anthropic Messages → OpenAI pivot → Gemini generateContent produces exactly
// the same bytes as the existing converters applied in sequence.
func TestComposedAnthropicToGeminiRequest(t *testing.T) {
	anthropicBody := []byte(`{
		"model":"gemini-test","max_tokens":64,"stream":false,
		"system":"be brief",
		"messages":[{"role":"user","content":"hello"}]
	}`)
	composed := ComposeDownstream(GeminiForwardAdapter{}, "anthropic")

	upstreamPath, out, err := composed.TransformRequest("messages", anthropicBody)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if upstreamPath == "" {
		t.Fatal("expected upstream path")
	}
	// Reference chain: pivot via the existing converters.
	pivot, err := MessagesToOpenAIChat(anthropicBody)
	if err != nil {
		t.Fatalf("pivot: %v", err)
	}
	wantPath, want, err := chatToGemini(pivot)
	if err != nil {
		t.Fatalf("gemini: %v", err)
	}
	if upstreamPath != wantPath || !bytes.Equal(out, want) {
		t.Fatalf("composed request != reference chain\npath: %q vs %q\nbody: %s\n vs\n%s", upstreamPath, wantPath, out, want)
	}
}

// TestComposedGeminiToAnthropicResponse verifies the response chain
// Gemini generateContent → OpenAI pivot → Anthropic Messages shape.
func TestComposedGeminiToAnthropicResponse(t *testing.T) {
	geminiBody := []byte(`{
		"candidates":[{"content":{"parts":[{"text":"hi there"}],"role":"model"},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}
	}`)
	composed := ComposeDownstream(GeminiForwardAdapter{}, "anthropic")

	out, err := composed.TransformResponse("messages", geminiBody)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, `"content"`) || !strings.Contains(text, "hi there") {
		t.Fatalf("missing content in messages response: %s", text)
	}
	if !strings.Contains(text, `"stop_reason"`) {
		t.Fatalf("missing stop_reason in messages response: %s", text)
	}
	if !strings.Contains(text, `"input_tokens":10`) {
		t.Fatalf("missing input_tokens usage: %s", text)
	}
}

// TestComposedOnOpenAIHook verifies the pivot hook runs between the downstream
// segment and the upstream transform (system prompt injection slot).
func TestComposedOnOpenAIHook(t *testing.T) {
	anthropicBody := []byte(`{"model":"gemini-test","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	composed := ComposeDownstream(GeminiForwardAdapter{}, "anthropic").(*ComposeForwardAdapter)
	hookCalled := false
	composed.OnOpenAI = func(body []byte) ([]byte, error) {
		hookCalled = true
		if !strings.Contains(string(body), `"role":"user"`) {
			t.Fatalf("hook must receive the OpenAI pivot body: %s", body)
		}
		return body, nil
	}
	if _, _, err := composed.TransformRequest("messages", anthropicBody); err != nil {
		t.Fatalf("transform: %v", err)
	}
	if !hookCalled {
		t.Fatal("pivot hook not called")
	}
}

// TestComposedAnthropicStream verifies the stream chain
// Gemini SSE (raw JSON lines) → OpenAI SSE → Anthropic event stream.
func TestComposedAnthropicStream(t *testing.T) {
	geminiSSE := "{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hel\"}],\"role\":\"model\"}}]}\n" +
		"{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"lo\"}],\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"totalTokenCount\":5}}\n"
	composed := ComposeDownstream(GeminiForwardAdapter{}, "anthropic")
	stream, err := composed.WrapStream("messages", io.NopCloser(strings.NewReader(geminiSSE)))
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	out, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = stream.Close()
	text := string(out)
	if !strings.Contains(text, "message_start") {
		t.Fatalf("missing message_start: %s", text)
	}
	if !strings.Contains(text, "content_block_delta") || !strings.Contains(text, "Hel") {
		t.Fatalf("missing content delta: %s", text)
	}
	if !strings.Contains(text, "message_delta") || !strings.Contains(text, `"stop_reason":"end_turn"`) {
		t.Fatalf("missing stop in message_delta: %s", text)
	}
	if !strings.Contains(text, `"input_tokens":3`) {
		t.Fatalf("missing usage passthrough: %s", text)
	}
}
