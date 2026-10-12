// Package usage extracts token accounting from OpenAI-compatible responses.
package usage

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
)

// Tokens holds prompt/completion/total token counts from an upstream response,
// plus cache-token detail (Anthropic cache_read/cache_creation, OpenAI
// prompt_tokens_details.cached_tokens, Gemini usageMetadata.cachedContentTokenCount).
type Tokens struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// CacheReadTokens are tokens served from an upstream prompt cache.
	CacheReadTokens int
	// CacheCreationTokens are tokens written into an upstream prompt cache.
	CacheCreationTokens int
	// PromptIncludesCacheRead identifies protocols whose prompt/input count
	// already contains cache hits (OpenAI, Responses and Gemini). The original
	// count remains intact for logs and token quotas; only billing subtracts it.
	PromptIncludesCacheRead bool
	Estimated               bool
}

// Valid reports whether any token field is present.
func (t Tokens) Valid() bool {
	return t.PromptTokens > 0 || t.CompletionTokens > 0 || t.TotalTokens > 0 || t.CacheReadTokens > 0 || t.CacheCreationTokens > 0
}

// Source identifies metered, estimated or missing usage without inventing counts.
func (t Tokens) Source() string {
	if t.Estimated {
		return "estimated"
	}
	if t.Valid() {
		return "upstream"
	}
	return "missing"
}

// Normalize fills TotalTokens when only prompt/completion are set.
func (t Tokens) Normalize() Tokens {
	if t.TotalTokens <= 0 && (t.PromptTokens > 0 || t.CompletionTokens > 0) {
		t.TotalTokens = t.PromptTokens + t.CompletionTokens
	}
	return t
}

// BillingInputTokens returns ordinary input plus cache creation, without
// counting cache reads a second time. Zero-valued protocol metadata preserves
// the legacy/internal Anthropic convention: prompt excludes cache detail.
func (t Tokens) BillingInputTokens() int {
	prompt := max(t.PromptTokens, 0)
	if t.PromptIncludesCacheRead {
		prompt = max(prompt-max(t.CacheReadTokens, 0), 0)
	}
	return prompt + max(t.CacheCreationTokens, 0)
}

// ExtractFromJSONBody parses usage from a non-stream OpenAI-style JSON body.
// It understands, in addition to OpenAI's usage object:
//   - Anthropic: usage.cache_read_input_tokens / usage.cache_creation_input_tokens
//   - OpenAI:    usage.prompt_tokens_details.cached_tokens
//   - Gemini:    top-level usageMetadata.promptTokenCount / candidatesTokenCount /
//     totalTokenCount / cachedContentTokenCount
//   - internal:  usage.cache_read_tokens / usage.cache_creation_tokens (adapter pipeline)
type usagePayload struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	// Anthropic cache accounting.
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	// Internal adapter-pipeline aliases (converted streams).
	CacheReadTokens     int `json:"cache_read_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	// OpenAI prompt-token cache detail.
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func ExtractFromJSONBody(body []byte) Tokens {
	var payload struct {
		Usage *usagePayload `json:"usage"`
		// Anthropic message_start places usage under message.usage.
		Message *struct {
			Usage *usagePayload `json:"usage"`
		} `json:"message"`
		// OpenAI Responses streams embed usage under response.usage in the
		// terminal response.completed event.
		Response *struct {
			Usage *usagePayload `json:"usage"`
		} `json:"response"`
		// Gemini top-level usageMetadata (generateContent / streamGenerateContent).
		UsageMetadata *struct {
			PromptTokenCount        int `json:"promptTokenCount"`
			CandidatesTokenCount    int `json:"candidatesTokenCount"`
			TotalTokenCount         int `json:"totalTokenCount"`
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Tokens{}
	}
	tokens := Tokens{}
	rawUsage := payload.Usage
	if rawUsage == nil && payload.Message != nil {
		rawUsage = payload.Message.Usage
	}
	if rawUsage == nil && payload.Response != nil {
		rawUsage = payload.Response.Usage
	}
	if rawUsage != nil {
		tokens = Tokens{
			PromptTokens:        rawUsage.PromptTokens,
			CompletionTokens:    rawUsage.CompletionTokens,
			TotalTokens:         rawUsage.TotalTokens,
			CacheReadTokens:     rawUsage.CacheReadTokens,
			CacheCreationTokens: rawUsage.CacheCreationTokens,
		}
		// OpenAI cached prompt tokens take precedence over Anthropic aliases
		// (the OpenAI field is the authoritative cache-hit count).
		if rawUsage.PromptTokensDetails != nil {
			tokens.PromptIncludesCacheRead = true
			if tokens.CacheReadTokens == 0 {
				tokens.CacheReadTokens = rawUsage.PromptTokensDetails.CachedTokens
			}
		} else if rawUsage.InputTokensDetails != nil {
			tokens.PromptIncludesCacheRead = true
			if tokens.CacheReadTokens == 0 {
				tokens.CacheReadTokens = rawUsage.InputTokensDetails.CachedTokens
			}
		}
		// Anthropic cache accounting.
		if tokens.CacheReadTokens == 0 {
			tokens.CacheReadTokens = rawUsage.CacheReadInputTokens
		}
		if tokens.CacheCreationTokens == 0 {
			tokens.CacheCreationTokens = rawUsage.CacheCreationInputTokens
		}
		// Responses / Anthropic-shaped aliases.
		if tokens.PromptTokens == 0 && rawUsage.InputTokens > 0 {
			tokens.PromptTokens = rawUsage.InputTokens
		}
		if tokens.CompletionTokens == 0 && rawUsage.OutputTokens > 0 {
			tokens.CompletionTokens = rawUsage.OutputTokens
		}
	}
	if payload.UsageMetadata != nil {
		// Gemini shape: usageMetadata at the top level of the response. Some
		// compatible proxies omit totalTokenCount, so Normalize below must be
		// allowed to derive it from prompt/completion counts.
		tokens.PromptIncludesCacheRead = true
		tokens.PromptTokens = payload.UsageMetadata.PromptTokenCount
		tokens.CompletionTokens = payload.UsageMetadata.CandidatesTokenCount
		tokens.TotalTokens = payload.UsageMetadata.TotalTokenCount
		if tokens.CacheReadTokens == 0 {
			tokens.CacheReadTokens = payload.UsageMetadata.CachedContentTokenCount
		}
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

	mu        sync.Mutex
	tokens    Tokens
	buf       bytes.Buffer
	line      bytes.Buffer
	closeOnce sync.Once
	closeErr  error
}

func NewTee(source io.ReadCloser, stream bool) *Tee {
	return &Tee{Source: source, Stream: stream}
}

// Read copies from Source, then scans under the accounting lock. Close may
// run concurrently to interrupt an idle upstream; no buffer is touched by IO.
func (t *Tee) Read(p []byte) (int, error) {
	n, err := t.Source.Read(p)
	if n > 0 {
		chunk := p[:n]
		t.mu.Lock()
		if t.Stream {
			if got := t.scanSSE(chunk); got.Valid() {
				t.tokens = mergeTokens(t.tokens, got)
			}
		} else {
			t.buf.Write(chunk)
		}
		t.mu.Unlock()
	}
	return n, err
}

func (t *Tee) Close() error {
	t.closeOnce.Do(func() { t.closeErr = t.close() })
	return t.closeErr
}

func (t *Tee) close() error {
	t.mu.Lock()
	// Flush any trailing SSE line without a final newline.
	if t.Stream && t.line.Len() > 0 {
		if got := t.consumeSSELine(t.line.String()); got.Valid() {
			t.tokens = mergeTokens(t.tokens, got)
		}
		t.line.Reset()
	}
	if !t.Stream && t.buf.Len() > 0 {
		// Most upstreams answer without any usage block (or the response is a
		// non-completions payload); skip the full-body JSON parse unless the
		// usage marker is actually present. Note: usage sits at the END of
		// OpenAI-compatible bodies, so a fixed head-truncation would lose it —
		// keyword presence is the safe filter.
		if bytes.Contains(t.buf.Bytes(), []byte("usage")) {
			t.tokens = ExtractFromJSONBody(t.buf.Bytes())
		}
		t.buf = bytes.Buffer{}
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

// mergeTokens combines usage snapshots that may split prompt/completion and
// cache fields across stream events, as Anthropic does in message_start and
// message_delta. Non-zero fields in the newer snapshot replace older values;
// omitted fields remain available for the final accounting record.
func mergeTokens(previous, next Tokens) Tokens {
	if next.PromptTokens > 0 {
		previous.PromptTokens = next.PromptTokens
		previous.PromptIncludesCacheRead = next.PromptIncludesCacheRead
	}
	if next.PromptIncludesCacheRead {
		previous.PromptIncludesCacheRead = true
	}
	if next.CompletionTokens > 0 {
		previous.CompletionTokens = next.CompletionTokens
	}
	if next.PromptTokens == 0 || next.CompletionTokens == 0 {
		// ExtractFromJSONBody normalizes a partial event into its local total.
		// Do not let that derived partial total replace the merged total.
		previous.TotalTokens = previous.PromptTokens + previous.CompletionTokens
	} else if next.TotalTokens > 0 {
		previous.TotalTokens = next.TotalTokens
	} else {
		previous.TotalTokens = previous.PromptTokens + previous.CompletionTokens
	}
	if next.CacheReadTokens > 0 {
		previous.CacheReadTokens = next.CacheReadTokens
	}
	if next.CacheCreationTokens > 0 {
		previous.CacheCreationTokens = next.CacheCreationTokens
	}
	return previous.Normalize()
}

// scanSSE consumes complete lines from chunk (buffering a trailing partial
// line in t.line) and returns the most recent valid usage snapshot seen.
// Line scanning uses IndexByte instead of a per-byte loop; CR is dropped to
// keep SSE CRLF framing compatible.
func (t *Tee) scanSSE(chunk []byte) Tokens {
	var last Tokens
	for len(chunk) > 0 {
		idx := bytes.IndexByte(chunk, '\n')
		if idx < 0 {
			t.line.Write(chunk)
			break
		}
		line := append(t.line.Bytes(), chunk[:idx]...)
		t.line.Reset()
		chunk = chunk[idx+1:]
		if i := bytes.IndexByte(line, '\r'); i >= 0 {
			line = bytes.ReplaceAll(line, []byte{'\r'}, nil)
		}
		if got := t.consumeSSELine(string(line)); got.Valid() {
			last = mergeTokens(last, got)
		}
	}
	return last
}

// consumeSSELine extracts usage from one SSE line. Returns the parsed tokens
// (possibly zero-valued); callers merge snapshots.
func (t *Tee) consumeSSELine(line string) Tokens {
	line = strings.TrimSpace(line)
	if line == "" {
		return Tokens{}
	}
	payload := ""
	switch {
	case strings.HasPrefix(line, "data:"):
		payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	case strings.HasPrefix(line, "{"):
		// Gemini-style streams emit one complete JSON object per line with no
		// "data:" prefix; treat the bare JSON line as an SSE payload.
		payload = line
	default:
		return Tokens{}
	}
	// Cheap pre-filter: the overwhelming majority of stream lines are content
	// deltas that cannot carry usage (OpenAI usage / Gemini usageMetadata are
	// the only carriers). Skipping the full JSON parse for them keeps long
	// streams near-zero overhead instead of one allocation-heavy Unmarshal per
	// line.
	if !strings.Contains(payload, "usage") {
		return Tokens{}
	}
	return ExtractFromSSELine(payload)
}
