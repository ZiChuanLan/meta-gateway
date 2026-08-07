// Intermediate-format conversion chain: every protocol adapter is expressed as
// a segment that converts to/from the internal OpenAI chat/completions format
// (the "pivot"). Downstream (client-side) protocols compose with upstream
// (channel-side) adapters so a client of one protocol can be served by a
// channel of another without writing an N×M matrix of conversions.
//
// Chain model (sub2api-style pivot, see docs/architecture.md):
//
//	client protocol --ToOpenAI--> OpenAI pivot --TransformRequest--> upstream format
//	upstream format --TransformResponse--> OpenAI pivot --FromOpenAI--> client protocol
//
// Adding a new client protocol = one SegmentConverter (two functions + a stream
// wrapper). Adding a new upstream platform = one ForwardAdapter (unchanged).
package adapters

import (
	"io"
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/usage"
)

// SegmentConverter converts one protocol to/from the internal OpenAI
// chat/completions pivot format for the downstream (client-side) contract.
type SegmentConverter interface {
	// Name is the protocol label, e.g. "openai" or "anthropic".
	Name() string
	// ToOpenAI maps a client protocol request body to the OpenAI pivot.
	// openAIPath is the client path ("chat/completions", "messages", …). It
	// returns the pivot path to hand to the upstream adapter plus the pivot
	// body.
	ToOpenAI(openAIPath string, body []byte) (string, []byte, error)
	// FromOpenAI maps an OpenAI pivot response body back to the client protocol.
	FromOpenAI(openAIPath string, body []byte) ([]byte, error)
	// PivotPath maps a client path to the pivot path the upstream adapter
	// understands ("messages" → "chat/completions" for Anthropic clients).
	PivotPath(openAIPath string) string
	// WrapOpenAIStream wraps an OpenAI SSE stream (as produced by an upstream
	// adapter) into the client protocol's stream. Returning nil means the
	// OpenAI stream is already the client contract (identity).
	WrapOpenAIStream(source io.ReadCloser) io.ReadCloser
}

// OpenAISegment is the identity pivot segment: the client already speaks the
// OpenAI contract.
type OpenAISegment struct{}

func (OpenAISegment) Name() string { return "openai" }

func (OpenAISegment) ToOpenAI(openAIPath string, body []byte) (string, []byte, error) {
	return openAIPath, body, nil
}

func (OpenAISegment) FromOpenAI(_ string, body []byte) ([]byte, error) {
	return body, nil
}

func (OpenAISegment) PivotPath(openAIPath string) string { return openAIPath }

func (OpenAISegment) WrapOpenAIStream(source io.ReadCloser) io.ReadCloser {
	return nil
}

// AnthropicDownstreamSegment serves native Anthropic Messages clients
// (/v1/messages) through any upstream: requests pivot to OpenAI chat, responses
// and streams pivot back to the Messages shape.
type AnthropicDownstreamSegment struct{}

func (AnthropicDownstreamSegment) Name() string { return "anthropic" }

func (AnthropicDownstreamSegment) ToOpenAI(openAIPath string, body []byte) (string, []byte, error) {
	converted, err := MessagesToOpenAIChat(body)
	if err != nil {
		return "", nil, err
	}
	return "chat/completions", converted, nil
}

func (AnthropicDownstreamSegment) FromOpenAI(_ string, body []byte) ([]byte, error) {
	return OpenAIChatToMessages(body)
}

func (AnthropicDownstreamSegment) PivotPath(openAIPath string) string {
	if openAIPath == "messages" {
		return "chat/completions"
	}
	return openAIPath
}

func (AnthropicDownstreamSegment) WrapOpenAIStream(source io.ReadCloser) io.ReadCloser {
	return NewOpenAIStreamToAnthropicStream(source)
}

// ComposeForwardAdapter serves a downstream protocol (From) through an
// upstream platform adapter (Upstream) via the OpenAI pivot. The upstream
// adapter keeps its own URL building, auth headers, and stream reshaping; only
// the request/response/stream bodies pass through the pivot.
//
// OnOpenAI, when set, hooks the OpenAI pivot body between From.ToOpenAI and
// Upstream.TransformRequest (used by the proxy for system-prompt injection on
// translated requests).
type ComposeForwardAdapter struct {
	From     SegmentConverter
	Upstream ForwardAdapter
	// OnOpenAI optionally rewrites the pivot body before the upstream
	// transform (system prompt injection, etc.).
	OnOpenAI func(body []byte) ([]byte, error)
}

var _ ForwardAdapter = (*ComposeForwardAdapter)(nil)

func (c *ComposeForwardAdapter) Name() string {
	return "composed:" + c.From.Name() + "->" + c.Upstream.Name()
}

// IsFor reports the upstream adapter's matching rule: composition is explicit,
// never auto-registered.
func (c *ComposeForwardAdapter) IsFor(typeHint, platform string) bool {
	return c.Upstream.IsFor(typeHint, platform)
}

func (c *ComposeForwardAdapter) BuildUpstreamURL(baseURL, upstreamPath string) (string, error) {
	return c.Upstream.BuildUpstreamURL(baseURL, upstreamPath)
}

func (c *ComposeForwardAdapter) AuthHeaders(apiKey string) http.Header {
	return c.Upstream.AuthHeaders(apiKey)
}

func (c *ComposeForwardAdapter) ExtractUsage(openAIPath string, body []byte) (usage.Tokens, bool) {
	return c.Upstream.ExtractUsage(c.From.PivotPath(openAIPath), body)
}

func (c *ComposeForwardAdapter) TransformRequest(openAIPath string, body []byte) (string, []byte, error) {
	pivotPath, pivotBody, err := c.From.ToOpenAI(openAIPath, body)
	if err != nil {
		return "", nil, err
	}
	if c.OnOpenAI != nil {
		pivotBody, err = c.OnOpenAI(pivotBody)
		if err != nil {
			return "", nil, err
		}
	}
	return c.Upstream.TransformRequest(pivotPath, pivotBody)
}

func (c *ComposeForwardAdapter) TransformResponse(openAIPath string, body []byte) ([]byte, error) {
	pivotBody, err := c.Upstream.TransformResponse(c.From.PivotPath(openAIPath), body)
	if err != nil {
		return nil, err
	}
	return c.From.FromOpenAI(openAIPath, pivotBody)
}

// WrapStream first reshapes the upstream stream into OpenAI SSE (upstream
// adapter), then wraps that into the downstream protocol's stream. The pivot
// path is always "chat/completions": composition serves the chat contract only;
// other paths fail in the upstream adapter with ErrUnsupportedPath.
func (c *ComposeForwardAdapter) WrapStream(openAIPath string, source io.ReadCloser) (io.ReadCloser, error) {
	openaiStream, err := c.Upstream.WrapStream(c.From.PivotPath(openAIPath), source)
	if err != nil {
		return nil, err
	}
	if wrapped := c.From.WrapOpenAIStream(openaiStream); wrapped != nil {
		return wrapped, nil
	}
	return openaiStream, nil
}

// ComposeDownstream returns an adapter that serves the given downstream
// protocol through the upstream adapter. OpenAI clients get the upstream
// adapter unchanged; Anthropic clients get a composed adapter (unless the
// upstream is Anthropic-native, whose "messages" path is verbatim passthrough).
func ComposeDownstream(upstream ForwardAdapter, downstreamProtocol string) ForwardAdapter {
	if !strings.EqualFold(downstreamProtocol, "anthropic") || upstream.Name() == "anthropic" {
		return upstream
	}
	return &ComposeForwardAdapter{
		From:     AnthropicDownstreamSegment{},
		Upstream: upstream,
	}
}
