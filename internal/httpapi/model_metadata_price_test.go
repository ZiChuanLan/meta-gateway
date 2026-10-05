package httpapi_test

import (
	"net/http"
	"testing"
)

// The per-call price was a column the billing path already read and the store
// already round-tripped, but the model-metadata endpoint never accepted it — so
// only a Go test could ever set it, and an operator reading the docs would have
// had no way to make it true. This pins the write path end to end.
func TestModelMetadataAcceptsPerRequestPrice(t *testing.T) {
	srv, db, _ := revealTestServer(t)

	status, _, raw := adminCall(t, srv.URL, http.MethodPut, "/admin/model-metadata/gpt-image-2",
		map[string]any{"price_prompt_per_1k": 1.5, "price_per_request": 0.04})
	if status != http.StatusOK {
		t.Fatalf("upsert = %d %s", status, raw)
	}

	meta, err := db.ModelMetadata.Get("gpt-image-2")
	if err != nil || meta == nil {
		t.Fatalf("get metadata = %v (%v)", meta, err)
	}
	if meta.PricePerRequest != 0.04 {
		t.Fatalf("price_per_request = %v, want 0.04", meta.PricePerRequest)
	}
	if meta.PricePromptPer1k != 1.5 {
		t.Fatalf("price_prompt_per_1k = %v, want 1.5 (the other prices still travel)", meta.PricePromptPer1k)
	}

	// A negative price is refused rather than stored: the relay would otherwise
	// credit the account for using the model.
	if status, _, raw = adminCall(t, srv.URL, http.MethodPut, "/admin/model-metadata/gpt-image-2",
		map[string]any{"price_per_request": -1}); status != http.StatusBadRequest {
		t.Fatalf("negative price = %d %s, want 400", status, raw)
	}
}
