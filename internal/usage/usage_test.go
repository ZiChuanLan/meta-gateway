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
