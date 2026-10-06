package proxy

import (
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
)

type protocolPlan struct {
	adapter        adapters.ForwardAdapter
	translation    *adapters.Translation
	anthropic      bool
	responses      bool
	nativeFallback bool
}

// Protocol selection is independent of retries, billing and request mutation.
// Native Responses wins on OpenAI upstreams; translation is only armed as a
// fallback for a missing native endpoint, or selected for other families.
func (s *Service) planProtocol(adapter adapters.ForwardAdapter, req Request, prompt string) protocolPlan {
	protocol := strings.ToLower(strings.TrimSpace(req.DownstreamProtocol))
	plan := protocolPlan{adapter: adapter, anthropic: protocol == "anthropic", responses: protocol == "responses" && req.OpenAIPath == "responses"}
	family := adapters.CanonicalFamily(adapter.Name())
	if plan.responses && family == "openai" {
		if translation, ok := s.registry.Translations.Lookup("responses", "openai"); ok && translation.Body != nil && translation.Response != nil {
			plan.nativeFallback = true
		}
		return plan
	}
	if !(plan.anthropic && req.OpenAIPath == "messages" && adapter.Name() != "anthropic") && !plan.responses {
		return plan
	}
	if translation, ok := s.registry.Translations.Lookup(protocol, family); ok && translation.Body != nil {
		plan.translation = &translation
		return plan
	}
	plan.adapter = adapters.ComposeDownstream(adapter, protocol)
	if composed, ok := plan.adapter.(*adapters.ComposeForwardAdapter); ok {
		prompt = strings.TrimSpace(prompt)
		composed.OnOpenAI = func(body []byte) ([]byte, error) {
			if prompt != "" {
				return injectSystemPrompt(body, prompt), nil
			}
			return body, nil
		}
	}
	return plan
}
