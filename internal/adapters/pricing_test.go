package adapters

import (
	"math"
	"testing"
)

// TestParseBillingExprRealWorld uses expressions captured from a live site's
// public /api/pricing, together with the prices an independent aggregator
// publishes for the same site and models: the coefficients are USD per 1M
// tokens, and they are the only place that site's real prices live.
func TestParseBillingExprRealWorld(t *testing.T) {
	cases := []struct {
		expr       string
		prompt     float64
		completion float64
		cacheRead  float64
	}{
		{`tier("base", p * 0.15 + c * 0.5 + cr * 0.03)`, 0.15, 0.5, 0.03},
		{`tier("base", p * 3 + c * 15 + cr * 0.3)`, 3, 15, 0.3},
		{`tier("base", p * 1.4 + c * 4.4 + cr * 0.26)`, 1.4, 4.4, 0.26},
		{`tier( "base" , c * 4.4 + p * 1.4 )`, 1.4, 4.4, 0},
		{`tier("base", 0.5 * p + 1 * c)`, 0.5, 1, 0},
	}
	for _, testCase := range cases {
		coefs, ok := ParseBillingExpr(testCase.expr)
		if !ok {
			t.Errorf("ParseBillingExpr(%q) failed", testCase.expr)
			continue
		}
		if coefs.Prompt != testCase.prompt || coefs.Completion != testCase.completion || coefs.CacheRead != testCase.cacheRead {
			t.Errorf("ParseBillingExpr(%q) = p%v c%v cr%v, want p%v c%v cr%v",
				testCase.expr, coefs.Prompt, coefs.Completion, coefs.CacheRead,
				testCase.prompt, testCase.completion, testCase.cacheRead)
		}
	}
}

// A parser that guesses is worse than one that gives up: an unsupported
// expression must surface as "unparsed" so the UI can show the raw text.
func TestParseBillingExprRejectsWhatItCannotEvaluate(t *testing.T) {
	unsupported := []string{
		"",
		"p * 1",
		`p * 1 + c * 2`,
		`tier("base", p * 0.1) + tier("long", p * 0.2)`,
		`tier("base", p * (0.1 + 0.2))`,
		`tier("base", x * 2)`,
		`tier("base", p)`,
		`tier("base", p / 2)`,
		`tier("base", p * c)`,
		`tier("base", )`,
		`tier(p * 1)`,
	}
	for _, expr := range unsupported {
		if _, ok := ParseBillingExpr(expr); ok {
			t.Errorf("ParseBillingExpr(%q) accepted an expression it cannot evaluate", expr)
		}
	}
}

// The measured trap: a tiered site carries a placeholder model_ratio on every
// model, so the ratio formula produced the same nonsense price (75 USD/1M) for
// models that really cost 0.15 / 1.4 / 3. The expression must win.
func TestNormalizePricePrefersTieredExpressionOverPlaceholderRatio(t *testing.T) {
	item := ModelPrice{
		Model:           "glm-5.3-flash",
		Ratio:           37.5,
		CompletionRatio: 1,
		GroupRatio:      1,
		BillingExpr:     `tier("base", p * 0.15 + c * 0.5 + cr * 0.03)`,
	}
	quote, ok := NormalizePrice(item, 500000)
	if !ok {
		t.Fatal("NormalizePrice rejected a priced model")
	}
	if quote.Unparsed {
		t.Fatal("a parseable expression must not be reported as unparsed")
	}
	if quote.InputPerMillion != 0.15 || quote.OutputPerMillion != 0.5 || quote.CacheReadPerMillion != 0.03 {
		t.Fatalf("quote = %+v, want 0.15 / 0.5 / 0.03", quote)
	}
	if quote.InputPerMillion == 75 {
		t.Fatal("the placeholder ratio leaked into the price")
	}
}

func TestNormalizePriceUnparsedExpressionKeepsRawAndNoNumber(t *testing.T) {
	expr := `tier("base", p * 0.1) + tier("long", p * 0.2)`
	quote, ok := NormalizePrice(ModelPrice{Model: "m", Ratio: 37.5, BillingExpr: expr, GroupRatio: 1}, 500000)
	if !ok {
		t.Fatal("an unparsed price is still a price observation")
	}
	if !quote.Unparsed {
		t.Fatal("quote must be flagged unparsed")
	}
	if quote.InputPerMillion != 0 || quote.OutputPerMillion != 0 {
		t.Fatalf("unparsed quote carried numbers: %+v", quote)
	}
	if quote.Raw != expr {
		t.Fatalf("raw expression = %q, want %q", quote.Raw, expr)
	}
}

// quoteFromPricingJSON feeds one real /api/pricing row through the real parser
// and the real normalizer, so a regression test never re-implements either. It
// fails when the parser drops the row, which is exactly the lossy step these
// tests pin down.
func quoteFromPricingJSON(t *testing.T, row string) (ModelPrice, PriceQuote, bool) {
	t.Helper()
	body := []byte(`{"success":true,"group_ratio":{"default":1},"data":[` + row + `]}`)
	items, err := ParsePricingPayload(body)
	if err != nil {
		t.Fatalf("ParsePricingPayload(%s): %v", row, err)
	}
	if len(items) != 1 {
		t.Fatalf("parser returned %d rows for %s, want 1", len(items), row)
	}
	quote, ok := NormalizePrice(items[0], 500000)
	return items[0], quote, ok
}

func closeTo(got, want float64) bool { return math.Abs(got-want) <= 1e-9 }

// A row priced only by a billing expression (model_price and model_ratio both 0)
// must survive parsing: the expression is the authoritative price and the plain
// columns are placeholders. An unparseable expression is still an observation
// and must be kept with Unparsed set, not silently dropped.
func TestPricingKeepsExpressionOnlyRows(t *testing.T) {
	body := []byte(`{"success":true,"group_ratio":{"default":1},"data":[
		{"model_name":"expr-only","model_price":0,"model_ratio":0,"quota_type":0,
		 "billing_expr":"tier(\"base\", p * 0.15 + c * 0.5)"},
		{"model_name":"expr-unparsed","model_price":0,"model_ratio":0,"quota_type":0,
		 "billing_expr":"tier(\"base\", p * 0.1) + tier(\"long\", p * 0.2)"},
		{"model_name":"still-free","model_price":0,"model_ratio":0}
	]}`)
	items, err := ParsePricingPayload(body)
	if err != nil {
		t.Fatalf("ParsePricingPayload: %v", err)
	}
	byModel := map[string]ModelPrice{}
	for _, item := range items {
		byModel[item.Model] = item
	}
	if _, ok := byModel["still-free"]; ok {
		t.Fatal("a row with no signal at all must still be skipped")
	}
	exprOnly, ok := byModel["expr-only"]
	if !ok {
		t.Fatal("an expression-only row was dropped")
	}
	quote, ok := NormalizePrice(exprOnly, 500000)
	if !ok || quote.Unparsed || !closeTo(quote.InputPerMillion, 0.15) || !closeTo(quote.OutputPerMillion, 0.5) {
		t.Fatalf("expression-only quote = %+v ok=%v", quote, ok)
	}
	unparsed, ok := byModel["expr-unparsed"]
	if !ok {
		t.Fatal("an unparseable expression-only row was dropped")
	}
	quote, ok = NormalizePrice(unparsed, 500000)
	if !ok || !quote.Unparsed || quote.Raw == "" {
		t.Fatalf("unparsed expression quote = %+v ok=%v", quote, ok)
	}
	if quote.InputPerMillion != 0 || quote.OutputPerMillion != 0 {
		t.Fatalf("an unparsed expression carried invented numbers: %+v", quote)
	}
}

// A missing completion_ratio means "same as input" while an explicit 0 means a
// free output. Collapsing the two (the old `<= 0 → 1` rule) bills free models.
func TestNormalizePriceCompletionRatioPresence(t *testing.T) {
	missingItem, missing, ok := quoteFromPricingJSON(t, `{"model_name":"m","quota_type":0,"model_ratio":1}`)
	if !ok {
		t.Fatal("missing completion_ratio must not reject the quote")
	}
	if missingItem.HasCompletionRatio {
		t.Fatal("an absent completion_ratio must not be marked present")
	}
	if !closeTo(missing.InputPerMillion, 2) || !closeTo(missing.OutputPerMillion, 2) {
		t.Fatalf("missing completion_ratio quote = %+v, want input 2 output 2", missing)
	}

	zeroItem, zero, ok := quoteFromPricingJSON(t, `{"model_name":"m","quota_type":0,"model_ratio":1,"completion_ratio":0}`)
	if !ok {
		t.Fatal("an explicit zero completion_ratio must not reject the quote")
	}
	if !zeroItem.HasCompletionRatio {
		t.Fatal("an explicit zero completion_ratio must be marked present")
	}
	if !closeTo(zero.InputPerMillion, 2) || zero.OutputPerMillion != 0 {
		t.Fatalf("explicit-zero completion_ratio quote = %+v, want input 2 output 0", zero)
	}
}

// A row declared per-call (quota_type=1) is fixed even when a placeholder
// model_ratio sits beside it; the residual ratio must not pull it into token
// billing.
func TestNormalizePriceDeclaredFixedBeatsResidualRatio(t *testing.T) {
	item, quote, ok := quoteFromPricingJSON(t, `{"model_name":"m","quota_type":1,"model_ratio":1,"model_price":0.05}`)
	if !ok {
		t.Fatal("a declared per-call row must still produce a quote")
	}
	if item.Mode != "fixed" || quote.Mode != "fixed" || !closeTo(quote.PerRequest, 0.05) {
		t.Fatalf("declared-fixed quote = %+v (item mode %q), want fixed 0.05", quote, item.Mode)
	}
	if quote.InputPerMillion != 0 {
		t.Fatalf("the residual model_ratio leaked into a token price: %+v", quote)
	}
}

// quota_type=0 is an explicit token declaration: a residual model_price must not
// flip the row to fixed, and a row with a real ratio still prices as token.
func TestNormalizePriceDeclaredTokenKeepsResidualModelPrice(t *testing.T) {
	item, quote, ok := quoteFromPricingJSON(t, `{"model_name":"m","quota_type":0,"model_ratio":0,"model_price":0.05}`)
	if item.Mode != "token" {
		t.Fatalf("quota_type=0 row mode = %q, want token", item.Mode)
	}
	if ok && quote.Mode == "fixed" {
		t.Fatalf("a residual model_price misjudged an explicit token row as fixed: %+v", quote)
	}

	_, priced, ok := quoteFromPricingJSON(t, `{"model_name":"m2","quota_type":0,"model_ratio":1,"model_price":0.05}`)
	if !ok || priced.Mode != "token" || !closeTo(priced.InputPerMillion, 2) {
		t.Fatalf("quota_type=0 with a ratio must stay token at input 2: %+v ok=%v", priced, ok)
	}
}

// A row that never declared quota_type keeps the historical heuristic: a
// model_price with no ratio is per-call, a ratio is token.
func TestNormalizePriceMissingQuotaTypeLegacy(t *testing.T) {
	item, quote, ok := quoteFromPricingJSON(t, `{"model_name":"m","model_price":10,"model_ratio":0}`)
	if !ok || item.Mode != "fixed" || quote.Mode != "fixed" || !closeTo(quote.PerRequest, 10) {
		t.Fatalf("legacy per-call row = %+v (item mode %q) ok=%v", quote, item.Mode, ok)
	}
	item, quote, ok = quoteFromPricingJSON(t, `{"model_name":"m2","model_ratio":0.5}`)
	if !ok || item.Mode != "token" || quote.Mode != "token" || !closeTo(quote.InputPerMillion, 1) {
		t.Fatalf("legacy ratio row = %+v (item mode %q) ok=%v", quote, item.Mode, ok)
	}
}

// Cache prices are real dimensions: a ratio route scales them off the input
// price, a direct route copies them, and a missing cache price is not a free
// one (the presence flag is what tells them apart).
func TestNormalizePriceCacheDimensions(t *testing.T) {
	_, quote, ok := quoteFromPricingJSON(t, `{"model_name":"m","quota_type":0,"model_ratio":1,"cache_ratio":0.1,"cache_creation_ratio":1.25}`)
	if !ok || !quote.HasCacheRead || !quote.HasCacheWrite {
		t.Fatalf("cache ratios must be reported present: %+v ok=%v", quote, ok)
	}
	if !closeTo(quote.CacheReadPerMillion, 0.2) || !closeTo(quote.CacheWritePerMillion, 2.5) {
		t.Fatalf("ratio cache prices = %+v, want read 0.2 write 2.5", quote)
	}

	_, quote, _ = quoteFromPricingJSON(t, `{"model_name":"m","quota_type":0,"model_ratio":1,"cache_ratio":0}`)
	if !quote.HasCacheRead || quote.CacheReadPerMillion != 0 {
		t.Fatalf("an explicit zero cache_ratio must be present and free: %+v", quote)
	}
	if quote.HasCacheWrite {
		t.Fatalf("a missing cache_creation_ratio must not be marked present: %+v", quote)
	}

	_, quote, _ = quoteFromPricingJSON(t, `{"model_name":"m","quota_type":0,"model_ratio":1}`)
	if quote.HasCacheRead || quote.HasCacheWrite {
		t.Fatalf("missing cache prices must not be assumed present: %+v", quote)
	}

	_, quote, ok = quoteFromPricingJSON(t, `{"model_name":"m","token_price_usd_per_million":{"input":3,"output":12,"cache_read":0.3,"cache_write":3.75}}`)
	if !ok || !quote.HasCacheRead || !quote.HasCacheWrite {
		t.Fatalf("direct cache prices must be reported present: %+v ok=%v", quote, ok)
	}
	if !closeTo(quote.CacheReadPerMillion, 0.3) || !closeTo(quote.CacheWritePerMillion, 3.75) {
		t.Fatalf("direct cache prices = %+v, want read 0.3 write 3.75", quote)
	}

	_, quote, ok = quoteFromPricingJSON(t, `{"model_name":"m","billing_expr":"tier(\"base\", p * 0.15 + c * 0.5 + cw * 0.25)"}`)
	if !ok || !quote.HasCacheWrite || !closeTo(quote.CacheWritePerMillion, 0.25) {
		t.Fatalf("expression cache write = %+v ok=%v, want 0.25 present", quote, ok)
	}
	if quote.HasCacheRead {
		t.Fatalf("an expression without cr must not claim a cache read price: %+v", quote)
	}
}

// A corrupt completion_ratio (negative, NaN, or infinite) must not be promoted
// to a valid positive output price: the row is refused rather than mispriced.
func TestNormalizePriceRefusesUnusableCompletionRatio(t *testing.T) {
	if _, _, ok := quoteFromPricingJSON(t, `{"model_name":"m","quota_type":0,"model_ratio":1,"completion_ratio":-3}`); ok {
		t.Fatal("a negative completion_ratio must not yield a usable quote")
	}
	// JSON cannot express NaN/Inf, so those come from a hand-built row.
	for _, ratio := range []float64{math.NaN(), math.Inf(1)} {
		item := ModelPrice{Model: "m", Ratio: 1, CompletionRatio: ratio, HasCompletionRatio: true, GroupRatio: 1}
		if quote, ok := NormalizePrice(item, 500000); ok {
			t.Fatalf("completion_ratio %v yielded a usable quote: %+v", ratio, quote)
		}
	}
}

func TestNormalizePriceShapes(t *testing.T) {
	// Token ratio: ratio × 1e6 / quota_per_unit × group_ratio (New-API's own
	// formula; with the default 500000 this is ratio × 2).
	quote, ok := NormalizePrice(ModelPrice{Model: "m", Ratio: 1, CompletionRatio: 2, GroupRatio: 1}, 500000)
	if !ok || quote.InputPerMillion != 2 || quote.OutputPerMillion != 4 {
		t.Fatalf("ratio quote = %+v ok=%v, want 2 / 4", quote, ok)
	}
	// Group ratio multiplies.
	quote, _ = NormalizePrice(ModelPrice{Model: "m", Ratio: 1, GroupRatio: 2}, 500000)
	if quote.InputPerMillion != 4 {
		t.Fatalf("group ratio not applied: %+v", quote)
	}
	// Direct USD per 1M wins over ratios and is not multiplied again.
	quote, _ = NormalizePrice(ModelPrice{Model: "m", Ratio: 9, GroupRatio: 3, TokenUSD: &TokenUSDPerMillion{Input: 0.25, Output: 1, CacheRead: 0.05}}, 500000)
	if quote.InputPerMillion != 0.25 || quote.OutputPerMillion != 1 || quote.CacheReadPerMillion != 0.05 {
		t.Fatalf("direct-usd quote = %+v", quote)
	}
	// Legacy quota per 1M.
	quote, _ = NormalizePrice(ModelPrice{Model: "m", QuotaPer1M: 500000}, 500000)
	if quote.InputPerMillion != 1 {
		t.Fatalf("legacy quota quote = %+v, want 1", quote)
	}
	// Per-call fixed price.
	quote, _ = NormalizePrice(ModelPrice{Model: "m", ModelPrice: 0.02, GroupRatio: 2}, 500000)
	if quote.Mode != "fixed" || quote.PerRequest != 0.04 {
		t.Fatalf("fixed quote = %+v, want 0.04 per request", quote)
	}
	// Nothing priced at all.
	if _, ok := NormalizePrice(ModelPrice{Model: "m"}, 500000); ok {
		t.Fatal("an unpriced model must not produce a quote")
	}
}
