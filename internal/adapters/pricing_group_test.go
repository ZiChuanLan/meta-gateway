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
