package adapters

import (
	"encoding/json"
	"io"
	"sort"
)

// ProtocolPair identifies one direction of an N×M protocol translation,
// e.g. {From: "anthropic", To: "openai"} converts a native Anthropic request
// into the OpenAI wire format for an OpenAI-compatible upstream.
type ProtocolPair struct {
	From string
	To   string
}

func (p ProtocolPair) String() string { return p.From + "→" + p.To }

// Translation holds the three independent modes a protocol pair can translate:
// request/response bodies, SSE streams, and token counting. A nil function
// means that mode is not supported for the pair; callers fall back (the
// registry's fallback rewrites only the model field for unsupported pairs).
type Translation struct {
	// Body maps a request body (and its path) from From to To. The returned
	// path is the To-side endpoint (e.g. "messages" for anthropic).
	Body func(fromPath string, body []byte) (toPath string, out []byte, err error)
	// Response maps a To-side 2xx body back to From format.
	Response func(path string, body []byte) ([]byte, error)
	// Stream wraps a To-side streaming body so it reads as From SSE chunks.
	Stream func(path string, body io.ReadCloser) (io.ReadCloser, error)
	// CountTokens counts the From-side request body locally. ok=false means
	// the pair has no local counter (callers forward to the upstream).
	CountTokens func(body []byte) (count int64, ok bool, err error)
}

// TranslationRegistry organizes protocol translators as a (from, to) matrix.
// Unregistered pairs fall back to ModelRewriteFallback (rewrite the model
// field, pass everything else through untouched).
type TranslationRegistry struct {
	pairs map[ProtocolPair]Translation
}

// NewTranslationRegistry builds the registry with the built-in matrix:
//
//	openai    → openai     (passthrough)
//	anthropic → openai     (native Anthropic clients on OpenAI upstreams)
//	openai    → anthropic  (OpenAI clients on Anthropic upstreams)
//	anthropic → anthropic  (passthrough; count_tokens forwarded upstream)
//	openai    → gemini     (OpenAI clients on Gemini upstreams)
func NewTranslationRegistry() *TranslationRegistry {
	r := &TranslationRegistry{pairs: make(map[ProtocolPair]Translation)}
	openAI := OpenAIPassthroughAdapter{}

	// openai → openai: verbatim passthrough.
	r.Register(ProtocolPair{From: "openai", To: "openai"}, Translation{
		Body:        openAI.TransformRequest,
		Response:    openAI.TransformResponse,
		Stream:      openAI.WrapStream,
		CountTokens: nil,
	})

	// anthropic → openai: native Anthropic Messages on an OpenAI upstream.
	anthropicDown := AnthropicDownstreamSegment{}
	r.Register(ProtocolPair{From: "anthropic", To: "openai"}, Translation{
		Body:     anthropicDown.ToOpenAI,
		Response: anthropicDown.FromOpenAI,
		Stream: func(_ string, body io.ReadCloser) (io.ReadCloser, error) {
			return anthropicDown.WrapOpenAIStream(body), nil
		},
		CountTokens: nil,
	})

	// openai → anthropic: OpenAI chat/completions on an Anthropic upstream.
	anthropicFwd := AnthropicForwardAdapter{}
	r.Register(ProtocolPair{From: "openai", To: "anthropic"}, Translation{
		Body:        anthropicFwd.TransformRequest,
		Response:    anthropicFwd.TransformResponse,
		Stream:      anthropicFwd.WrapStream,
		CountTokens: nil,
	})

	// anthropic → anthropic: native passthrough; local counting unavailable
	// (forwarded upstream).
	r.Register(ProtocolPair{From: "anthropic", To: "anthropic"}, Translation{
		Body: func(fromPath string, body []byte) (string, []byte, error) {
			return fromPath, body, nil
		},
		Response:    func(_ string, body []byte) ([]byte, error) { return body, nil },
		Stream:      func(_ string, body io.ReadCloser) (io.ReadCloser, error) { return body, nil },
		CountTokens: nil,
	})

	// openai → gemini: OpenAI chat/completions on a Gemini upstream.
	gemini := GeminiForwardAdapter{}
	r.Register(ProtocolPair{From: "openai", To: "gemini"}, Translation{
		Body:        gemini.TransformRequest,
		Response:    gemini.TransformResponse,
		Stream:      gemini.WrapStream,
		CountTokens: nil,
	})

	return r
}

// Register installs (or replaces) the translation for a protocol pair.
func (r *TranslationRegistry) Register(pair ProtocolPair, tr Translation) {
	r.pairs[pair] = tr
}

// Lookup returns the registered translation for the pair.
func (r *TranslationRegistry) Lookup(from, to string) (Translation, bool) {
	tr, ok := r.pairs[ProtocolPair{From: canonical(from), To: canonical(to)}]
	return tr, ok
}

// Translate applies the pair's body translation. For an unregistered pair it
// applies the model-rewrite fallback: the request body's model field is
// rewritten (aliases must not leak upstream) and everything else passes
// through verbatim. ok reports whether a dedicated translation was found.
func (r *TranslationRegistry) Translate(from, to, fromPath string, body []byte) (toPath string, out []byte, tr Translation, ok bool, err error) {
	tr, ok = r.Lookup(from, to)
	if ok && tr.Body != nil {
		toPath, out, err = tr.Body(fromPath, body)
		return toPath, out, tr, true, err
	}
	// Fallback: rewrite only the model field, pass everything else verbatim.
	return fromPath, ModelRewriteFallback(body, modelFromBody(body)), Translation{
		Response: func(_ string, b []byte) ([]byte, error) { return b, nil },
		Stream:   func(_ string, b io.ReadCloser) (io.ReadCloser, error) { return b, nil },
	}, false, nil
}

// StreamFor returns the pair's stream translation (nil when unsupported).
func (r *TranslationRegistry) StreamFor(from, to string) func(path string, body io.ReadCloser) (io.ReadCloser, error) {
	tr, ok := r.Lookup(from, to)
	if !ok || tr.Stream == nil {
		return nil
	}
	return tr.Stream
}

// CountTokensFor runs the pair's local token counter. ok=false means the pair
// has no local counter (callers forward count_tokens upstream).
func (r *TranslationRegistry) CountTokensFor(from, to string, body []byte) (count int64, ok bool, err error) {
	tr, found := r.Lookup(from, to)
	if !found || tr.CountTokens == nil {
		return 0, false, nil
	}
	return tr.CountTokens(body)
}

// Pairs lists the registered matrix (sorted, for audits/tests).
func (r *TranslationRegistry) Pairs() []ProtocolPair {
	out := make([]ProtocolPair, 0, len(r.pairs))
	for pair := range r.pairs {
		out = append(out, pair)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// modelFromBody extracts the request body's model field ("" when absent or
// unparseable).
func modelFromBody(body []byte) string {
	var doc struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return ""
	}
	return doc.Model
}

// ModelRewriteFallback rewrites the model field of an OpenAI-style request
// body, preserving every other field byte-for-byte. A non-JSON or model-less
// body passes through unchanged (the fallback never blocks a request).
func ModelRewriteFallback(body []byte, model string) []byte {
	if model == "" {
		return body
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body
	}
	doc["model"] = model
	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return out
}
