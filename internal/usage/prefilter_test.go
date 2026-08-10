package usage

import (
	"io"
	"strings"
	"testing"
)

// TestTeeStreamPrefilterSkipsNonUsageLines verifies stream lines without the
// "usage" marker never reach the JSON parser (behavioral check: the extracted
// tokens come only from usage-bearing lines, and content-only streams yield
// nothing).
func TestTeeStreamPrefilterSkipsNonUsageLines(t *testing.T) {
	stream := "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: [DONE]\n\n"
	tee := NewTee(io.NopCloser(strings.NewReader(stream)), true)
	_, err := io.Copy(io.Discard, tee)
	if err != nil {
		t.Fatal(err)
	}
	if err := tee.Close(); err != nil {
		t.Fatal(err)
	}
	if tokens := tee.Tokens(); tokens.Valid() {
		t.Fatalf("content-only stream must not yield usage, got %+v", tokens)
	}
}

// TestTeeStreamExtractsUsageFromPrefilteredLine verifies usage-bearing lines
// still parse after the pre-filter.
func TestTeeStreamExtractsUsageFromPrefilteredLine(t *testing.T) {
	stream := "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"id\":\"x\",\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":8,\"total_tokens\":20}}\n\n"
	tee := NewTee(io.NopCloser(strings.NewReader(stream)), true)
	_, err := io.Copy(io.Discard, tee)
	if err != nil {
		t.Fatal(err)
	}
	if err := tee.Close(); err != nil {
		t.Fatal(err)
	}
	tokens := tee.Tokens()
	if tokens.PromptTokens != 12 || tokens.CompletionTokens != 8 || tokens.TotalTokens != 20 {
		t.Fatalf("tokens=%+v", tokens)
	}
}

// TestTeeNonStreamSkipsParseWithoutUsageMarker verifies a non-stream body
// without the usage marker skips the full-body JSON parse (empty result,
// no error), while a usage-bearing body is still extracted.
func TestTeeNonStreamSkipsParseWithoutUsageMarker(t *testing.T) {
	// No usage marker anywhere.
	tee := NewTee(io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"plain"}}]}`)), false)
	_, err := io.Copy(io.Discard, tee)
	if err != nil {
		t.Fatal(err)
	}
	if err := tee.Close(); err != nil {
		t.Fatal(err)
	}
	if tokens := tee.Tokens(); tokens.Valid() {
		t.Fatalf("body without usage must yield nothing, got %+v", tokens)
	}

	// Usage present (at the end, OpenAI-style) → extracted.
	withUsage := `{"choices":[{"message":{"content":"long answer"}}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`
	tee2 := NewTee(io.NopCloser(strings.NewReader(withUsage)), false)
	_, err = io.Copy(io.Discard, tee2)
	if err != nil {
		t.Fatal(err)
	}
	if err := tee2.Close(); err != nil {
		t.Fatal(err)
	}
	tokens := tee2.Tokens()
	if tokens.PromptTokens != 3 || tokens.CompletionTokens != 4 || tokens.TotalTokens != 7 {
		t.Fatalf("tokens=%+v", tokens)
	}
}
