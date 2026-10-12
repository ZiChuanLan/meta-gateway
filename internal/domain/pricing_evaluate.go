package domain

import (
	"math"
	"math/big"
	"strconv"
	"time"
)

// BillingRates is the one effective-rate result used by billing and previews.
type BillingRates struct {
	Input      float64 `json:"input_per_1k"`
	Output     float64 `json:"output_per_1k"`
	CacheRead  float64 `json:"cache_per_1k"`
	CacheWrite float64 `json:"cache_write_per_1k"`
	PerRequest float64 `json:"per_request"`
	Multiplier float64 `json:"multiplier"`
	Timezone   string  `json:"timezone"`
}

func (l PriceLayer) Rates(input int, at time.Time) BillingRates {
	if l.Timezone != "" {
		if loc, err := time.LoadLocation(l.Timezone); err == nil {
			at = at.In(loc)
		}
	}
	a, b, c, d := l.EffectivePrices(input)
	if l.State == "free" || l.State == "inherit" {
		a, b, c, d = 0, 0, 0, 0
	}
	if l.State != "explicit" && !l.CachePresent && c <= 0 {
		c = a
	}
	cw := a
	if l.CacheWrite != nil {
		cw = *l.CacheWrite
	}
	if l.State == "free" || l.State == "inherit" {
		cw = 0
	}
	return BillingRates{Input: a, Output: b, CacheRead: c, CacheWrite: cw, PerRequest: d, Multiplier: l.TimeMultiplier(at), Timezone: at.Location().String()}
}

// DecimalCharge combines decimal rates once, converting to the existing float
// ledger only at the end. Invalid values never credit a user or fabricate cost.
func DecimalCharge(r BillingRates, ordinary, output, cacheRead, cacheWrite int, ratio float64) (float64, bool) {
	decimal := func(value float64) *big.Rat {
		q, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
		if !ok {
			return nil
		}
		return q
	}
	for _, v := range []float64{r.Input, r.Output, r.CacheRead, r.CacheWrite, r.PerRequest, r.Multiplier, ratio} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
	}
	cost := decimal(r.PerRequest)
	for _, term := range []struct {
		count int
		price float64
	}{{ordinary, r.Input}, {output, r.Output}, {cacheRead, r.CacheRead}, {cacheWrite, r.CacheWrite}} {
		if term.count < 0 {
			return 0, false
		}
		value := new(big.Rat).Mul(new(big.Rat).SetFrac64(int64(term.count), 1000), decimal(term.price))
		cost.Add(cost, value)
	}
	cost.Mul(cost, decimal(r.Multiplier))
	cost.Mul(cost, decimal(ratio))
	v, _ := cost.Float64()
	return v, !math.IsInf(v, 0) && !math.IsNaN(v)
}
