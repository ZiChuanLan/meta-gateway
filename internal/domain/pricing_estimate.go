package domain

import (
	"fmt"
	"math"
	"time"
)

// Workload uses exclusive input buckets; no amount is converted between units.
type PriceWorkload struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	CacheRead  int `json:"cache_read_tokens"`
	CacheWrite int `json:"cache_write_tokens"`
}
type PriceEstimate struct {
	Status     string        `json:"status"`
	Reason     string        `json:"reason,omitempty"`
	Unit       QuoteUnit     `json:"unit"`
	Cost       *float64      `json:"cost_per_call,omitempty"`
	Calls      *int64        `json:"calls,omitempty"`
	Workload   PriceWorkload `json:"workload"`
	Assumption string        `json:"assumption"`
}

func EstimateQuote(q PriceQuoteSnapshot, w PriceWorkload, balance *float64, at time.Time) PriceEstimate {
	result := PriceEstimate{Status: "unavailable", Unit: q.Unit, Workload: w, Assumption: "scenario; shared balance and external consumption may reduce remaining calls"}
	unavailable := func(reason string) PriceEstimate {
		result.Status = "unavailable"
		result.Cost = nil
		result.Calls = nil
		result.Reason = reason
		return result
	}
	if q.Status != "calculable" {
		return unavailable(q.Status)
	}
	if q.Unit.Kind == "unknown" {
		return unavailable("unknown_unit")
	}
	if q.ExpiresAt != "" {
		expiry, err := time.Parse(time.RFC3339Nano, q.ExpiresAt)
		if err != nil || !at.Before(expiry) {
			return unavailable("stale_quote")
		}
	}
	for _, v := range []int{w.Input, w.Output, w.CacheRead, w.CacheWrite} {
		if v < 0 || v > 100000000 {
			return unavailable("invalid_workload")
		}
	}
	rates := BillingRates{Multiplier: 1}
	if q.Values.Mode == "fixed" || q.Values.Mode == "request" {
		if q.Values.PerRequest == nil {
			return unavailable("missing_request_price")
		}
		rates.PerRequest = *q.Values.PerRequest
	} else if q.Values.Mode == "token" || q.Values.Mode == "hybrid" {
		for _, term := range []struct {
			count  int
			v      *float64
			target *float64
			name   string
		}{{w.Input, q.Values.Input, &rates.Input, "input"}, {w.Output, q.Values.Output, &rates.Output, "output"}, {w.CacheRead, q.Values.CacheRead, &rates.CacheRead, "cache_read"}, {w.CacheWrite, q.Values.CacheWrite, &rates.CacheWrite, "cache_write"}} {
			if term.v == nil {
				if term.count > 0 {
					return unavailable("missing_" + term.name + "_price")
				}
			} else {
				*term.target = *term.v / 1000
			}
		}
		if q.Values.Mode == "hybrid" {
			if q.Values.PerRequest == nil {
				return unavailable("missing_request_price")
			}
			rates.PerRequest = *q.Values.PerRequest
		}
	} else {
		return unavailable("unsupported_meter")
	}
	cost, ok := DecimalCharge(rates, w.Input, w.Output, w.CacheRead, w.CacheWrite, 1)
	if !ok {
		return unavailable("invalid_price")
	}
	result.Status = "estimated"
	result.Cost = &cost
	if balance != nil {
		if *balance < 0 || math.IsNaN(*balance) || math.IsInf(*balance, 0) {
			return unavailable("invalid_balance")
		}
		if cost > 0 {
			calls := math.Floor(*balance / cost)
			if calls > math.MaxInt64 {
				return unavailable("range_exceeded")
			}
			n := int64(calls)
			result.Calls = &n
		} else {
			result.Reason = "zero_price_does_not_remove_other_limits"
		}
	}
	return result
}
func (q PriceQuoteSnapshot) Validate() error {
	if q.Model == "" || len(q.Model) > 512 || len(q.Raw) > 8192 {
		return fmt.Errorf("invalid quote model")
	}
	if q.Unit.Kind != "iso" && q.Unit.Kind != "site_credit" && q.Unit.Kind != "unknown" {
		return fmt.Errorf("invalid native unit")
	}
	if len(q.Unit.Code) > 512 || q.Unit.Code == "" {
		return fmt.Errorf("native unit required")
	}
	for _, p := range []*float64{q.Values.Input, q.Values.Output, q.Values.CacheRead, q.Values.CacheWrite, q.Values.PerRequest, q.Unit.DisplayRate, q.Unit.USDPerUnit} {
		if p != nil && (*p < 0 || math.IsNaN(*p) || math.IsInf(*p, 0) || *p > 1e9) {
			return fmt.Errorf("invalid quote amount")
		}
	}
	return nil
}
