package domain

import (
	"testing"
	"time"
)

func TestEstimateQuoteKeepsNativeUnitAndWorkload(t *testing.T) {
	input, output, cache, balance := 2.0, 10.0, 0.0, 3.0
	q := PriceQuoteSnapshot{Model: "m", Unit: QuoteUnit{Kind: "site_credit", Code: "credit:site/1"}, Status: "calculable", Values: QuoteValues{Mode: "token", Input: &input, Output: &output, CacheRead: &cache}}
	result := EstimateQuote(q, PriceWorkload{Input: 1000, Output: 100, CacheRead: 800}, &balance, time.Now())
	if result.Status != "estimated" || result.Cost == nil || *result.Cost != .003 || result.Calls == nil || *result.Calls != 1000 || result.Unit.Kind != "site_credit" {
		t.Fatalf("estimate=%+v", result)
	}
	q.Values.CacheRead = nil
	result = EstimateQuote(q, PriceWorkload{CacheRead: 1}, &balance, time.Now())
	if result.Status != "unavailable" || result.Reason != "missing_cache_read_price" {
		t.Fatalf("missing price=%+v", result)
	}
	q.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	result = EstimateQuote(q, PriceWorkload{}, nil, time.Now())
	if result.Reason != "stale_quote" {
		t.Fatalf("stale=%+v", result)
	}
}
func TestZeroPriceIsNotUnlimitedCalls(t *testing.T) {
	fee, balance := 0.0, 10.0
	result := EstimateQuote(PriceQuoteSnapshot{Status: "calculable", Unit: QuoteUnit{Kind: "iso", Code: "USD"}, Values: QuoteValues{Mode: "request", PerRequest: &fee}}, PriceWorkload{}, &balance, time.Now())
	if result.Status != "estimated" || result.Calls != nil || result.Reason != "zero_price_does_not_remove_other_limits" {
		t.Fatalf("zero=%+v", result)
	}
}
