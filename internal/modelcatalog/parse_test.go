package modelcatalog

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// litellmFixture is a realistic slice of the price list. "sample_spec" is the
// schema example, whose numeric fields hold prose — it is filtered by name,
// which is the only reliable test, because the prose itself is unparseable.
// Rows that fail to decode live in TestParseToleratesEveryEntryIndependently,
// so this fixture stays usable by the service-level tests that count problems.
const litellmFixture = `{
  "sample_spec": {
    "max_input_tokens": "max input tokens, if the provider specifies it",
    "max_output_tokens": "max output tokens, if the provider specifies it",
    "mode": "chat",
    "input_cost_per_token": "cost per input token"
  },
  "stringly-typed": {
    "max_input_tokens": "32000",
    "max_output_tokens": "8000",
    "mode": "chat",
    "litellm_provider": "openai",
    "input_cost_per_token": "1e-06"
  },
  "gpt-5": {
    "max_input_tokens": 272000,
    "max_output_tokens": 128000,
    "max_tokens": 128000,
    "mode": "chat",
    "litellm_provider": "openai",
    "input_cost_per_token": 1.25e-06,
    "output_cost_per_token": 1e-05,
    "cache_read_input_token_cost": 1.25e-07,
    "supports_function_calling": true,
    "supports_native_streaming": true,
    "supports_reasoning": true,
    "supports_vision": true,
    "supports_pdf_input": true,
    "supports_response_schema": true,
    "supported_endpoints": ["/v1/chat/completions", "/v1/batch"],
    "supported_modalities": ["text", "image"],
    "supported_output_modalities": ["text"]
  },
  "gpt-image-1": {
    "max_input_tokens": 32000,
    "mode": "image_generation",
    "litellm_provider": "openai",
    "input_cost_per_token": 5e-06,
    "output_cost_per_token": 4e-05,
    "supports_native_streaming": false
  },
  "grok-imagine-image-edit": {
    "mode": "image_edit",
    "litellm_provider": "xai",
    "input_cost_per_token": 0,
    "output_cost_per_token": 0
  },
  "text-embedding-3-large": {
    "max_input_tokens": 8191,
    "mode": "embedding",
    "litellm_provider": "openai",
    "input_cost_per_token": 1.3e-07,
    "output_cost_per_token": 0
  },
  "gpt-realtime": {"mode": "realtime", "litellm_provider": "openai"},
  "some-vector-store": {"mode": "vector_store", "litellm_provider": "openai"}
}`

func TestParseLiteLLMNormalizesKindsEndpointsAndPrices(t *testing.T) {
	result, err := ParseLiteLLM([]byte(litellmFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	entries := result.Entries
	byModel := map[string]Entry{}
	for _, entry := range entries {
		byModel[entry.Model] = entry
	}
	// Modes the gateway has no vocabulary for are dropped, "sample_spec"
	// included — otherwise the registry fills up with non-models.
	if _, ok := byModel["gpt-realtime"]; ok {
		t.Error("realtime entry should be skipped")
	}
	if _, ok := byModel["some-vector-store"]; ok {
		t.Error("vector store entry should be skipped")
	}
	if _, ok := byModel["sample_spec"]; ok {
		t.Error("sample_spec should be skipped")
	}
	// Numbers written as strings are still numbers.
	if loosely := byModel["stringly-typed"]; loosely.ContextWindow != 32000 || loosely.MaxOutputTokens != 8000 {
		t.Errorf("stringly-typed limits = %d/%d", loosely.ContextWindow, loosely.MaxOutputTokens)
	}
	if got := byModel["stringly-typed"].PricePromptPer1k; got != 0.001 {
		t.Errorf("stringly-typed prompt price = %v", got)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("a clean row must not be reported as skipped: %v", result.Skipped)
	}

	chat := byModel["gpt-5"]
	if chat.Kind != domain.CapabilityKindChat {
		t.Errorf("chat kind = %q", chat.Kind)
	}
	if chat.ContextWindow != 272000 || chat.MaxOutputTokens != 128000 {
		t.Errorf("chat limits = %d/%d", chat.ContextWindow, chat.MaxOutputTokens)
	}
	if !chat.SupportsTools || !chat.SupportsJSONMode || !chat.SupportsStream {
		t.Errorf("chat flags = tools:%v json:%v stream:%v", chat.SupportsTools, chat.SupportsJSONMode, chat.SupportsStream)
	}
	if chat.SupportsThinking != 1 {
		t.Errorf("chat thinking = %d", chat.SupportsThinking)
	}
	// The coarse modality list must not veto the explicit pdf flag: models.dev
	// curates that fact and a sync would otherwise narrow the row.
	if got := domain.JoinCSV(chat.InputModal); got != "text,image,pdf" {
		t.Errorf("chat input modalities = %q", got)
	}
	// Per-token 1.25e-06 USD becomes 0.00125 per 1k.
	if chat.PricePromptPer1k != 0.00125 || chat.PriceCompletionPer1k != 0.01 {
		t.Errorf("chat prices = %v/%v", chat.PricePromptPer1k, chat.PriceCompletionPer1k)
	}
	if chat.PriceCachePer1k != 0.000125 {
		t.Errorf("chat cache price = %v", chat.PriceCachePer1k)
	}

	image := byModel["gpt-image-1"]
	if image.Kind != domain.CapabilityKindImageGen {
		t.Errorf("image kind = %q", image.Kind)
	}
	// A generation model takes no reference image; an edit model takes one.
	if image.MaxInputImages != 0 {
		t.Errorf("image max_input_images = %d", image.MaxInputImages)
	}
	if got := domain.JoinCSV(image.Endpoints); got != "/v1/images/generations" {
		t.Errorf("image endpoints = %q", got)
	}
	if got := domain.JoinCSV(image.InputFormats); got != "json,multipart" {
		t.Errorf("image input formats = %q", got)
	}

	edit := byModel["grok-imagine-image-edit"]
	if edit.Kind != domain.CapabilityKindImageEdit || edit.MaxInputImages != 1 {
		t.Errorf("edit = kind:%q images:%d", edit.Kind, edit.MaxInputImages)
	}
	// Free models carry no price group, so a sync will not write a zero.
	if edit.HasPrice {
		t.Error("edit entry should report no price")
	}
	// A non-chat kind must not assert thinking either way.
	if edit.SupportsThinking != -1 {
		t.Errorf("edit thinking = %d", edit.SupportsThinking)
	}

	embedding := byModel["text-embedding-3-large"]
	if embedding.Kind != domain.CapabilityKindEmbedding {
		t.Errorf("embedding kind = %q", embedding.Kind)
	}
	if got := domain.JoinCSV(embedding.Endpoints); got != "/v1/embeddings" {
		t.Errorf("embedding endpoints = %q", got)
	}
	if embedding.SupportsStream {
		t.Error("embedding should not stream")
	}
}

const modelsDevFixture = `{
  "anthropic": {
    "id": "anthropic",
    "name": "Anthropic",
    "models": {
      "claude-sonnet-4-5": {
        "id": "claude-sonnet-4-5",
        "name": "Claude Sonnet 4.5",
        "attachment": true,
        "reasoning": true,
        "tool_call": true,
        "structured_output": true,
        "modalities": {"input": ["text", "image", "pdf"], "output": ["text"]},
        "limit": {"context": 1000000, "output": 64000},
        "cost": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75}
      }
    }
  },
  "black-forest-labs": {
    "id": "black-forest-labs",
    "name": "Black Forest Labs",
    "models": {
      "flux-pro-1.1": {
        "id": "flux-pro-1.1",
        "modalities": {"input": ["text"], "output": ["image"]},
        "limit": {"context": 1000, "output": 1},
        "cost": {"input": 0, "output": 40}
      }
    }
  },
  "openai": {
    "id": "openai",
    "name": "OpenAI",
    "models": {
      "whisper-1": {
        "id": "whisper-1",
        "modalities": {"input": ["audio"], "output": ["text"]},
        "limit": {"context": 0, "output": 0},
        "cost": {"input": 0.006, "output": 0}
      },
      "mimo-v2.5": {
        "id": "mimo-v2.5",
        "reasoning": true,
        "modalities": {"input": ["text", "image", "audio", "video"], "output": ["text"]},
        "limit": {"context": 128000, "output": 8192},
        "cost": {"input": 0.2, "output": 0.6}
      }
    }
  }
}`

func TestParseModelsDevRefinesKindFromModalities(t *testing.T) {
	result, err := ParseModelsDev([]byte(modelsDevFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	byModel := map[string]Entry{}
	for _, entry := range result.Entries {
		byModel[entry.Model] = entry
	}
	if len(byModel) != 4 {
		t.Fatalf("models = %d, want 4", len(byModel))
	}

	chat := byModel["claude-sonnet-4-5"]
	if chat.Kind != domain.CapabilityKindChat {
		t.Errorf("chat kind = %q", chat.Kind)
	}
	if chat.ContextWindow != 1000000 || chat.MaxOutputTokens != 64000 {
		t.Errorf("chat limits = %d/%d", chat.ContextWindow, chat.MaxOutputTokens)
	}
	// Dollars per million becomes per-1k: 3 -> 0.003, cache 0.3 -> 0.0003.
	if chat.PricePromptPer1k != 0.003 || chat.PriceCompletionPer1k != 0.015 {
		t.Errorf("chat prices = %v/%v", chat.PricePromptPer1k, chat.PriceCompletionPer1k)
	}
	if chat.PriceCachePer1k != 0.0003 {
		t.Errorf("chat cache price = %v", chat.PriceCachePer1k)
	}
	if chat.SupportsThinking != 1 || !chat.SupportsTools {
		t.Errorf("chat flags = thinking:%d tools:%v", chat.SupportsThinking, chat.SupportsTools)
	}
	if chat.MaxInputImages != 1 {
		t.Errorf("attachment model should accept a reference image, got %d", chat.MaxInputImages)
	}

	// The name says nothing about images; the curated modalities do.
	image := byModel["flux-pro-1.1"]
	if image.Kind != domain.CapabilityKindImageGen {
		t.Errorf("flux kind = %q", image.Kind)
	}
	if image.MaxInputImages != 0 {
		t.Errorf("text-to-image model should take no reference image, got %d", image.MaxInputImages)
	}
	// The block id is a reseller, so it must not displace the vendor the name
	// implies. Here it also keeps one spelling per vendor: the classifier
	// already labels flux rows "blackforestlabs", and taking the catalog's
	// "black-forest-labs" would leave the registry with two names for one
	// company.
	if image.Provider != "blackforestlabs" {
		t.Errorf("flux provider = %q, want the name-derived vendor", image.Provider)
	}

	// Audio in, text out is a transcription endpoint, not a conversation.
	whisper := byModel["whisper-1"]
	if whisper.Kind != domain.CapabilityKindAudioSTT {
		t.Errorf("whisper kind = %q", whisper.Kind)
	}
	if got := domain.JoinCSV(whisper.InputFormats); got != "multipart" {
		t.Errorf("whisper input formats = %q", got)
	}

	// A chat model that merely *also* accepts audio stays a chat model.
	// Reclassifying it as transcription would route its traffic to an endpoint
	// that cannot answer — this is the case that shipped broken once.
	mimo := byModel["mimo-v2.5"]
	if mimo.Kind != domain.CapabilityKindChat {
		t.Errorf("mimo kind = %q, want chat", mimo.Kind)
	}
	if got := domain.JoinCSV(mimo.Endpoints); got != "/v1/chat/completions" {
		t.Errorf("mimo endpoints = %q", got)
	}
	if mimo.SupportsThinking != 1 {
		t.Errorf("mimo thinking = %d", mimo.SupportsThinking)
	}
}

func TestMergeKeepsLiteLLMShapeAndFillsGapsFromModelsDev(t *testing.T) {
	litellm, err := ParseLiteLLM([]byte(`{"gpt-5": {"mode": "chat", "max_input_tokens": 272000, "input_cost_per_token": 1.25e-06, "supports_function_calling": true}}`))
	if err != nil {
		t.Fatalf("litellm: %v", err)
	}
	modelsdev, err := ParseModelsDev([]byte(`{"openai": {"models": {"gpt-5": {"id": "gpt-5", "reasoning": true, "modalities": {"input": ["text", "image"], "output": ["text"]}, "limit": {"context": 400000, "output": 128000}, "cost": {"input": 1.25, "output": 10}}}}}`))
	if err != nil {
		t.Fatalf("models.dev: %v", err)
	}

	merged := Merge(litellm.Entries[0], modelsdev.Entries[0])
	if got := domain.JoinCSV(merged.Sources); got != "litellm,models.dev" {
		t.Errorf("sources = %q", got)
	}
	// LiteLLM spoke first, so its request shape and limits survive.
	if !merged.SupportsTools {
		t.Error("tool support from litellm should survive")
	}
	if merged.ContextWindow != 272000 {
		t.Errorf("context window = %d, want the litellm value", merged.ContextWindow)
	}
	if merged.PricePromptPer1k != 0.00125 {
		t.Errorf("prompt price = %v, want the litellm value", merged.PricePromptPer1k)
	}
	// models.dev fills what LiteLLM left open: the output limit and the
	// reasoning flag.
	if merged.MaxOutputTokens != 128000 {
		t.Errorf("output tokens = %d", merged.MaxOutputTokens)
	}
	if merged.SupportsThinking != 1 {
		t.Errorf("thinking = %d", merged.SupportsThinking)
	}
	// Modalities are asserted by models.dev, so an empty LiteLLM modality set
	// is not allowed to overwrite them with nothing.
	if got := domain.JoinCSV(merged.InputModal); got != "text,image" {
		t.Errorf("input modalities = %q", got)
	}
}

func TestParseToleratesEveryEntryIndependently(t *testing.T) {
	// A row that is unreadable must not cost the readable ones: these indexes
	// are third-party, and one bad row used to abort the whole document.
	result, err := ParseLiteLLM([]byte(`{
	  "good-one": {"mode": "chat", "max_input_tokens": 8000},
	  "bad-one": [],
	  "bad-two": 17,
	  "good-two": {"mode": "embedding"}
	}`))
	if err != nil {
		t.Fatalf("a malformed row must not fail the parse: %v", err)
	}
	models := map[string]bool{}
	for _, entry := range result.Entries {
		models[entry.Model] = true
	}
	if !models["good-one"] || !models["good-two"] {
		t.Errorf("readable rows were lost: %v", models)
	}
	if models["bad-one"] || models["bad-two"] {
		t.Errorf("malformed rows leaked into entries: %v", models)
	}
	if len(result.Skipped) != 2 {
		t.Errorf("skipped = %d, want 2 (%v)", len(result.Skipped), result.Skipped)
	}
	if result.Overflow != 0 {
		t.Errorf("overflow = %d, want 0", result.Overflow)
	}
}

func TestParseBoundsTheSkipReport(t *testing.T) {
	// An index with dozens of broken rows must not bury the operator.
	var document strings.Builder
	document.WriteString(`{"readable": {"mode": "chat"}`)
	for i := 0; i < maxReportedSkips+4; i++ {
		fmt.Fprintf(&document, `,"broken-%02d": []`, i)
	}
	document.WriteString("}")
	result, err := ParseLiteLLM([]byte(document.String()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Errorf("entries = %d, want 1", len(result.Entries))
	}
	if len(result.Skipped) != maxReportedSkips+1 {
		t.Errorf("skipped = %d, want %d plus the count line", len(result.Skipped), maxReportedSkips)
	}
	if result.Overflow != 4 {
		t.Errorf("overflow = %d, want 4", result.Overflow)
	}
}

func TestMergeUnionsModalitiesAcrossSources(t *testing.T) {
	// The two catalogs disagree about detail, not about truth: LiteLLM's coarse
	// list says text+image, models.dev's curation adds pdf. Unioning keeps both;
	// picking a winner silently narrows the row on every sync.
	litellm, err := ParseLiteLLM([]byte(`{"gpt-5": {"mode": "chat", "supported_modalities": ["text", "image"], "supported_output_modalities": ["text"]}}`))
	if err != nil {
		t.Fatalf("litellm: %v", err)
	}
	modelsdev, err := ParseModelsDev([]byte(`{"openai": {"models": {"gpt-5": {"id": "gpt-5", "modalities": {"input": ["text", "image", "pdf"], "output": ["text"]}}}}}`))
	if err != nil {
		t.Fatalf("models.dev: %v", err)
	}
	merged := Merge(litellm.Entries[0], modelsdev.Entries[0])
	if got := domain.JoinCSV(merged.InputModal); got != "text,image,pdf" {
		t.Errorf("input modalities = %q, want the union of both sources", got)
	}
	// Output modalities union too, and must not be duplicated by the merge.
	if got := domain.JoinCSV(merged.OutputMod); got != "text" {
		t.Errorf("output modalities = %q", got)
	}
}

func TestParseModelsDevIsDeterministicAcrossProviders(t *testing.T) {
	// models.dev lists one model under every provider that resells it, and the
	// document is a JSON object — so iteration order used to decide which
	// provider named the model, and identical syncs disagreed. Sorted traversal
	// makes the first provider win every time.
	const document = `{
	  "zulu":   {"id": "zulu",   "models": {"shared": {"id": "shared", "limit": {"context": 100}}}},
	  "alpha":  {"id": "alpha",  "models": {"shared": {"id": "shared", "limit": {"context": 200}}}},
	  "mike":   {"id": "mike",   "models": {"shared": {"id": "shared", "limit": {"context": 300}}}}
	}`
	for attempt := 0; attempt < 20; attempt++ {
		result, err := ParseModelsDev([]byte(document))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(result.Entries) != 3 {
			t.Fatalf("entries = %d, want one per provider", len(result.Entries))
		}
		// The provider visited first wins the merge, so "alpha" must always be
		// the one that reaches the registry.
		order := make([]string, 0, len(result.Entries))
		for _, entry := range result.Entries {
			order = append(order, entry.Provider)
		}
		if order[0] != "alpha" {
			t.Fatalf("first provider = %q, want alpha (sorted)", order[0])
		}
		merged := Merge(result.Entries...)
		if merged.Provider != "alpha" || merged.ContextWindow != 200 {
			t.Fatalf("merged = provider %q context %d, want alpha/200 on every run",
				merged.Provider, merged.ContextWindow)
		}
	}
}

func TestParseModelsDevPrefersTheVendorTheNameImplies(t *testing.T) {
	// The block id is whoever resells the model. "openai/gpt-oss-20b" is
	// OpenAI's model even when the page we are reading belongs to deepinfra, so
	// the name wins; the block id is only there for names that say nothing.
	const document = `{
	  "deepinfra": {"id": "deepinfra", "models": {"openai/gpt-oss-20b": {"id": "openai/gpt-oss-20b"}}},
	  "abacus":    {"id": "abacus",    "models": {"openai/gpt-oss-20b": {"id": "openai/gpt-oss-20b"}}}
	}`
	result, err := ParseModelsDev([]byte(document))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, entry := range result.Entries {
		if entry.Provider != "openai" {
			t.Errorf("provider = %q, want openai (from the model name, not the reseller block)", entry.Provider)
		}
	}

	// A name the classifier knows now beats the reseller block too — this is
	// what stops "mimo-v2.5" from being filed under whichever aggregator
	// happened to be read.
	xiaomi, err := ParseModelsDev([]byte(`{"llmgateway": {"id": "llmgateway", "models": {"mimo-v2.5": {"id": "mimo-v2.5"}}}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if xiaomi.Entries[0].Provider != "xiaomi" {
		t.Errorf("provider = %q, want xiaomi (from the model name)", xiaomi.Entries[0].Provider)
	}

	// A name that carries no vendor still gets a labelled row, and still the
	// same one every time.
	nameless, err := ParseModelsDev([]byte(`{"zulu": {"id": "zulu", "models": {"mystery-model": {"id": "mystery-model"}}}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if nameless.Entries[0].Provider != "zulu" {
		t.Errorf("provider = %q, want the block id as fallback", nameless.Entries[0].Provider)
	}
}

func TestFormatPriceTrimsTrailingZeros(t *testing.T) {
	cases := map[float64]string{
		0:           "0",
		0.00125:     "0.00125",
		0.000000125: "0.0000001",
		3:           "3",
	}
	for value, want := range cases {
		if got := formatPrice(value); got != want {
			t.Errorf("formatPrice(%v) = %q, want %q", value, got, want)
		}
	}
}
