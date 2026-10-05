package httpapi_test

import (
	"net/http"
	"testing"
)

// Billing markup is set per model. The Pricing board both sets a multiplier and
// takes one off again; the second half used to be impossible — the handler
// rejected every negative ratio, while the store treats a negative ratio as
// "delete this row". A markup could therefore be raised but never removed.
//
// The assertions go through the store rather than the response body: what makes
// the feature real is the row the billing path reads, not the echo back.
func TestModelRatioSetAndRemove(t *testing.T) {
	srv, db, _ := revealTestServer(t)

	status, body, raw := adminCall(t, srv.URL, http.MethodPut, "/admin/ratios/gpt-4o", map[string]any{"ratio": 2})
	if status != http.StatusOK {
		t.Fatalf("set ratio = %d %s", status, raw)
	}
	if body["ratio"] != float64(2) {
		t.Fatalf("set ratio body = %v", body)
	}
	if got, err := db.ModelRatio.GetRatio("gpt-4o"); err != nil || got != 2 {
		t.Fatalf("stored ratio = %v (%v), want 2", got, err)
	}

	// A negative ratio removes the row, restoring the 1× default.
	status, body, raw = adminCall(t, srv.URL, http.MethodPut, "/admin/ratios/gpt-4o", map[string]any{"ratio": -1})
	if status != http.StatusOK {
		t.Fatalf("remove ratio = %d %s", status, raw)
	}
	if body["deleted"] != true {
		t.Fatalf("remove ratio body = %v, want deleted", body)
	}
	if got, err := db.ModelRatio.GetRatio("gpt-4o"); err != nil || got != 1 {
		t.Fatalf("ratio after delete = %v (%v), want 1 (no markup)", got, err)
	}

	// Out-of-range ceilings are still refused: the bound is what keeps a typo
	// from multiplying every account's bill by a thousand.
	if status, _, raw = adminCall(t, srv.URL, http.MethodPut, "/admin/ratios/gpt-4o", map[string]any{"ratio": 1001}); status != http.StatusBadRequest {
		t.Fatalf("ratio above ceiling = %d %s, want 400", status, raw)
	}
	// A model with no ratio configured must not be created by reading it.
	if got, _ := db.ModelRatio.GetRatio("never-set"); got != 1 {
		t.Fatalf("unknown model ratio = %v, want 1", got)
	}
}
