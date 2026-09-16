package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// newTestService wires a Service against a scratch database and a stub pair of
// catalog endpoints, so the plan/apply logic is exercised without the network.
func newTestService(t *testing.T) (*Service, *store.DB) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	litellm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(litellmFixture))
	}))
	t.Cleanup(litellm.Close)
	modelsdev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(modelsDevFixture))
	}))
	t.Cleanup(modelsdev.Close)

	service := New(db, Options{
		Client: litellm.Client(),
		URLs: map[string]string{
			SourceLiteLLM:   litellm.URL,
			SourceModelsDev: modelsdev.URL,
		},
	})
	return service, db
}

func changeFields(changes []domain.CatalogFieldChange) map[string]domain.CatalogFieldChange {
	out := make(map[string]domain.CatalogFieldChange, len(changes))
	for _, change := range changes {
		out[change.Field] = change
	}
	return out
}

func findItem(t *testing.T, preview *domain.CatalogPreview, model string) domain.CatalogPreviewItem {
	t.Helper()
	for _, item := range preview.Items {
		if item.Model == model {
			return item
		}
	}
	t.Fatalf("model %q missing from preview", model)
	return domain.CatalogPreviewItem{}
}

func TestSyncCreatesCapabilitiesAndFillsMetadataWithPrices(t *testing.T) {
	service, db := newTestService(t)

	state, err := service.Sync(context.Background(),
		[]string{"gpt-5", "gpt-image-1", "claude-sonnet-4-5", "flux-pro-1.1"},
		DefaultPolicy)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if state.Requested != 4 || state.Matched != 4 || state.Missing != 0 {
		t.Fatalf("state = %+v", state)
	}
	if state.Capabilities != 4 {
		t.Errorf("capabilities written = %d, want 4", state.Capabilities)
	}
	if state.Prices == 0 {
		t.Error("expected prices to be written")
	}
	if state.SyncedAt == "" {
		t.Error("synced_at should be recorded")
	}

	cap, err := db.ModelCapability.Get("gpt-5")
	if err != nil || cap == nil {
		t.Fatalf("capability: %v %v", cap, err)
	}
	if cap.Source != domain.CapabilitySourceCatalog {
		t.Errorf("source = %q", cap.Source)
	}
	if cap.Kind != domain.CapabilityKindChat {
		t.Errorf("kind = %q", cap.Kind)
	}
	// LiteLLM is the shape authority even when models.dev also knows the model.
	if got := domain.JoinCSV(cap.Endpoints); got != "/v1/chat/completions,/v1/batch" {
		t.Errorf("endpoints = %q", got)
	}

	// models.dev carries no shape, so a model only it knows keeps the
	// classifier-derived endpoints for its refined kind.
	flux, err := db.ModelCapability.Get("flux-pro-1.1")
	if err != nil || flux == nil {
		t.Fatalf("flux capability: %v %v", flux, err)
	}
	if flux.Kind != domain.CapabilityKindImageGen {
		t.Errorf("flux kind = %q", flux.Kind)
	}
	if got := domain.JoinCSV(flux.Endpoints); got != "/v1/images/generations" {
		t.Errorf("flux endpoints = %q", got)
	}

	// The image model's vendor is the one its name implies, and its price lands
	// in the per-1k unit the billing chain reads.
	image, err := db.ModelMetadata.Get("flux-pro-1.1")
	if err != nil || image == nil {
		t.Fatalf("flux metadata: %v %v", image, err)
	}
	if image.PriceCompletionPer1k != 0.04 {
		t.Errorf("flux completion price = %v, want 0.04", image.PriceCompletionPer1k)
	}
	if image.Vendor != "blackforestlabs" {
		t.Errorf("flux vendor = %q, want the name-derived vendor", image.Vendor)
	}

	// A second sync over an unchanged registry is a no-op for capabilities.
	state, err = service.Sync(context.Background(), []string{"gpt-5"}, DefaultPolicy)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if state.Capabilities != 0 {
		t.Errorf("second sync rewrote %d capabilities, want 0", state.Capabilities)
	}
}

func TestSyncNeverOverwritesAManualOverrideOrAnExistingPrice(t *testing.T) {
	service, db := newTestService(t)

	if err := db.ModelCapability.Upsert(&domain.Capability{
		Model: "gpt-5", Kind: domain.CapabilityKindChat, Provider: "hand-picked",
		Endpoints: []string{"/v1/chat/completions"}, InputFormats: []string{"json"},
		Source: domain.CapabilitySourceManual, Notes: "operator",
	}); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{
		ModelName: "gpt-5", ContextWindow: 999, SupportsThinking: 1,
		InputModalities: "text", OutputModalities: "text",
		PricePromptPer1k: 0.5, PriceCompletionPer1k: 1.5, PriceCachePer1k: 0.25,
		Vendor: "operator",
	}); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}

	preview, err := service.Preview(context.Background(), []string{"gpt-5"}, DefaultPolicy)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	item := findItem(t, preview, "gpt-5")
	if item.CapabilityAction != domain.CatalogActionSkipManual {
		t.Errorf("capability action = %q, want skip_manual", item.CapabilityAction)
	}
	if len(item.CapabilityChanges) != 0 {
		t.Errorf("a manual row should propose no capability changes, got %v", item.CapabilityChanges)
	}
	if item.MetadataAction != domain.CatalogActionUnchanged {
		t.Errorf("metadata action = %q, want unchanged (changes %v)", item.MetadataAction, item.MetadataChanges)
	}
	if item.PriceAction != domain.CatalogActionUnchanged {
		t.Errorf("price action = %q, want unchanged (changes %v)", item.PriceAction, item.PriceChanges)
	}

	state, err := service.Sync(context.Background(), []string{"gpt-5"}, DefaultPolicy)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if state.SkippedManual != 1 {
		t.Errorf("skipped_manual = %d, want 1", state.SkippedManual)
	}
	if state.Prices != 0 {
		t.Errorf("prices written = %d, want 0", state.Prices)
	}

	stored, err := db.ModelCapability.Get("gpt-5")
	if err != nil || stored == nil {
		t.Fatalf("capability: %v %v", stored, err)
	}
	if stored.Source != domain.CapabilitySourceManual || stored.Provider != "hand-picked" {
		t.Errorf("manual row was rewritten: %+v", stored)
	}
	meta, err := db.ModelMetadata.Get("gpt-5")
	if err != nil || meta == nil {
		t.Fatalf("metadata: %v %v", meta, err)
	}
	if meta.ContextWindow != 999 || meta.PricePromptPer1k != 0.5 || meta.Vendor != "operator" {
		t.Errorf("operator metadata was rewritten: %+v", meta)
	}
}

func TestSyncRefreshesItsOwnRowsButLeavesPricesDisabledAlone(t *testing.T) {
	service, db := newTestService(t)

	if _, err := service.Sync(context.Background(), []string{"gpt-image-1"}, DefaultPolicy); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// Simulate a stale catalog row: the sync owns it, so it may be refreshed.
	stored, err := db.ModelCapability.Get("gpt-image-1")
	if err != nil || stored == nil {
		t.Fatalf("capability: %v %v", stored, err)
	}
	stored.Kind = domain.CapabilityKindChat
	stored.Endpoints = []string{"/v1/chat/completions"}
	if err := db.ModelCapability.Upsert(stored); err != nil {
		t.Fatalf("stale write: %v", err)
	}

	preview, err := service.Preview(context.Background(), []string{"gpt-image-1"}, DefaultPolicy)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	item := findItem(t, preview, "gpt-image-1")
	if item.CapabilityAction != domain.CatalogActionRefresh {
		t.Fatalf("capability action = %q, want refresh", item.CapabilityAction)
	}
	fields := changeFields(item.CapabilityChanges)
	if fields["kind"].To != domain.CapabilityKindImageGen {
		t.Errorf("kind change = %+v", fields["kind"])
	}

	// With prices switched off the plan must not propose or write them, and the
	// metadata-only run must still leave the price columns alone.
	if err := db.ModelMetadata.Delete("gpt-image-1"); err != nil {
		t.Fatalf("clear metadata: %v", err)
	}
	state, err := service.Sync(context.Background(), []string{"gpt-image-1"},
		Policy{Capabilities: true, Metadata: true, Prices: false})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if state.Prices != 0 {
		t.Errorf("prices written = %d, want 0", state.Prices)
	}
	meta, err := db.ModelMetadata.Get("gpt-image-1")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta != nil && meta.PricePromptPer1k != 0 {
		t.Errorf("price should stay unset, got %v", meta.PricePromptPer1k)
	}
}

func TestSyncFillsOnlyThePriceColumnsThatAreUnset(t *testing.T) {
	service, db := newTestService(t)

	// The operator priced the prompt side by hand and left the rest at zero.
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{
		ModelName: "gpt-5", ContextWindow: 128000, SupportsThinking: 1,
		InputModalities: "text,image", OutputModalities: "text",
		PricePromptPer1k: 9, Vendor: "operator",
	}); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}

	preview, err := service.Preview(context.Background(), []string{"gpt-5"}, DefaultPolicy)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	item := findItem(t, preview, "gpt-5")
	fields := changeFields(item.PriceChanges)
	if _, ok := fields["price_prompt_per_1k"]; ok {
		t.Error("an operator-set prompt price must not be proposed for change")
	}
	if fields["price_completion_per_1k"].To != "0.01" {
		t.Errorf("completion change = %+v", fields["price_completion_per_1k"])
	}
	if fields["price_cache_per_1k"].To != "0.000125" {
		t.Errorf("cache change = %+v", fields["price_cache_per_1k"])
	}

	if _, err := service.Sync(context.Background(), []string{"gpt-5"}, DefaultPolicy); err != nil {
		t.Fatalf("sync: %v", err)
	}
	meta, err := db.ModelMetadata.Get("gpt-5")
	if err != nil || meta == nil {
		t.Fatalf("metadata: %v %v", meta, err)
	}
	if meta.PricePromptPer1k != 9 {
		t.Errorf("prompt price = %v, want the operator value", meta.PricePromptPer1k)
	}
	if meta.PriceCompletionPer1k != 0.01 || meta.PriceCachePer1k != 0.000125 {
		t.Errorf("prices = %v/%v", meta.PriceCompletionPer1k, meta.PriceCachePer1k)
	}
}

func TestSyncRecordsStateAndSurvivesAMissingModel(t *testing.T) {
	service, db := newTestService(t)

	state, err := service.Sync(context.Background(), []string{"not-in-any-catalog"}, DefaultPolicy)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if state.Missing != 1 || state.Matched != 0 {
		t.Errorf("state = %+v", state)
	}
	if state.Capabilities != 0 {
		t.Errorf("capabilities written = %d, want 0", state.Capabilities)
	}
	saved, err := db.ModelCatalog.GetCatalogState()
	if err != nil || saved == nil {
		t.Fatalf("state: %v %v", saved, err)
	}
	if saved.Missing != 1 {
		t.Errorf("persisted state = %+v", saved)
	}
	if len(saved.Sources) != 2 {
		t.Errorf("persisted sources = %v", saved.Sources)
	}
}

func TestFetchKeepsWorkingWhenOneSourceIsDown(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(litellmFixture))
	}))
	t.Cleanup(working.Close)

	service := New(db, Options{
		Client: working.Client(),
		URLs: map[string]string{
			SourceLiteLLM:   working.URL,
			SourceModelsDev: broken.URL,
		},
	})
	entries, problems, err := service.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("a healthy source should still yield entries")
	}
	if len(problems) != 1 {
		t.Errorf("problems = %v, want one reported failure", problems)
	}

	// A sync with both sources down is a hard error rather than a silent no-op,
	// so the operator learns the registry was not refreshed.
	bothBroken := New(db, Options{
		Client: broken.Client(),
		URLs: map[string]string{
			SourceLiteLLM:   broken.URL,
			SourceModelsDev: broken.URL,
		},
	})
	if _, err := bothBroken.Sync(context.Background(), []string{"gpt-5"}, DefaultPolicy); err == nil {
		t.Fatal("expected an error when every source fails")
	}
}

func TestFetchReportsSkippedRowsWithoutFailingTheSource(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// One readable model and one unreadable row. Reporting the loss is the
	// whole point of the exercise: a silently partial import is how a registry
	// goes stale without anyone noticing.
	partial := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"kept": {"mode": "chat"}, "dropped": 42}`))
	}))
	t.Cleanup(partial.Close)
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(empty.Close)

	service := New(db, Options{
		Client: partial.Client(),
		URLs: map[string]string{
			SourceLiteLLM:   partial.URL,
			SourceModelsDev: empty.URL,
		},
	})
	entries, problems, err := service.Fetch(context.Background())
	if err != nil {
		t.Fatalf("a skipped row must not fail the source: %v", err)
	}
	if len(entries) != 1 || entries[0].Model != "kept" {
		t.Fatalf("entries = %+v, want just the readable row", entries)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "1 row(s) skipped") {
		t.Errorf("problems = %v, want a single skip notice", problems)
	}

	// The notice has to reach the plan, otherwise the operator reviews a diff
	// that is missing rows with no hint that anything was dropped.
	preview, err := service.Preview(context.Background(), []string{"kept"}, DefaultPolicy)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Matched != 1 {
		t.Errorf("matched = %d, want 1", preview.Matched)
	}
	if len(preview.Errors) != 1 || !strings.Contains(preview.Errors[0], "row(s) skipped") {
		t.Errorf("preview errors = %v, want the skip notice", preview.Errors)
	}
}
