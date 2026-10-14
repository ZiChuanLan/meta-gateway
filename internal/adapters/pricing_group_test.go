package adapters

import "testing"

func TestPricingGroupDoesNotPickRandomMultiplier(t *testing.T) {
	raw := []byte(`{"data":[{"model_name":"m","quota_type":0,"model_ratio":1}],"group_ratio":{"vip":0.5,"pro":2}}`)
	for i := 0; i < 50; i++ {
		items, err := ParsePricingPayload(raw)
		if err != nil || len(items) != 1 {
			t.Fatal(err)
		}
		q, ok := NormalizePrice(items[0], 500000)
		if !ok || q.Status != "unknown_group" {
			t.Fatalf("invented default group: %+v", q)
		}
		q, ok = NormalizePrice(WithPricingGroup(items[0], "vip"), 500000)
		if !ok || q.Status != "calculable" || q.InputPerMillion != 1 {
			t.Fatalf("vip quote=%+v", q)
		}
		q, _ = NormalizePrice(WithPricingGroup(items[0], "not-available"), 500000)
		if q.Status != "unknown_group" {
			t.Fatalf("unknown key group=%+v", q)
		}
	}
}
func TestPricingZeroGroupIsFree(t *testing.T) {
	items, err := ParsePricingPayload([]byte(`{"data":[{"model_name":"m","model_ratio":1}],"group_ratio":{"default":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	q, ok := NormalizePrice(items[0], 500000)
	if !ok || q.InputPerMillion != 0 || q.Status != "calculable" {
		t.Fatalf("zero multiplier lost: %+v", q)
	}
}

// The ladder reader and the group reader both have something to say about a
// tiered price from a site that prices per group, and the group wins: every rung
// it read is missing the multiplier this account pays with, so the quote stays
// not-adoptable instead of looking like a price that was measured.
func TestTieredPriceKeepsAnUnconfirmedGroup(t *testing.T) {
	ladder := `len <= 272000 ? tier("standard", p * 1 + c * 2) : tier("long", p * 2 + c * 4)`
	item := ModelPrice{Model: "m", BillingExpr: ladder}
	q, ok := NormalizePrice(item, 500000)
	if !ok || q.Status != "tiered" || len(q.Tiers) != 2 {
		t.Fatalf("confirmed group lost its ladder: %+v", q)
	}
	item.UnknownGroup = true
	q, ok = NormalizePrice(item, 500000)
	if !ok || q.Status != "unknown_group" || len(q.Tiers) != 2 {
		t.Fatalf("unconfirmed group was cleared by the ladder: %+v", q)
	}
	if q.Unparsed {
		t.Fatalf("rungs should still be read: %+v", q)
	}
}
