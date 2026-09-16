package modelcatalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// modelsDevModel mirrors one entry of models.dev's api.json. The document is
// keyed provider -> models -> model id, and carries curated modality, limit and
// cost data rather than a request shape.
type modelsDevModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Attachment  bool   `json:"attachment"`
	Reasoning   bool   `json:"reasoning"`
	ToolCall    bool   `json:"tool_call"`
	Temperature bool   `json:"temperature"`
	OpenWeights bool   `json:"open_weights"`
	Structured  bool   `json:"structured_output"`
	Modalities  struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context flexInt `json:"context"`
		Output  flexInt `json:"output"`
	} `json:"limit"`
	Cost struct {
		Input      flexFloat `json:"input"`
		Output     flexFloat `json:"output"`
		CacheRead  flexFloat `json:"cache_read"`
		CacheWrite flexFloat `json:"cache_write"`
	} `json:"cost"`
}

// modelsDevProvider is one provider block. Its models stay raw so a single
// malformed entry cannot cost us the rest of the document.
type modelsDevProvider struct {
	ID     string                     `json:"id"`
	Name   string                     `json:"name"`
	Models map[string]json.RawMessage `json:"models"`
}

// ParseModelsDev decodes models.dev's api.json.
//
// models.dev does not state a request shape, so the kind comes from the built-in
// classifier and is then corrected by the curated output modalities — that is
// what catches image and video models whose names carry no hint. Providers
// without models are skipped, as are entries with no usable identifier.
func ParseModelsDev(raw []byte) (ParseResult, error) {
	var document map[string]modelsDevProvider
	if err := json.Unmarshal(raw, &document); err != nil {
		return ParseResult{}, fmt.Errorf("models.dev catalog: %w", err)
	}
	providerIDs := make([]string, 0, len(document))
	for providerID := range document {
		providerIDs = append(providerIDs, providerID)
	}
	// Providers are visited in a stable order. models.dev lists one model under
	// every provider that resells it — a popular id appears dozens of times — so
	// map order would decide which one names the model, and two identical syncs
	// would report different changes.
	sort.Strings(providerIDs)

	result := ParseResult{Entries: make([]Entry, 0, 1024)}
	for _, providerID := range providerIDs {
		provider := document[providerID]
		for key, payload := range provider.Models {
			var model modelsDevModel
			if err := json.Unmarshal(payload, &model); err != nil {
				result.addSkip("%s/%s: %v", providerID, key, err)
				continue
			}
			modelID := strings.TrimSpace(model.ID)
			if modelID == "" {
				modelID = strings.TrimSpace(key)
			}
			if modelID == "" {
				continue
			}
			entry := NewEntry(modelID, SourceModelsDev)
			// models.dev carries no request shape, so HasShape stays false and
			// LiteLLM's mode-derived shape wins when both sources know the
			// model. The kind refinement below still applies when it is the only
			// source, which is the image/video case LiteLLM often misses.
			entry.Kind = refineKind(entry.Kind, model.Modalities.Input, model.Modalities.Output)
			entry.Endpoints = endpointsForKind(entry.Kind)
			entry.InputFormats = formatsForEndpoints(entry.Endpoints)
			// models.dev's block id is a *reseller*, not a vendor: a single
			// model is listed by all 25 providers that carry it, so trusting
			// the block would file "openai/gpt-oss-20b" under "deepinfra".
			// The name-derived vendor comes from the in-repo classifier, is the
			// same no matter which block we happen to be reading, and matches
			// how every non-catalog row is labelled — so it wins whenever the
			// name says anything at all. The curated id is only a fallback for
			// names that give nothing, where the sorted traversal still keeps
			// the choice deterministic.
			if entry.Provider == "" {
				for _, candidate := range []string{provider.ID, providerID, provider.Name} {
					if trimmed := strings.TrimSpace(candidate); trimmed != "" {
						entry.Provider = strings.ToLower(trimmed)
						break
					}
				}
			}

			inputs := dedupeStrings(model.Modalities.Input)
			outputs := dedupeStrings(model.Modalities.Output)
			if len(inputs) > 0 || len(outputs) > 0 || model.Attachment {
				entry.HasModality = true
				if len(inputs) > 0 {
					entry.InputModal = inputs
				}
				if len(outputs) > 0 {
					entry.OutputMod = outputs
				}
			}
			// An image model that accepts image input takes a reference image;
			// one that does not is text-to-image only.
			switch entry.Kind {
			case domain.CapabilityKindImageEdit:
				entry.MaxInputImages = 1
			case domain.CapabilityKindImageGen:
				entry.MaxInputImages = 0
			default:
				if containsFold(entry.InputModal, "image") || model.Attachment {
					entry.MaxInputImages = atLeastOne(entry.MaxInputImages)
				} else {
					entry.MaxInputImages = 0
				}
			}
			entry.SupportsTools = model.ToolCall
			entry.SupportsJSONMode = model.Structured
			// models.dev lists streaming as a modality-level property rather
			// than a flag; chat and image models all stream on this gateway.
			entry.SupportsStream = entry.Kind == domain.CapabilityKindChat ||
				entry.Kind == domain.CapabilityKindImageGen ||
				entry.Kind == domain.CapabilityKindImageEdit
			// Its reasoning flag is curated per model, so presence is an answer.
			entry.HasThinking = true
			entry.SupportsThinking = boolToThinking(model.Reasoning)

			entry.ContextWindow = int64(model.Limit.Context)
			entry.MaxOutputTokens = int64(model.Limit.Output)
			entry.HasContext = entry.ContextWindow > 0
			entry.HasMaxOutput = entry.MaxOutputTokens > 0

			// models.dev costs are USD per million tokens.
			entry.PricePromptPer1k = usdPerMillionToPer1k(float64(model.Cost.Input))
			entry.PriceCompletionPer1k = usdPerMillionToPer1k(float64(model.Cost.Output))
			entry.PriceCachePer1k = usdPerMillionToPer1k(float64(model.Cost.CacheRead))
			entry.HasPrice = entry.PricePromptPer1k > 0 || entry.PriceCompletionPer1k > 0

			result.Entries = append(result.Entries, entry)
		}
	}
	result.report()
	return result, nil
}

// refineKind corrects a classifier guess using the curated modalities. Modality
// data is stronger evidence than a name, so it wins whenever it is specific
// enough to decide; otherwise the classifier's guess stands.
func refineKind(kind string, inputs, outputs []string) string {
	switch {
	case containsFold(outputs, "video"):
		return domain.CapabilityKindVideo
	case containsFold(outputs, "embedding"):
		return domain.CapabilityKindEmbedding
	case containsFold(outputs, "image"):
		// An image-output model still needs the name to tell generation from
		// editing, so an existing edit classification is preserved.
		if kind == domain.CapabilityKindImageEdit {
			return domain.CapabilityKindImageEdit
		}
		return domain.CapabilityKindImageGen
	case containsFold(outputs, "audio"), containsFold(outputs, "speech"):
		return domain.CapabilityKindAudioTTS
	case kind == domain.CapabilityKindChat && audioOnlyInput(inputs):
		// Audio in, text out — and no text input at all. That last part matters:
		// a multimodal chat model also accepts audio, and reclassifying it as a
		// transcription endpoint would send its traffic to a route that cannot
		// answer.
		return domain.CapabilityKindAudioSTT
	default:
		return kind
	}
}

// audioOnlyInput reports whether audio is the only thing a model reads, which is
// what distinguishes a transcription endpoint from a multimodal chat model.
func audioOnlyInput(inputs []string) bool {
	if !containsFold(inputs, "audio") {
		return false
	}
	return !containsFold(inputs, "text")
}

// containsFold reports whether the list holds the value, ignoring case.
func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

// atLeastOne raises a count to one without lowering it.
func atLeastOne(value int) int {
	if value < 1 {
		return 1
	}
	return value
}
