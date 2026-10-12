// Site price normalization.
//
// Every upstream publishes prices in its own shape: a New-API site gives a
// token ratio, a per-call price, a legacy quota-per-1M map, or — on newer forks
// — a tiered billing expression. The gateway stores one unit and only one:
// USD per 1M tokens for token-priced models, USD per request for call-priced
// ones. This file is the single place that conversion happens, shared by the
// finance sweep (internal/account) and the site-probe collector
// (internal/siteprobe), so the two can never disagree about what a site costs.
package adapters

import (
	"math"
	"strconv"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// PriceQuote is a normalized site price, denominated in the site's declared
// currency (Currency): amounts are per 1M tokens in token mode and per request
// in fixed mode. Nothing here is converted between currencies — an exchange rate
// the gateway invented would end up in someone's cost model.
type PriceQuote struct {
	Model    string `json:"model"`
	Mode     string `json:"mode"` // token | fixed
	Currency string `json:"currency,omitempty"`
	// CurrencySymbol is what the site prints ("$", "¥", "¤" for a custom one).
	CurrencySymbol      string  `json:"currency_symbol,omitempty"`
	InputPerMillion     float64 `json:"input_per_million,omitempty"`
	OutputPerMillion    float64 `json:"output_per_million,omitempty"`
	CacheReadPerMillion float64 `json:"cache_read_per_million,omitempty"`
	// CacheWritePerMillion is the cache-creation price (New-API's cw / a direct
	// cache_write). It is a separate dimension from the cache read price.
	CacheWritePerMillion float64 `json:"cache_write_per_million,omitempty"`
	PerRequest           float64 `json:"per_request,omitempty"`
	GroupRatio           float64 `json:"group_ratio,omitempty"`
	// HasCacheRead / HasCacheWrite record whether the upstream actually
	// published a cache price. A missing cache price is not the same as a free
	// one, so callers must not read 0 without checking the flag.
	HasCacheRead  bool `json:"-"`
	HasCacheWrite bool `json:"-"`
	// Raw is the published expression this quote came from (billing_expr),
	// empty when the site published plain numbers.
	Raw string `json:"raw,omitempty"`
	// Unparsed means the site published a price we could not normalize. The
	// numeric fields are then meaningless: show Raw instead of a number.
	Unparsed      bool             `json:"unparsed,omitempty"`
	Unit          domain.QuoteUnit `json:"unit"`
	QuoteID       int64            `json:"quote_id,omitempty"`
	Status        string           `json:"status,omitempty"`
	UpstreamGroup string           `json:"upstream_group,omitempty"`
}

// BillingCoefs are the per-1M USD coefficients of a tiered billing expression.
type BillingCoefs struct {
	Prompt     float64
	Completion float64
	CacheRead  float64
	CacheWrite float64
	// HasCacheRead / HasCacheWrite record whether the expression named the
	// variable at all: an expression without cr does not price cache reads, so a
	// zero coefficient must not be mistaken for a published free price.
	HasCacheRead  bool
	HasCacheWrite bool
}

// billingVariables maps the expression's variable names to their slot. These
// are the names New-API's tiered-pricing DSL uses: p = prompt, c = completion,
// cr = cache read, cw = cache write.
var billingVariables = map[string]int{
	"p":  0,
	"c":  1,
	"cr": 2,
	"cw": 3,
}

// ParseBillingExpr parses the single-tier additive form of a New-API billing
// expression, e.g.
//
//	tier("base", p * 0.15 + c * 0.5 + cr * 0.03)
//
// into per-1M USD coefficients. The coefficients are already USD per 1M tokens:
// verified against an independent public aggregator's published price for the
// same site and model (0.15 / 0.5 / 0.03), and against the variables' meaning
// (prompt / completion / cache-read).
//
// Anything else — multiple tiers, unknown variables, arithmetic we do not
// model — returns false, and the caller must surface the raw expression instead
// of inventing a number.
func ParseBillingExpr(raw string) (BillingCoefs, bool) {
	expr := strings.TrimSpace(raw)
	if expr == "" {
		return BillingCoefs{}, false
	}
	body, ok := singleTierBody(expr)
	if !ok {
		return BillingCoefs{}, false
	}
	// Any parenthesized sub-expression (a nested call, a multiplication by a
	// grouped sum) is outside the grammar we can evaluate honestly.
	if strings.Contains(body, "(") {
		return BillingCoefs{}, false
	}
	var coefs BillingCoefs
	slots := [4]*float64{&coefs.Prompt, &coefs.Completion, &coefs.CacheRead, &coefs.CacheWrite}
	found := false
	for _, term := range strings.Split(body, "+") {
		variable, number, ok := parseBillingTerm(term)
		if !ok {
			return BillingCoefs{}, false
		}
		slot, known := billingVariables[variable]
		if !known {
			return BillingCoefs{}, false
		}
		*slots[slot] += number
		switch slot {
		case 2:
			coefs.HasCacheRead = true
		case 3:
			coefs.HasCacheWrite = true
		}
		found = true
	}
	if !found {
		return BillingCoefs{}, false
	}
	return coefs, true
}

// singleTierBody returns the body of `tier("<label>", <body>)`, requiring the
// whole expression to be exactly that one call.
func singleTierBody(expr string) (string, bool) {
	const prefix = "tier("
	if !strings.HasPrefix(expr, prefix) || !strings.HasSuffix(expr, ")") {
		return "", false
	}
	inner := expr[len(prefix) : len(expr)-1]
	comma := strings.Index(inner, ",")
	if comma < 0 {
		return "", false
	}
	label := strings.TrimSpace(inner[:comma])
	if len(label) < 2 || label[0] != '"' || label[len(label)-1] != '"' {
		return "", false
	}
	return strings.TrimSpace(inner[comma+1:]), true
}

// parseBillingTerm parses `<var> * <number>` or `<number> * <var>`.
func parseBillingTerm(term string) (string, float64, bool) {
	parts := strings.Split(term, "*")
	if len(parts) != 2 {
		return "", 0, false
	}
	left, right := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if value, err := strconv.ParseFloat(left, 64); err == nil {
		return right, value, true
	}
	if value, err := strconv.ParseFloat(right, 64); err == nil {
		return left, value, true
	}
	return "", 0, false
}

// NormalizePrice converts one published price into the gateway's storage units.
//
// The order matters and mirrors what New-API itself bills:
//  1. a per-call declaration (quota_type=1, or the parser's legacy
//     model_price-without-ratio heuristic) — a residual model_ratio or a stale
//     direct quote beside it must not pull the row into token billing;
//  2. a tiered expression, when the site publishes one — it is the authoritative
//     price there, and the plain ratios beside it are placeholders (measured:
//     every model of one such site carries model_ratio 37.5 while the real
//     prices differ by model);
//  3. a direct USD-per-1M quote;
//  4. a token ratio: ratio × 1e6 / quota_per_unit × group_ratio;
//  5. a legacy quota-per-1M quote: quota_per_1M / quota_per_unit;
//  6. a per-call price: model_price × group_ratio.
//
// Returns ok=false when the item carries no price at all, when an explicitly
// token-declared row has no token price to offer, or when the declared
// completion_ratio is not a usable price (negative / NaN / infinite).
func NormalizePrice(item ModelPrice, quotaPerUnit int64) (PriceQuote, bool) {
	groupRatio := item.GroupRatio
	if groupRatio <= 0 && len(item.GroupRatios) == 0 {
		groupRatio = 1
	}
	quote := PriceQuote{Model: item.Model, Currency: strings.TrimSpace(item.Currency), GroupRatio: groupRatio, Status: "calculable", UpstreamGroup: item.UpstreamGroup}
	if item.UnknownGroup {
		quote.Status = "unknown_group"
	}

	// A declared per-call row stays fixed even with a placeholder ratio or a
	// direct quote beside it; only a missing model_price lets it fall through.
	if declaredFixed(item) && item.ModelPrice > 0 {
		quote.Mode = "fixed"
		quote.PerRequest = item.ModelPrice * groupRatio
		return quote, true
	}

	if expr := strings.TrimSpace(item.BillingExpr); expr != "" {
		quote.Mode = "token"
		quote.Raw = expr
		if coefs, parsed := ParseBillingExpr(expr); parsed {
			quote.InputPerMillion = coefs.Prompt * groupRatio
			quote.OutputPerMillion = coefs.Completion * groupRatio
			quote.CacheReadPerMillion = coefs.CacheRead * groupRatio
			quote.CacheWritePerMillion = coefs.CacheWrite * groupRatio
			quote.HasCacheRead = coefs.HasCacheRead
			quote.HasCacheWrite = coefs.HasCacheWrite
			return quote, true
		}
		quote.Unparsed = true
		return quote, true
	}

	if quotaPerUnit <= 0 {
		quotaPerUnit = 500000
	}
	unit := float64(quotaPerUnit)
	switch {
	case item.TokenUSD != nil && item.TokenUSD.Input > 0:
		quote.Mode = "token"
		quote.Currency = "USD"
		quote.InputPerMillion = item.TokenUSD.Input
		quote.OutputPerMillion = item.TokenUSD.Output
		quote.CacheReadPerMillion = item.TokenUSD.CacheRead
		quote.CacheWritePerMillion = item.TokenUSD.CacheWrite
		quote.HasCacheRead = item.TokenUSD.HasCacheRead
		quote.HasCacheWrite = item.TokenUSD.HasCacheWrite
	case item.Ratio > 0:
		quote.Mode = "token"
		quote.InputPerMillion = item.Ratio * 1_000_000 / unit * groupRatio
		multiplier, usable := completionMultiplier(item)
		if !usable {
			return quote, false
		}
		quote.OutputPerMillion = quote.InputPerMillion * multiplier
		quote.CacheReadPerMillion, quote.HasCacheRead = cacheRatioPrice(item.CacheRatio, item.HasCacheRatio, quote.InputPerMillion)
		quote.CacheWritePerMillion, quote.HasCacheWrite = cacheRatioPrice(item.CacheWriteRatio, item.HasCacheWriteRatio, quote.InputPerMillion)
	case item.QuotaPer1M > 0:
		quote.Mode = "token"
		quote.InputPerMillion = item.QuotaPer1M / unit
		multiplier, usable := completionMultiplier(item)
		if !usable {
			return quote, false
		}
		quote.OutputPerMillion = quote.InputPerMillion * multiplier
	case item.ModelPrice > 0:
		// A row that says token billing (quota_type=0) must not be reclassified
		// as per-call from a residual model_price.
		if declaredToken(item) {
			return quote, false
		}
		quote.Mode = "fixed"
		quote.PerRequest = item.ModelPrice * groupRatio
	default:
		return quote, false
	}
	return quote, true
}

// declaredFixed reports whether the row declares per-call billing: the parser
// sets Mode="fixed" from quota_type=1 or the legacy model_price heuristic, and
// a hand-built row may set QuotaType directly.
func declaredFixed(item ModelPrice) bool {
	return item.Mode == "fixed" || item.QuotaType == 1
}

// declaredToken reports an explicit quota_type=0 declaration. Such a row must
// not fall back to a per-call price just because a stale model_price remains.
func declaredToken(item ModelPrice) bool {
	return item.HasQuotaType && item.QuotaType == 0
}

// cacheRatioPrice turns a New-API cache ratio (a multiple of the input price)
// into a per-1M amount. The presence flag rides through: a missing cache price
// is not a free one, so a zero value without presence stays unset. A negative,
// NaN, or infinite ratio is refused rather than dressed up as a real price.
func cacheRatioPrice(ratio float64, present bool, inputPerMillion float64) (float64, bool) {
	if !present || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
		return 0, false
	}
	return inputPerMillion * ratio, true
}

// completionMultiplier returns the token output multiplier and whether it is
// usable. A missing completion_ratio means "same as input" (1); an explicit 0
// means a free output (0). A negative, NaN, or infinite ratio is not a price at
// all and is refused rather than quietly promoted to 1 (which would invent a
// valid positive output price).
func completionMultiplier(item ModelPrice) (float64, bool) {
	if item.HasCompletionRatio {
		switch {
		case math.IsNaN(item.CompletionRatio), math.IsInf(item.CompletionRatio, 0), item.CompletionRatio < 0:
			return 0, false
		default:
			return item.CompletionRatio, true
		}
	}
	// Rows without a presence flag (hand-built callers, legacy data) keep the
	// historical rule: zero or negative means "same as input".
	if item.CompletionRatio <= 0 {
		return 1, true
	}
	return item.CompletionRatio, true
}
