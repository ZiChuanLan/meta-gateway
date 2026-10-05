package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The ladder and the schedule cross the HTTP boundary as JSON text. These tests
// pin the boundary rather than the parser: a stored ladder must be the value
// the handler approved, and a malformed one must never reach storage — it would
// otherwise be parsed on every relay afterwards.
func TestModelMetadataAcceptsPriceLadder(t *testing.T) {
	srv, db, _ := revealTestServer(t)

	ladder := `[{"max_prompt_tokens":32000,"prompt":0.15,"completion":0.6,"cache":0.03},{"max_prompt_tokens":0,"prompt":1.5,"completion":6}]`
	schedule := `[{"days":[1,2,3,4,5],"from_hour":22,"to_hour":6,"multiplier":0.5}]`
	status, _, raw := adminCall(t, srv.URL, http.MethodPut, "/admin/model-metadata/ladder-model", map[string]any{
		"price_tiers":    ladder,
		"price_schedule": schedule,
	})
	if status != http.StatusOK {
		t.Fatalf("upsert = %d %s", status, raw)
	}
	meta, err := db.ModelMetadata.Get("ladder-model")
	if err != nil || meta == nil {
		t.Fatalf("get metadata = %v (%v)", meta, err)
	}
	// The stored value is what the handler re-encoded, so it must round-trip
	// through the same parser the billing path uses.
	var tiers []map[string]any
	if err := json.Unmarshal([]byte(meta.PriceTiers), &tiers); err != nil {
		t.Fatalf("stored ladder is not JSON: %v (%q)", err, meta.PriceTiers)
	}
	if len(tiers) != 2 || tiers[0]["prompt"] != 0.15 {
		t.Fatalf("stored ladder = %v", tiers)
	}
	var windows []map[string]any
	if err := json.Unmarshal([]byte(meta.PriceSchedule), &windows); err != nil {
		t.Fatalf("stored schedule is not JSON: %v (%q)", err, meta.PriceSchedule)
	}
	if len(windows) != 1 || windows[0]["multiplier"] != 0.5 {
		t.Fatalf("stored schedule = %v", windows)
	}

	// An empty string clears the ladder — the way back to flat pricing.
	if status, _, raw = adminCall(t, srv.URL, http.MethodPut, "/admin/model-metadata/ladder-model", map[string]any{
		"price_tiers":    "",
		"price_schedule": "",
	}); status != http.StatusOK {
		t.Fatalf("clear = %d %s", status, raw)
	}
	meta, _ = db.ModelMetadata.Get("ladder-model")
	if meta.PriceTiers != "" || meta.PriceSchedule != "" {
		t.Fatalf("cleared metadata still carries pricing: %q / %q", meta.PriceTiers, meta.PriceSchedule)
	}
}

func TestModelMetadataRejectsBrokenPricing(t *testing.T) {
	srv, db, _ := revealTestServer(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"ladder is not JSON", map[string]any{"price_tiers": "{not json"}},
		{"two open-ended rungs", map[string]any{"price_tiers": `[{"max_prompt_tokens":0},{"max_prompt_tokens":0}]`}},
		{"negative ceiling", map[string]any{"price_tiers": `[{"max_prompt_tokens":-1}]`}},
		{"weekday zero is not ISO", map[string]any{"price_schedule": `[{"days":[0],"from_hour":1,"to_hour":2,"multiplier":1}]`}},
		{"hour out of range", map[string]any{"price_schedule": `[{"from_hour":24,"to_hour":3,"multiplier":1}]`}},
		{"zero multiplier would give usage away", map[string]any{"price_schedule": `[{"from_hour":1,"to_hour":2,"multiplier":0}]`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _, raw := adminCall(t, srv.URL, http.MethodPut, "/admin/model-metadata/broken-model", tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d %s, want 400", status, raw)
			}
			// Nothing was written: a refused value must not leave a partial row.
			meta, err := db.ModelMetadata.Get("broken-model")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if meta != nil {
				t.Fatalf("a refused ladder was stored: %+v", meta)
			}
		})
	}
}
