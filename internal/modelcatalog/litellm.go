package modelcatalog

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// litellmEntry mirrors LiteLLM's model_prices_and_context_window.json, whose
// values are keyed by model id and mix chat, image, audio and embedding shapes
// into one flat map. Only the fields the gateway can use are decoded.
type litellmEntry struct {
	MaxTokens        flexInt   `json:"max_tokens"`
	MaxInputTokens   flexInt   `json:"max_input_tokens"`
	MaxOutputTokens  flexInt   `json:"max_output_tokens"`
	Mode             string    `json:"mode"`
	LitellmProvider  string    `json:"litellm_provider"`
	InputCostPerTok  flexFloat `json:"input_cost_per_token"`
	OutputCostPerTok flexFloat `json:"output_cost_per_token"`
	CacheReadCost    flexFloat `json:"cache_read_input_token_cost"`

	// The support flags are pointers because LiteLLM only writes them for
	// models it has checked: an absent key means "not stated", which must not
	// be flattened into "not supported".
	SupportsFunctionCalling *bool `json:"supports_function_calling"`
	SupportsNativeStreaming *bool `json:"supports_native_streaming"`
	SupportsReasoning       *bool `json:"supports_reasoning"`
	SupportsVision          *bool `json:"supports_vision"`
	SupportsPDFInput        *bool `json:"supports_pdf_input"`
	SupportsResponseSchema  *bool `json:"supports_response_schema"`

	SupportedEndpoints        []string `json:"supported_endpoints"`
	SupportedModalities       []string `json:"supported_modalities"`
	SupportedOutputModalities []string `json:"supported_output_modalities"`
}

// ParseLiteLLM decodes the LiteLLM price list into normalized entries. Entries
// the gateway has no vocabulary for (realtime sockets, vector stores, search)
// are dropped rather than mislabelled as chat models, and a row that fails to
// decode is reported instead of aborting the other four thousand.
func ParseLiteLLM(raw []byte) (ParseResult, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return ParseResult{}, fmt.Errorf("litellm catalog: %w", err)
	}
	result := ParseResult{Entries: make([]Entry, 0, len(document))}
	for name, payload := range document {
		model := strings.TrimSpace(name)
		if model == "" || isPseudoEntry(model) {
			continue
		}
		var item litellmEntry
		if err := json.Unmarshal(payload, &item); err != nil {
			result.addSkip("%s: %v", model, err)
			continue
		}
		// A mode the gateway has no endpoint vocabulary for is not an error —
		// it is simply out of scope.
		kind := kindFromMode(item.Mode)
		if kind == "" {
			continue
		}
		entry := NewEntry(model, SourceLiteLLM)
		// LiteLLM is the shape authority: its `mode` states which endpoint the
		// model answers on, which a name or a modality list cannot.
		entry.HasShape = true
		entry.Kind = kind
		if item.LitellmProvider != "" {
			entry.Provider = strings.TrimSpace(item.LitellmProvider)
		}
		if endpoints := dedupeStrings(item.SupportedEndpoints); len(endpoints) > 0 {
			entry.Endpoints = endpoints
		} else {
			entry.Endpoints = endpointsForKind(kind)
		}
		entry.InputFormats = formatsForEndpoints(entry.Endpoints)

		// The mode is more reliable than the vision flag for image models: a
		// generation model takes no reference image, an edit model takes one.
		switch kind {
		case domain.CapabilityKindImageEdit:
			entry.MaxInputImages = 1
		case domain.CapabilityKindImageGen, domain.CapabilityKindAudioSTT:
			entry.MaxInputImages = 0
		default:
			if boolValue(item.SupportsVision) || boolValue(item.SupportsPDFInput) {
				entry.MaxInputImages = 1
			} else {
				entry.MaxInputImages = 0
			}
		}

		if item.SupportsNativeStreaming != nil {
			entry.SupportsStream = *item.SupportsNativeStreaming
		}
		entry.SupportsTools = boolValue(item.SupportsFunctionCalling)
		entry.SupportsJSONMode = boolValue(item.SupportsResponseSchema)

		maxTokens := int64(item.MaxTokens)
		entry.ContextWindow = firstPositive(int64(item.MaxInputTokens), maxTokens)
		entry.MaxOutputTokens = firstPositive(int64(item.MaxOutputTokens), maxTokens)
		entry.HasContext = entry.ContextWindow > 0
		entry.HasMaxOutput = entry.MaxOutputTokens > 0

		modalities := dedupeStrings(item.SupportedModalities)
		outputs := dedupeStrings(item.SupportedOutputModalities)
		if len(modalities) > 0 || len(outputs) > 0 || item.SupportsVision != nil || item.SupportsPDFInput != nil {
			entry.HasModality = true
			if len(modalities) == 0 {
				modalities = []string{"text"}
			}
			// An explicit flag is evidence in its own right, so it adds to the
			// list rather than only standing in for an empty one. LiteLLM's
			// supported_modalities is coarse and routinely omits pdf on rows it
			// separately marks supports_pdf_input; letting the list veto the
			// flag would throw away a fact the other catalog curates.
			if boolValue(item.SupportsVision) {
				modalities = append(modalities, "image")
			}
			if boolValue(item.SupportsPDFInput) {
				modalities = append(modalities, "pdf")
			}
			entry.InputModal = dedupeStrings(modalities)
			if len(outputs) > 0 {
				entry.OutputMod = outputs
			}
		}
		// LiteLLM states reasoning support for chat models only; for everything
		// else the field stays unknown rather than being asserted as "no".
		if kind == domain.CapabilityKindChat && item.SupportsReasoning != nil {
			entry.HasThinking = true
			entry.SupportsThinking = boolToThinking(*item.SupportsReasoning)
		}

		// LiteLLM prices are USD per token, including the cache read price.
		entry.PricePromptPer1k = usdPerTokenToPer1k(float64(item.InputCostPerTok))
		entry.PriceCompletionPer1k = usdPerTokenToPer1k(float64(item.OutputCostPerTok))
		entry.PriceCachePer1k = usdPerTokenToPer1k(float64(item.CacheReadCost))
		entry.HasPrice = entry.PricePromptPer1k > 0 || entry.PriceCompletionPer1k > 0

		result.Entries = append(result.Entries, entry)
	}
	result.report()
	return result, nil
}

// isPseudoEntry reports whether a LiteLLM key is the schema example rather than
// a real model id. "sample_spec" carries mode=chat and prose in its numeric
// fields, so it would otherwise be imported as a callable model.
func isPseudoEntry(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "sample_spec")
}

// boolValue dereferences a tri-state flag, treating "not stated" as false. Only
// use it where a false default is harmless; presence matters for merge order,
// so the pointers are checked directly where that distinction is load-bearing.
func boolValue(flag *bool) bool {
	return flag != nil && *flag
}
