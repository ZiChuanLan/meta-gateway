package adapters

import (
	"io"
	"net/http"

	"github.com/lan/meta-gateway/internal/usage"
)

// AnthropicForwardAdapter translates between the OpenAI wire contract and the
// native Anthropic Messages API. It wraps the existing conversion functions
// (ChatToAnthropicMessages / AnthropicMessagesToChat / AnthropicToOpenAIStream)
// so the relay path can treat Anthropic channels uniformly with other adapters.
type AnthropicForwardAdapter struct{}

func (AnthropicForwardAdapter) Name() string { return "anthropic" }

func (AnthropicForwardAdapter) IsFor(typeHint, platform string) bool {
	return IsAnthropicFamily(typeHint, platform)
}

func (AnthropicForwardAdapter) BuildUpstreamURL(baseURL, upstreamPath string) (string, error) {
	path := upstreamPath
	if path == "" {
		path = "messages"
	}
	return JoinAnthropicPath(baseURL, path)
}

func (AnthropicForwardAdapter) TransformRequest(openAIPath string, body []byte) (string, []byte, error) {
	switch openAIPath {
	case "", "chat/completions":
		translated, err := ChatToAnthropicMessages(body)
		if err != nil {
			return "", nil, err
		}
		return "messages", translated, nil
	case "messages":
		// Native Anthropic Messages clients already send Messages JSON.
		return "messages", body, nil
	default:
		// Images/audio/etc. are OpenAI-compatible only; do not invent
		// Anthropic mappings.
		return "", nil, ErrUnsupportedPath
	}
}

func (AnthropicForwardAdapter) TransformResponse(openAIPath string, body []byte) ([]byte, error) {
	// Native /v1/messages callers expect the Anthropic response contract. The
	// request is already in Messages format, so preserve the upstream body;
	// chat/completions callers still receive the OpenAI pivot response.
	if openAIPath == "messages" {
		return body, nil
	}
	converted, err := AnthropicMessagesToChat(body)
	if err != nil {
		return nil, err
	}
	return converted, nil
}

func (AnthropicForwardAdapter) WrapStream(openAIPath string, body io.ReadCloser) (io.ReadCloser, error) {
	if openAIPath == "messages" {
		return body, nil
	}
	return NewAnthropicToOpenAIStream(body), nil
}

func (AnthropicForwardAdapter) AuthHeaders(apiKey string) http.Header {
	return AnthropicAuthHeaders(apiKey)
}

func (AnthropicForwardAdapter) ExtractUsage(_ string, body []byte) (usage.Tokens, bool) {
	tokens := usage.ExtractFromJSONBody(body)
	return tokens, tokens.Valid()
}

var _ ForwardAdapter = AnthropicForwardAdapter{}
