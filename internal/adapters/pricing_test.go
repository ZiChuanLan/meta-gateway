package adapters

import "testing"

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
