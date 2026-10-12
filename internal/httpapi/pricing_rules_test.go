package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestBillingRulesHTTPSeparateQuotesAndPolicy(t *testing.T) {
	srv, db, _ := revealTestServer(t)
	path := "/admin/billing-rule?scope=model&key=priced-model"
	rule := map[string]any{"state": "explicit", "mode": "token", "input_per_1k": 1, "output_per_1k": 0, "cache_per_1k": 0, "cache_write_per_1k": 1.25, "timezone": "UTC"}
	status, _, raw := adminCall(t, srv.URL, http.MethodPut, path, map[string]any{"rule": rule})
	if status != 400 {
		t.Fatalf("missing revision=%d %s", status, raw)
	}
	status, _, raw = adminCall(t, srv.URL, http.MethodPut, path, map[string]any{"rule": rule, "expected_revision": 0})
	if status != 200 {
		t.Fatalf("save=%d %s", status, raw)
	}
	status, view, raw := adminCall(t, srv.URL, http.MethodGet, path, nil)
	if status != 200 || view["effective_state"] != "explicit" {
		t.Fatalf("read=%d %s", status, raw)
	}
	status, _, _ = adminCall(t, srv.URL, http.MethodPut, path, map[string]any{"rule": rule, "expected_revision": 0})
	if status != 409 {
		t.Fatalf("stale revision=%d", status)
	}
	a, b, c := .1, .2, 0.0
	q := domain.PriceQuoteSnapshot{Model: "priced-model", ScopeKey: "reference:priced-model", Source: "catalog", Purpose: "reference", Status: "calculable", Unit: domain.QuoteUnit{Kind: "iso", Code: "USD"}, Values: domain.QuoteValues{Mode: "token", Input: &a, Output: &b, CacheRead: &c}, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	id, err := db.SavePriceQuote(q)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = adminCall(t, srv.URL, http.MethodPost, "/admin/billing-rule/copy-quote?scope=model&key=priced-model", map[string]any{"quote_id": id, "expected_revision": 1})
	if status != 200 {
		t.Fatalf("copy=%d %s", status, raw)
	}
	status, _, raw = adminCall(t, srv.URL, http.MethodPost, "/admin/price-quotes/estimate", map[string]any{"quote_id": id, "workload": map[string]any{"input_tokens": 1000, "output_tokens": 100}, "balance": 1, "balance_unit": "CNY"})
	if status != 400 {
		t.Fatalf("mixed unit estimate=%d %s", status, raw)
	}
	status, estimate, raw := adminCall(t, srv.URL, http.MethodPost, "/admin/price-quotes/estimate", map[string]any{"quote_id": id, "workload": map[string]any{"input_tokens": 1000, "output_tokens": 100}, "balance": 1, "balance_unit": "USD"})
	if status != 200 || estimate["status"] != "estimated" || estimate["calls"] != float64(8333) {
		t.Fatalf("estimate=%d %s", status, raw)
	}
	// Refresh is observation-only; the copied rule stays on the old quote.
	q.Values.Input = &b
	newID, err := db.SavePriceQuote(q)
	if err != nil || newID == id {
		t.Fatal(err)
	}
	saved, err := db.BillingRule("model", "priced-model")
	if err != nil || saved.QuoteID != id || saved.Input == nil || *saved.Input != .0001 {
		t.Fatalf("refresh changed billing: %+v %v", saved, err)
	}
	// A reviewed quote stays copyable after a background refresh wrote a newer
	// observation for the same scope: the payload is immutable, the operator saw
	// the values, and the copy records which quote it came from.
	status, _, raw = adminCall(t, srv.URL, http.MethodPost, "/admin/billing-rule/copy-quote?scope=model&key=priced-model", map[string]any{"quote_id": id, "expected_revision": 2})
	if status != 200 {
		t.Fatalf("reviewed quote=%d %s", status, raw)
	}
	saved, err = db.BillingRule("model", "priced-model")
	if err != nil || saved.QuoteID != id || saved.Input == nil || *saved.Input != .0001 {
		t.Fatalf("reviewed quote not re-applied: %+v %v", saved, err)
	}
	// No anonymous caller gets private price/account evidence.
	resp, err := http.Get(srv.URL + "/admin/price-quotes")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous quotes=%d", resp.StatusCode)
	}
}

func TestManualQuoteCannotInventFreeRates(t *testing.T) {
	srv, db, _ := revealTestServer(t)
	site, err := db.Site.Create(&domain.Site{Name: "quote", BaseURL: "https://quotes.example", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"model": "m", "site_id": site, "unit": map[string]any{"kind": "site_credit", "code": "native"}, "values": map[string]any{"mode": "token"}}
	status, _, _ := adminCall(t, srv.URL, http.MethodPost, "/admin/price-quotes", body)
	if status != 400 {
		t.Fatalf("missing values accepted: %d", status)
	}
	body["values"] = map[string]any{"mode": "token", "input_per_million": 1, "output_per_million": 0}
	status, _, _ = adminCall(t, srv.URL, http.MethodPost, "/admin/price-quotes", body)
	if status != 201 {
		t.Fatalf("explicit free output rejected: %d", status)
	}
	rule, err := db.BillingRule("model", "m")
	if err != nil || rule != nil {
		t.Fatalf("observation changed billing: %+v %v", rule, err)
	}
}
