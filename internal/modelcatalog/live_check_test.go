package modelcatalog

import (
	"context"
	"os"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// TestLiveCatalogParses checks the parsers against the deployed indexes instead
// of fixtures. Both bugs this package has shipped were invisible to fixtures and
// obvious here, because the documents are third-party and change without notice.
//
// It is skipped unless MODELCATALOG_LIVE=1, so the normal suite stays offline:
//
//	MODELCATALOG_LIVE=1 go test ./internal/modelcatalog/ -run Live -v
func TestLiveCatalogParses(t *testing.T) {
	if os.Getenv("MODELCATALOG_LIVE") != "1" {
		t.Skip("set MODELCATALOG_LIVE=1 to fetch the real catalogs")
	}
	service := New(nil, Options{})
	entries, problems, err := service.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	t.Logf("merged entries: %d", len(entries))
	for _, problem := range problems {
		// A source that reports a floor of zero entries is the failure this
		// test exists to catch, so say so rather than logging in passing.
		t.Errorf("source problem: %s", problem)
	}
	// A per-entry decode failure used to abort the whole document; a document
	// that yields a handful of entries is that bug coming back.
	if len(entries) < 1000 {
		t.Errorf("expected thousands of entries, got %d", len(entries))
	}

	byModel := map[string]Entry{}
	for _, entry := range entries {
		byModel[entry.Model] = entry
	}
	if _, ok := byModel["sample_spec"]; ok {
		t.Error("the schema example leaked into the registry")
	}
	// An audio-capable multimodal chat model must not be mistaken for a
	// transcription endpoint — doing so routes its traffic where it cannot be
	// answered.
	if mimo, ok := byModel["mimo-v2.5"]; ok {
		t.Logf("mimo-v2.5 kind=%s endpoints=%v modalities=%v",
			mimo.Kind, mimo.Endpoints, mimo.InputModal)
		if mimo.Kind != domain.CapabilityKindChat {
			t.Errorf("mimo-v2.5 kind = %q, want chat", mimo.Kind)
		}
	}
	// Spot-check that the two sources actually merge rather than one shadowing
	// the other: these are known to LiteLLM and models.dev both.
	for _, model := range []string{"gpt-5", "gemini-2.5-flash", "claude-sonnet-4-5"} {
		entry, ok := byModel[model]
		if !ok {
			t.Errorf("%s missing from the merged view", model)
			continue
		}
		t.Logf("%s: kind=%s ctx=%d out=%d src=%v modal=%v price=%v/%v",
			model, entry.Kind, entry.ContextWindow, entry.MaxOutputTokens,
			entry.Sources, entry.InputModal, entry.PricePromptPer1k, entry.PriceCompletionPer1k)
		if len(entry.Sources) != 2 {
			t.Errorf("%s sources = %v, want both catalogs", model, entry.Sources)
		}
	}
	// models.dev curates pdf input for this model while LiteLLM's coarse
	// supported_modalities omits it; the union must keep it, because narrowing
	// the column on every sync would be a regression rather than a refresh.
	if terra, ok := byModel["gpt-5.6-terra"]; ok {
		t.Logf("gpt-5.6-terra modalities = %v provider = %s", terra.InputModal, terra.Provider)
		if !containsFold(terra.InputModal, "pdf") {
			t.Errorf("gpt-5.6-terra lost its curated pdf modality: %v", terra.InputModal)
		}
	}
	// models.dev lists this under 25 resellers; the vendor is OpenAI either way.
	if oss, ok := byModel["openai/gpt-oss-20b"]; ok {
		t.Logf("openai/gpt-oss-20b provider = %s", oss.Provider)
		if oss.Provider != "openai" {
			t.Errorf("openai/gpt-oss-20b provider = %q, want openai", oss.Provider)
		}
	}
}
