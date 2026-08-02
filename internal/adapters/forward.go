// Forward adapters: per-platform request/response transformation for the
// relay path. OpenAI-compatible channels use the default passthrough adapter;
// native platforms (Anthropic, Gemini, …) translate between the OpenAI wire
// contract and their own formats.
package adapters

import (
	"errors"
	"io"
	"net/http"
	"strings"
)

// ErrUnsupportedPath is returned by TransformRequest when the adapter has no
// mapping for the requested OpenAI path. The proxy treats it as a hard
// (non-retryable) failure.
var ErrUnsupportedPath = errors.New("adapter: path not supported")

// ForwardAdapter transforms a request/response between the OpenAI wire
// contract and a native upstream platform.
type ForwardAdapter interface {
	// Name is the adapter key used in the registry (e.g. "gemini").
	Name() string
	// IsFor reports whether this adapter handles the channel, given the
	// channel type hint and site platform (same precedence as Resolve).
	IsFor(typeHint, platform string) bool
	// BuildUpstreamURL joins a base URL with the upstream path produced by
	// TransformRequest.
	BuildUpstreamURL(baseURL, upstreamPath string) (string, error)
	// TransformRequest maps an OpenAI request body to the upstream format.
	// openAIPath is the OpenAI-style path ("chat/completions", "embeddings",
	// "messages", …). It returns the upstream path to forward to plus the
	// converted body. Returning ErrUnsupportedPath means no mapping exists.
	TransformRequest(openAIPath string, body []byte) (upstreamPath string, out []byte, err error)
	// TransformResponse maps an upstream 2xx body back to OpenAI format.
	TransformResponse(openAIPath string, body []byte) ([]byte, error)
	// WrapStream wraps an upstream streaming body (SSE or native stream) so it
	// reads as OpenAI SSE chunks.
	WrapStream(openAIPath string, body io.ReadCloser) (io.ReadCloser, error)
	// AuthHeaders returns the upstream authentication headers for an API key.
	AuthHeaders(apiKey string) http.Header
}

// OpenAIPassthroughAdapter is the default adapter: the channel already speaks
// OpenAI /v1, so nothing is transformed.
type OpenAIPassthroughAdapter struct{}

func (OpenAIPassthroughAdapter) Name() string { return "openai-compatible" }

func (OpenAIPassthroughAdapter) IsFor(_ string, _ string) bool { return true }

func (OpenAIPassthroughAdapter) BuildUpstreamURL(baseURL, upstreamPath string) (string, error) {
	path := strings.TrimSpace(upstreamPath)
	if path == "" {
		path = "chat/completions"
	}
	return JoinOpenAIPath(baseURL, path)
}

func (OpenAIPassthroughAdapter) TransformRequest(openAIPath string, body []byte) (string, []byte, error) {
	path := strings.TrimSpace(openAIPath)
	if path == "" {
		path = "chat/completions"
	}
	return path, body, nil
}

func (OpenAIPassthroughAdapter) TransformResponse(_ string, body []byte) ([]byte, error) {
	return body, nil
}

func (OpenAIPassthroughAdapter) WrapStream(_ string, body io.ReadCloser) (io.ReadCloser, error) {
	return body, nil
}

func (OpenAIPassthroughAdapter) AuthHeaders(apiKey string) http.Header {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+apiKey)
	return headers
}
