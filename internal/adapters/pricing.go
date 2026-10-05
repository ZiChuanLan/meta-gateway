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
	"strconv"
	"strings"
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
	PerRequest          float64 `json:"per_request,omitempty"`
	GroupRatio          float64 `json:"group_ratio,omitempty"`
	// Raw is the published expression this quote came from (billing_expr),
	// empty when the site published plain numbers.
	Raw string `json:"raw,omitempty"`
	// Unparsed means the site published a price we could not normalize. The
	// numeric fields are then meaningless: show Raw instead of a number.
	Unparsed bool `json:"unparsed,omitempty"`
}

// BillingCoefs are the per-1M USD coefficients of a tiered billing expression.
type BillingCoefs struct {
	Prompt     float64
	Completion float64
	CacheRead  float64
	CacheWrite float64
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
//  1. a tiered expression, when the site publishes one — it is the authoritative
//     price there, and the plain ratios beside it are placeholders (measured:
//     every model of one such site carries model_ratio 37.5 while the real
//     prices differ by model);
//  2. a direct USD-per-1M quote;
//  3. a token ratio: ratio × 1e6 / quota_per_unit × group_ratio;
//  4. a legacy quota-per-1M quote: quota_per_1M / quota_per_unit;
//  5. a per-call price: model_price × group_ratio.
//
// Returns ok=false when the item carries no price at all.
func NormalizePrice(item ModelPrice, quotaPerUnit int64) (PriceQuote, bool) {
	groupRatio := item.GroupRatio
	if groupRatio <= 0 {
		groupRatio = 1
	}
	quote := PriceQuote{Model: item.Model, Currency: strings.TrimSpace(item.Currency), GroupRatio: groupRatio}

	if expr := strings.TrimSpace(item.BillingExpr); expr != "" {
		quote.Mode = "token"
		quote.Raw = expr
		if coefs, parsed := ParseBillingExpr(expr); parsed {
			quote.InputPerMillion = coefs.Prompt * groupRatio
			quote.OutputPerMillion = coefs.Completion * groupRatio
			quote.CacheReadPerMillion = coefs.CacheRead * groupRatio
			return quote, true
		}
		quote.Unparsed = true
		return quote, true
	}

	if quotaPerUnit <= 0 {
		quotaPerUnit = 500000
	}
	switch {
	case item.TokenUSD != nil && item.TokenUSD.Input > 0:
		quote.Mode = "token"
		quote.InputPerMillion = item.TokenUSD.Input
		quote.OutputPerMillion = item.TokenUSD.Output
		quote.CacheReadPerMillion = item.TokenUSD.CacheRead
	case item.Ratio > 0:
		quote.Mode = "token"
		quote.InputPerMillion = item.Ratio * 1_000_000 / float64(quotaPerUnit) * groupRatio
		quote.OutputPerMillion = quote.InputPerMillion * completionMultiplier(item.CompletionRatio)
	case item.QuotaPer1M > 0:
		quote.Mode = "token"
		quote.InputPerMillion = item.QuotaPer1M / float64(quotaPerUnit)
		quote.OutputPerMillion = quote.InputPerMillion * completionMultiplier(item.CompletionRatio)
	case item.ModelPrice > 0:
		quote.Mode = "fixed"
		quote.PerRequest = item.ModelPrice * groupRatio
	default:
		return quote, false
	}
	return quote, true
}

// completionMultiplier: a completion ratio of 0 means "same as prompt", which is
// how New-API treats an unset ratio.
func completionMultiplier(ratio float64) float64 {
	if ratio <= 0 {
		return 1
	}
	return ratio
}
