// Package usage extracts token accounting from OpenAI-compatible responses.
package usage

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
)

// Tokens holds prompt/completion/total token counts from an upstream response.
type Tokens struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Valid reports whether any token field is present.
func (t Tokens) Valid() bool {
	return t.PromptTokens > 0 || t.CompletionTokens > 0 || t.TotalTokens > 0
}

// Normalize fills TotalTokens when only prompt/completion are set.
func (t Tokens) Normalize() Tokens {
	if t.TotalTokens <= 0 && (t.PromptTokens > 0 || t.CompletionTokens > 0) {
		t.TotalTokens = t.PromptTokens + t.CompletionTokens
	}
	return t
}

// ExtractFromJSONBody parses usage from a non-stream OpenAI-style JSON body.
func ExtractFromJSONBody(body []byte) Tokens {
	var payload struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Usage == nil {
		return Tokens{}
	}
	tokens := Tokens{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
	}
	// Responses / Anthropic-shaped aliases.
	if tokens.PromptTokens == 0 && payload.Usage.InputTokens > 0 {
		tokens.PromptTokens = payload.Usage.InputTokens
	}
	if tokens.CompletionTokens == 0 && payload.Usage.OutputTokens > 0 {
		tokens.CompletionTokens = payload.Usage.OutputTokens
	}
	return tokens.Normalize()
}

// ExtractFromSSELine parses one SSE data payload for usage fields.
func ExtractFromSSELine(data string) Tokens {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return Tokens{}
	}
	return ExtractFromJSONBody([]byte(data))
}

// Tee captures usage while copying bytes to the client.
// For non-stream responses it buffers the full body; for stream it scans SSE lines.
type Tee struct {
	Source io.ReadCloser
	Stream bool

	mu     sync.Mutex
	tokens Tokens
	buf    bytes.Buffer
	line   bytes.Buffer
}

func NewTee(source io.ReadCloser, stream bool) *Tee {
	return &Tee{Source: source, Stream: stream}
}

func (t *Tee) Read(p []byte) (int, error) {
	n, err := t.Source.Read(p)
	if n > 0 {
		chunk := p[:n]
		t.mu.Lock()
		if t.Stream {
			t.scanSSE(chunk)
		} else {
			_, _ = t.buf.Write(chunk)
		}
		t.mu.Unlock()
	}
	return n, err
}

func (t *Tee) Close() error {
	t.mu.Lock()
	if !t.Stream && t.buf.Len() > 0 {
		t.tokens = ExtractFromJSONBody(t.buf.Bytes())
	}
	// Flush any trailing SSE line without a final newline.
	if t.Stream && t.line.Len() > 0 {
		t.consumeSSELine(t.line.String())
		t.line.Reset()
	}
	t.mu.Unlock()
	if t.Source != nil {
		return t.Source.Close()
	}
	return nil
}

// Tokens returns the best usage observed so far.
func (t *Tee) Tokens() Tokens {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tokens.Normalize()
}

func (t *Tee) scanSSE(chunk []byte) {
	for _, b := range chunk {
		if b == '\n' {
			line := t.line.String()
			t.line.Reset()
			t.consumeSSELine(line)
			continue
		}
		if b == '\r' {
			continue
		}
		_ = t.line.WriteByte(b)
	}
}

func (t *Tee) consumeSSELine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if strings.HasPrefix(line, "data:") {
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		got := ExtractFromSSELine(payload)
		if got.Valid() {
			// Prefer the latest non-empty usage snapshot (final chunk often has totals).
			t.tokens = got.Normalize()
		}
	}
}

// EstimateCost returns approximate cost using per-1k token prices.
func EstimateCost(promptTokens, completionTokens int, pricePromptPer1k, priceCompletionPer1k float64) float64 {
	cost := 0.0
	if pricePromptPer1k > 0 && promptTokens > 0 {
		cost += (float64(promptTokens) / 1000.0) * pricePromptPer1k
	}
	if priceCompletionPer1k > 0 && completionTokens > 0 {
		cost += (float64(completionTokens) / 1000.0) * priceCompletionPer1k
	}
	return cost
}
