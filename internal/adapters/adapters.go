// Package adapters contains stateless upstream platform integrations.
package adapters

import (
	"context"
	"net/http"
	"strings"
)

type ModelAdapter interface {
	Name() string
	ListModels(ctx context.Context, baseURL, apiKey string) ([]string, error)
}

type Registry struct {
	adapters        map[string]ModelAdapter
	checkinAdapters map[string]CheckinAdapter
	accountAdapters map[string]AccountAdapter
	forwardAdapters []ForwardAdapter
	// Translations is the N×M protocol translation matrix (from → to pairs
	// with stream/non-stream/count-token modes and the model-rewrite fallback
	// for unregistered pairs).
	Translations *TranslationRegistry
}

func NewRegistry(client *http.Client) *Registry {
	openAI := NewOpenAIModelAdapter("openai-compatible", client)
	newAPI := NewOpenAIModelAdapter("new-api", client)
	oneAPI := NewOpenAIModelAdapter("one-api", client)
	anthropic := NewAnthropicModelAdapter("anthropic", client)
	gemini := NewGeminiModelAdapter("gemini", client)
	newAPICheckin := NewJSONCheckinAdapter("new-api", client, true)
	oneAPICheckin := NewJSONCheckinAdapter("one-api", client, false)
	newAPIAccount := NewNewAPIAccountAdapter("new-api", client, true)
	oneAPIAccount := NewNewAPIAccountAdapter("one-api", client, false)

	// Primary families. Aliases and third-party site brands resolve through FamilyOf.
	modelAdapters := map[string]ModelAdapter{
		"openai":            openAI,
		"openai-compatible": openAI,
		"openaicompat":      openAI,
		"new-api":           newAPI,
		"newapi":            newAPI,
		"one-api":           oneAPI,
		"oneapi":            oneAPI,
		"anthropic":         anthropic,
		"claude":            anthropic,
		"claude-official":   anthropic,
		"gemini":            gemini,
		"google-gemini":     gemini,
	}
	// Register common OpenAI-compatible relay brands so discovery works after AAH import.
	for _, brand := range OpenAICompatibleBrands() {
		modelAdapters[brand] = openAI
	}

	checkinMap := map[string]CheckinAdapter{
		"new-api": newAPICheckin,
		"newapi":  newAPICheckin,
		"one-api": oneAPICheckin,
		"oneapi":  oneAPICheckin,
		// Many New-API forks share the same check-in / account family.
		"anyrouter":   newAPICheckin,
		"done-hub":    newAPICheckin,
		"one-hub":     newAPICheckin,
		"veloera":     newAPICheckin,
		"v-api":       newAPICheckin,
		"voapi":       newAPICheckin,
		"super-api":   newAPICheckin,
		"rix-api":     newAPICheckin,
		"neo-api":     newAPICheckin,
		"sub2api":     newAPICheckin,
		"wong-gongyi": newAPICheckin,
		// AxonHub is NOT a New-API family site: it has its own JWT/OAuth
		// account system (Gin + GraphQL) and no /api/token or /api/user/self,
		// so check-in / account sync must not be offered for it.
		"metapi":          newAPICheckin,
		"aihubmix":        newAPICheckin,
		"sharedchat":      newAPICheckin,
		"octopus":         newAPICheckin,
		"claude-code-hub": newAPICheckin,
	}
	accountMap := map[string]AccountAdapter{
		"new-api": newAPIAccount,
		"newapi":  newAPIAccount,
		"one-api": oneAPIAccount,
		"oneapi":  oneAPIAccount,
	}
	for brand := range checkinMap {
		if brand == "one-api" || brand == "oneapi" {
			accountMap[brand] = oneAPIAccount
			continue
		}
		if _, exists := accountMap[brand]; !exists {
			accountMap[brand] = newAPIAccount
		}
	}
	return &Registry{
		adapters:        modelAdapters,
		checkinAdapters: checkinMap,
		accountAdapters: accountMap,
		// Registration order matters: the first adapter whose IsFor matches wins.
		// OpenAIPassthroughAdapter is appended last as the universal fallback.
		forwardAdapters: []ForwardAdapter{
			AnthropicForwardAdapter{},
			GeminiForwardAdapter{},
			OpenAIPassthroughAdapter{},
		},
		Translations: NewTranslationRegistry(),
	}
}

// OpenAICompatibleBrands are site/channel type labels that speak OpenAI /v1.
func OpenAICompatibleBrands() []string {
	return []string{
		"anyrouter",
		"veloera",
		"one-hub",
		"done-hub",
		"v-api",
		"voapi",
		"super-api",
		"rix-api",
		"neo-api",
		"sub2api",
		"octopus",
		"axonhub",
		"metapi",
		"claude-code-hub",
		"aihubmix",
		"sharedchat",
		"wong-gongyi",
		// Domestic providers (OpenAI-compatible endpoints).
		"deepseek",
		"moonshot",
		"zhipu",
		"qwen",
		"doubao",
		"siliconflow",
		"minimax",
		"stepfun",
		"lingyiwanwu",
		"baichuan",
		"spark",
		"hunyuan",
		"qianfan",
		// International providers (OpenAI-compatible endpoints).
		"openrouter",
		"groq",
		"xai",
		"mistral",
		"perplexity",
		"unknown",
	}
}

func (r *Registry) ResolveAccount(platform string) (AccountAdapter, bool) {
	name := CanonicalType(platform)
	if adapter, ok := r.accountAdapters[name]; ok {
		return adapter, true
	}
	adapter, ok := r.accountAdapters[canonical(platform)]
	return adapter, ok
}

func (r *Registry) ResolveCheckin(platform string) (CheckinAdapter, bool) {
	name := CanonicalType(platform)
	if adapter, ok := r.checkinAdapters[name]; ok {
		return adapter, true
	}
	// Fall back to brand map with original key.
	adapter, ok := r.checkinAdapters[canonical(platform)]
	return adapter, ok
}

// Resolve gives channel type_hint precedence over the site's platform.
// When type_hint is set but unknown, platform is not used as a silent fallback.
func (r *Registry) Resolve(typeHint, platform string) (ModelAdapter, bool) {
	if strings.TrimSpace(typeHint) != "" {
		return r.resolveOne(typeHint)
	}
	return r.resolveOne(platform)
}

func (r *Registry) resolveOne(raw string) (ModelAdapter, bool) {
	key := canonical(raw)
	if adapter, ok := r.adapters[key]; ok {
		return adapter, true
	}
	family := CanonicalType(raw)
	if family != key {
		if adapter, ok := r.adapters[family]; ok {
			return adapter, true
		}
	}
	return nil, false
}

// ResolveForward returns the forwarding adapter for a channel's type hint and
// site platform. The passthrough fallback guarantees a non-nil result.
func (r *Registry) ResolveForward(typeHint, platform string) ForwardAdapter {
	for _, adapter := range r.forwardAdapters {
		if adapter.IsFor(typeHint, platform) {
			return adapter
		}
	}
	return OpenAIPassthroughAdapter{}
}

// CanonicalFamily maps an adapter name to a protocol family id used in the
// translation matrix ("openai-compatible" → "openai", "claude" →
// "anthropic", "google-gemini" → "gemini"). Unknown names stay verbatim.
func CanonicalFamily(name string) string {
	switch CanonicalType(name) {
	case "openai", "openai-compatible", "new-api", "one-api":
		return "openai"
	case "anthropic", "claude":
		return "anthropic"
	case "gemini", "google-gemini":
		return "gemini"
	}
	return CanonicalType(name)
}
func CanonicalType(value string) string {
	value = canonical(value)
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "", "openai", "openaicompat", "openai-compatible", "openai-compat":
		return "openai-compatible"
	case "newapi", "new-api":
		return "new-api"
	case "oneapi", "one-api":
		return "one-api"
	case "anthropic", "claude", "claude-official", "claude-api":
		return "anthropic"
	case "gemini", "google-gemini", "google":
		return "gemini"
	case "anyrouter", "veloera", "one-hub", "done-hub", "v-api", "voapi",
		"super-api", "rix-api", "neo-api", "sub2api", "octopus", "axonhub",
		"metapi", "claude-code-hub", "aihubmix", "sharedchat", "wong-gongyi",
		"deepseek", "moonshot", "zhipu", "qwen", "doubao", "siliconflow",
		"minimax", "stepfun", "lingyiwanwu", "baichuan", "spark", "hunyuan",
		"qianfan", "openrouter", "groq", "xai", "mistral", "perplexity",
		"unknown":
		// Brand-specific New-API / One-API style relays: OpenAI-compatible /v1 surface.
		return "openai-compatible"
	default:
		return value
	}
}

func canonical(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
