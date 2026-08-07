package usage

import (
	"io"
	"strings"
	"testing"
)

func TestExtractFromJSONBody(t *testing.T) {
	got := ExtractFromJSONBody([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	if got.PromptTokens != 10 || got.CompletionTokens != 5 || got.TotalTokens != 15 {
		t.Fatalf("unexpected tokens: %+v", got)
	}
	got = ExtractFromJSONBody([]byte(`{"usage":{"input_tokens":3,"output_tokens":2}}`))
	if got.PromptTokens != 3 || got.CompletionTokens != 2 || got.TotalTokens != 5 {
		t.Fatalf("alias normalize failed: %+v", got)
	}
}

func TestExtractAnthropicCacheUsage(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":40,"cache_creation_input_tokens":20}}`)
	got := ExtractFromJSONBody(body)
	if got.PromptTokens != 100 || got.CompletionTokens != 50 || got.TotalTokens != 150 {
		t.Fatalf("anthropic tokens: %+v", got)
	}
	if got.CacheReadTokens != 40 || got.CacheCreationTokens != 20 {
		t.Fatalf("anthropic cache tokens: %+v", got)
	}
}

func TestExtractOpenAICachedPromptTokens(t *testing.T) {
	body := []byte(`{"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":90}}}`)
	got := ExtractFromJSONBody(body)
	if got.CacheReadTokens != 90 {
		t.Fatalf("openai cached_tokens: %+v", got)
	}
	if got.PromptTokens != 120 {
		t.Fatalf("prompt tokens: %+v", got)
	}
}

func TestExtractAnthropicMessageStartUsage(t *testing.T) {
	body := []byte(`{"type":"message_start","message":{"usage":{"input_tokens":12,"cache_read_input_tokens":4}}}`)
	got := ExtractFromJSONBody(body)
	if got.PromptTokens != 12 || got.TotalTokens != 12 || got.CacheReadTokens != 4 {
		t.Fatalf("message_start usage: %+v", got)
	}
}

func TestExtractGeminiUsageMetadata(t *testing.T) {
	body := []byte(`{"candidates":[{}],"usageMetadata":{"promptTokenCount":25,"candidatesTokenCount":8,"totalTokenCount":33,"cachedContentTokenCount":7}}`)
	got := ExtractFromJSONBody(body)
	if got.PromptTokens != 25 || got.CompletionTokens != 8 || got.TotalTokens != 33 {
		t.Fatalf("gemini tokens: %+v", got)
	}
	if got.CacheReadTokens != 7 {
		t.Fatalf("gemini cached content: %+v", got)
	}
}

func TestExtractGeminiUsageMetadataWithoutTotal(t *testing.T) {
	body := []byte(`{"usageMetadata":{"promptTokenCount":25,"candidatesTokenCount":8,"cachedContentTokenCount":7}}`)
	got := ExtractFromJSONBody(body)
	if got.PromptTokens != 25 || got.CompletionTokens != 8 || got.TotalTokens != 33 {
		t.Fatalf("gemini tokens without total: %+v", got)
	}
	if got.CacheReadTokens != 7 {
		t.Fatalf("gemini cached content without total: %+v", got)
	}
}

func TestExtractAdapterPipelineAliases(t *testing.T) {
	// Converted Anthropic stream chunks carry the internal aliases.
	body := []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cache_read_tokens":3,"cache_creation_tokens":2}}`)
	got := ExtractFromJSONBody(body)
	if got.CacheReadTokens != 3 || got.CacheCreationTokens != 2 {
		t.Fatalf("pipeline aliases: %+v", got)
	}
}

func TestTeeStreamMergesSplitUsage(t *testing.T) {
	sse := `data: {"usage":{"prompt_tokens":50}}

data: {"usage":{"completion_tokens":20}}
`
	tee := NewTee(io.NopCloser(strings.NewReader(sse)), true)
	if _, err := io.ReadAll(tee); err != nil {
		t.Fatal(err)
	}
	_ = tee.Close()
	got := tee.Tokens()
	if got.PromptTokens != 50 || got.CompletionTokens != 20 || got.TotalTokens != 70 {
		t.Fatalf("split usage: %+v", got)
	}
}

func TestTeeStreamBareJSONLines(t *testing.T) {
	// Gemini-style stream: one complete JSON object per line, no "data:" prefix.
	gemini := "{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n" +
		"{\"candidates\":[],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2,\"totalTokenCount\":7}}\n"
	tee := NewTee(io.NopCloser(strings.NewReader(gemini)), true)
	if _, err := io.ReadAll(tee); err != nil {
		t.Fatal(err)
	}
	_ = tee.Close()
	tokens := tee.Tokens()
	if tokens.TotalTokens != 7 || tokens.PromptTokens != 5 || tokens.CompletionTokens != 2 {
		t.Fatalf("bare json lines: %+v", tokens)
	}
}

func TestTeeNonStream(t *testing.T) {
	body := `{"id":"x","usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`
	tee := NewTee(io.NopCloser(strings.NewReader(body)), false)
	data, err := io.ReadAll(tee)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatalf("body mutated")
	}
	_ = tee.Close()
	tokens := tee.Tokens()
	if tokens.TotalTokens != 10 {
		t.Fatalf("got %+v", tokens)
	}
}

func TestTeeStream(t *testing.T) {
	sse := "data: {\"choices\":[]}\n\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\ndata: [DONE]\n\n"
	tee := NewTee(io.NopCloser(strings.NewReader(sse)), true)
	if _, err := io.ReadAll(tee); err != nil {
		t.Fatal(err)
	}
	_ = tee.Close()
	tokens := tee.Tokens()
	if tokens.TotalTokens != 3 {
		t.Fatalf("got %+v", tokens)
	}
}

func TestEstimateCost(t *testing.T) {
	cost := EstimateCost(1000, 500, 0.01, 0.02)
	if cost < 0.0199 || cost > 0.0201 {
		t.Fatalf("cost=%v", cost)
	}
}

func TestTeeStreamChunkedCRLF(t *testing.T) {
	// SSE with CRLF framing, delivered one byte at a time so every line
	// boundary falls inside the trailing-partial-line buffer (no newline ever
	// arrives in the same Read as its payload bytes).
	sse := "data: {\"choices\":[]}\r\n\r\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\r\n\r\ndata: [DONE]\r\n\r\n"
	tee := NewTee(io.NopCloser(strings.NewReader(sse)), true)
	buf := make([]byte, 1)
	for {
		if _, err := tee.Read(buf); err != nil {
			break
		}
	}
	_ = tee.Close()
	tokens := tee.Tokens()
	if tokens.TotalTokens != 3 || tokens.PromptTokens != 1 || tokens.CompletionTokens != 2 {
		t.Fatalf("chunked CRLF scan: %+v", tokens)
	}
}

func TestTeeStreamTrailingLineWithoutNewline(t *testing.T) {
	// Final SSE chunk carries usage but no trailing newline; Close must flush it.
	sse := "data: {\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":1,\"total_tokens\":10}}"
	tee := NewTee(io.NopCloser(strings.NewReader(sse)), true)
	if _, err := io.ReadAll(tee); err != nil {
		t.Fatal(err)
	}
	_ = tee.Close()
	tokens := tee.Tokens()
	if tokens.TotalTokens != 10 {
		t.Fatalf("trailing line flush: %+v", tokens)
	}
}
