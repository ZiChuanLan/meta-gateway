package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestModelCapabilityRoundTrip(t *testing.T) {
	db := openTestDB(t)

	items, err := db.ModelCapability.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("fresh registry = %d rows", len(items))
	}

	cap := domain.Capability{
		Model:            "grok-imagine-image-edit",
		Kind:             domain.CapabilityKindImageEdit,
		Provider:         "xai",
		Endpoints:        []string{"/v1/images/edits"},
		InputFormats:     []string{"json"},
		InputModalities:  []string{"text", "image"},
		OutputModalities: []string{"image"},
		MaxInputImages:   8,
		Source:           domain.CapabilitySourceManual,
		Notes:            "json only",
	}
	if err := db.ModelCapability.Upsert(&cap); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := db.ModelCapability.Get("grok-imagine-image-edit")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.Kind != domain.CapabilityKindImageEdit {
		t.Errorf("kind = %q", got.Kind)
	}
	if !got.HasInputFormat("json") || got.HasInputFormat("multipart") {
		t.Errorf("input formats = %v", got.InputFormats)
	}
	if got.MaxInputImages != 8 || !got.CanAcceptImages() {
		t.Errorf("images = %d, modalities = %v", got.MaxInputImages, got.InputModalities)
	}
	if got.SupportsStream {
		// Upserted with the zero value: must round-trip as false, not the
		// database default of 1.
		t.Error("supports_stream should persist as false")
	}

	// Update in place.
	cap.MaxInputImages = 4
	if err := db.ModelCapability.Upsert(&cap); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	got, _ = db.ModelCapability.Get("grok-imagine-image-edit")
	if got.MaxInputImages != 4 {
		t.Errorf("after update max_input_images = %d", got.MaxInputImages)
	}

	if err := db.ModelCapability.Delete("grok-imagine-image-edit"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := db.ModelCapability.Get("grok-imagine-image-edit"); got != nil {
		t.Fatal("row survived delete")
	}
}

func TestModelCapabilityResolveFallsBackToClassifier(t *testing.T) {
	db := openTestDB(t)

	// Unknown model: classifier answer, flagged as builtin.
	cap, fromDB := db.ModelCapability.Resolve("grok-imagine-image-edit")
	if fromDB {
		t.Fatal("expected classifier fallback")
	}
	if cap.Kind != domain.CapabilityKindImageEdit {
		t.Fatalf("kind = %q", cap.Kind)
	}
	if !cap.ResolvedByBuiltin {
		t.Error("expected resolved_by_builtin flag")
	}

	// Once persisted, the row wins.
	override := domain.ClassifyModel("grok-imagine-image-edit")
	override.Endpoints = []string{"/v1/images/edits", "/v1/chat/completions"}
	override.Source = domain.CapabilitySourceManual
	if err := db.ModelCapability.Upsert(&override); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cap, fromDB = db.ModelCapability.Resolve("grok-imagine-image-edit")
	if !fromDB {
		t.Fatal("expected stored row to win")
	}
	if !cap.HasEndpoint("/v1/chat/completions") {
		t.Error("override endpoint lost")
	}
}

func TestModelCapabilityResolveMany(t *testing.T) {
	db := openTestDB(t)
	got := db.ModelCapability.ResolveMany([]string{"gpt-image-2", "", "sora-2"})
	if len(got) != 2 {
		t.Fatalf("ResolveMany = %v", got)
	}
	if got["gpt-image-2"].Kind != domain.CapabilityKindImageEdit {
		t.Errorf("gpt-image-2 = %q", got["gpt-image-2"].Kind)
	}
	if got["sora-2"].Kind != domain.CapabilityKindVideo {
		t.Errorf("sora-2 = %q", got["sora-2"].Kind)
	}
}

// AutoTag seeds discovery rows but must never clobber an operator override —
// otherwise a discovery sweep would silently revert a manual correction.
func TestModelCapabilityAutoTagPreservesManual(t *testing.T) {
	db := openTestDB(t)

	manual := domain.ClassifyModel("gpt-image-2")
	manual.MaxInputImages = 1
	manual.Source = domain.CapabilitySourceManual
	manual.Notes = "operator says one image only"
	if err := db.ModelCapability.Upsert(&manual); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.ModelCapability.AutoTag("gpt-image-2"); err != nil {
		t.Fatalf("autotag: %v", err)
	}
	got, _ := db.ModelCapability.Get("gpt-image-2")
	if got.MaxInputImages != 1 || got.Source != domain.CapabilitySourceManual {
		t.Fatalf("manual override clobbered: %+v", got)
	}

	// A brand-new model gets a discovery row.
	if err := db.ModelCapability.AutoTag("grok-imagine-image-edit"); err != nil {
		t.Fatalf("autotag2: %v", err)
	}
	got, _ = db.ModelCapability.Get("grok-imagine-image-edit")
	if got == nil || got.Source != domain.CapabilitySourceDiscovery {
		t.Fatalf("discovery row = %+v", got)
	}
	if got.Kind != domain.CapabilityKindImageEdit {
		t.Errorf("kind = %q", got.Kind)
	}

	// Idempotent: re-tagging must not downgrade the source to builtin.
	if err := db.ModelCapability.AutoTag("grok-imagine-image-edit"); err != nil {
		t.Fatalf("autotag3: %v", err)
	}
	got, _ = db.ModelCapability.Get("grok-imagine-image-edit")
	if got.Source != domain.CapabilitySourceDiscovery {
		t.Errorf("source after re-tag = %q", got.Source)
	}
}

// A classifier-derived row is refreshed, so fixing a rule reaches the models it
// already labelled. Without this, a row stays wrong forever after the rule is
// corrected.
func TestModelCapabilityAutoTagRefreshesItsOwnRows(t *testing.T) {
	db := openTestDB(t)

	// A discovery row from before "mimo" was known to be Xiaomi's.
	stale := domain.ClassifyModel("mimo-v2.5")
	stale.Provider = ""
	stale.Source = domain.CapabilitySourceDiscovery
	if err := db.ModelCapability.Upsert(&stale); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.ModelCapability.AutoTag("mimo-v2.5"); err != nil {
		t.Fatalf("autotag: %v", err)
	}
	got, _ := db.ModelCapability.Get("mimo-v2.5")
	if got.Provider != "xiaomi" {
		t.Errorf("provider = %q, want the refreshed xiaomi", got.Provider)
	}
}

// A catalog row is not, because the built-in classifier is cruder: it knows no
// context limits, prices or curated modalities and would replace better data
// with a guess.
func TestModelCapabilityAutoTagLeavesCatalogRowsAlone(t *testing.T) {
	db := openTestDB(t)

	curated := domain.ClassifyModel("mimo-v2.5")
	curated.Provider = "requesty"
	curated.InputModalities = []string{"text", "image", "audio", "video"}
	curated.Source = domain.CapabilitySourceCatalog
	if err := db.ModelCapability.Upsert(&curated); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.ModelCapability.AutoTag("mimo-v2.5"); err != nil {
		t.Fatalf("autotag: %v", err)
	}
	got, _ := db.ModelCapability.Get("mimo-v2.5")
	if got.Source != domain.CapabilitySourceCatalog {
		t.Errorf("source = %q, want catalog kept", got.Source)
	}
	if got.Provider != "requesty" || len(got.InputModalities) != 4 {
		t.Errorf("catalog row clobbered by the built-in guess: %+v", got)
	}
}
