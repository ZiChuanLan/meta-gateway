package adapters

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// The expressions below are verbatim from sites the operator runs, which is the
// only fixture that matters: the reader exists to translate what they publish.
func TestParseBillingExpressionReadsTimeWindows(t *testing.T) {
	raw := `(tier("base", p * 1.5 + c * 4.5 + cr * 0.05)) * (hour("UTC") >= 1 && hour("UTC") < 4 ? 2 : 1) * (hour("UTC") >= 6 && hour("UTC") < 10 ? 2 : 1)`
	parsed, ok := ParseBillingExpression(raw)
	if !ok {
		t.Fatalf("refused a published expression: %s", raw)
	}
	if parsed.Coefs.Prompt != 1.5 || parsed.Coefs.Completion != 4.5 || parsed.Coefs.CacheRead != 0.05 {
		t.Fatalf("coefficients: %+v", parsed.Coefs)
	}
	if parsed.Timezone != "UTC" {
		t.Fatalf("timezone: %q", parsed.Timezone)
	}
	want := []domain.PriceWindow{
		{FromHour: 1, ToHour: 4, Multiplier: 2},
		{FromHour: 6, ToHour: 10, Multiplier: 2},
	}
	if len(parsed.Windows) != len(want) {
		t.Fatalf("windows: %+v", parsed.Windows)
	}
	for i, window := range parsed.Windows {
		if window.FromHour != want[i].FromHour || window.ToHour != want[i].ToHour || window.Multiplier != want[i].Multiplier {
			t.Fatalf("window %d = %+v, want %+v", i, window, want[i])
		}
	}
	// The band boundaries must be exclusive on the end hour, exactly like the
	// expression, or the two would disagree by one hour of surcharge.
	if parsed.Windows[0].CoversHour(4) || !parsed.Windows[0].CoversHour(3) {
		t.Fatal("window边界不符：01–04 必须是左闭右开")
	}
}

// A night band written with OR runs over midnight; the scheduler's own window
// means the same thing.
func TestParseBillingExpressionReadsOvernightBand(t *testing.T) {
	raw := `tier("base", p * 1) * (hour("Asia/Shanghai") >= 22 || hour("Asia/Shanghai") < 6 ? 0.5 : 1)`
	parsed, ok := ParseBillingExpression(raw)
	if !ok {
		t.Fatalf("refused %s", raw)
	}
	if parsed.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone: %q", parsed.Timezone)
	}
	if len(parsed.Windows) != 1 || parsed.Windows[0].FromHour != 22 || parsed.Windows[0].ToHour != 6 || parsed.Windows[0].Multiplier != 0.5 {
		t.Fatalf("windows: %+v", parsed.Windows)
	}
	if !parsed.Windows[0].CoversHour(23) || !parsed.Windows[0].CoversHour(3) || parsed.Windows[0].CoversHour(12) {
		t.Fatal("跨夜窗口匹配错")
	}
}

// A start-only condition covers the rest of the day.
func TestParseBillingExpressionReadsHalfOpenBand(t *testing.T) {
	parsed, ok := ParseBillingExpression(`tier("base", p * 1) * (hour("UTC") >= 9 ? 2 : 1)`)
	if !ok {
		t.Fatal("refused a start-only window")
	}
	if len(parsed.Windows) != 1 || !parsed.Windows[0].CoversHour(9) || !parsed.Windows[0].CoversHour(23) || parsed.Windows[0].CoversHour(8) {
		t.Fatalf("windows: %+v", parsed.Windows)
	}
	parsed, ok = ParseBillingExpression(`tier("base", p * 1) * (hour("UTC") <= 8 ? 2 : 1)`)
	if !ok {
		t.Fatal("refused an end-only window")
	}
	if len(parsed.Windows) != 1 || !parsed.Windows[0].CoversHour(0) || !parsed.Windows[0].CoversHour(8) || parsed.Windows[0].CoversHour(9) {
		t.Fatalf("windows: %+v", parsed.Windows)
	}
}

// The ladder form becomes the gateway's own ladder: the threshold is the rung's
// inclusive ceiling and the other branch is open-ended.
func TestParseBillingExpressionReadsLadder(t *testing.T) {
	raw := `len <= 272000 ? tier("standard", p * 2.5 + c * 15 + cr * 0.25) : tier("long_context", p * 2 + c * 15 + cr * 0.25)`
	parsed, ok := ParseBillingExpression(raw)
	if !ok {
		t.Fatalf("refused %s", raw)
	}
	if len(parsed.Tiers) != 2 {
		t.Fatalf("tiers: %+v", parsed.Tiers)
	}
	narrow, wide := parsed.Tiers[0], parsed.Tiers[1]
	if narrow.MaxPromptTokens != 272000 || narrow.Prompt != 2.5 || narrow.Completion != 15 || narrow.Cache != 0.25 {
		t.Fatalf("narrow rung: %+v", narrow)
	}
	if wide.MaxPromptTokens != 0 || wide.Prompt != 2 || wide.Completion != 15 {
		t.Fatalf("open rung: %+v", wide)
	}
	// The ladder the gateway bills must pick the same rung the site would.
	picked, ok := domain.PickPriceTier(parsed.Tiers, 272000)
	if !ok || picked.Prompt != 2.5 {
		t.Fatalf("at the ceiling: %+v", picked)
	}
	picked, _ = domain.PickPriceTier(parsed.Tiers, 272001)
	if picked.Prompt != 2 {
		t.Fatalf("one token past the ceiling: %+v", picked)
	}
}

// A threshold on the other side swaps which branch is the narrow one.
func TestParseBillingExpressionReadsInvertedLadder(t *testing.T) {
	raw := `len > 100000 ? tier("long", p * 2) : tier("standard", p * 2.5)`
	parsed, ok := ParseBillingExpression(raw)
	if !ok {
		t.Fatalf("refused %s", raw)
	}
	picked, _ := domain.PickPriceTier(parsed.Tiers, 100000)
	if picked.Prompt != 2.5 {
		t.Fatalf("at the ceiling: %+v", picked)
	}
	picked, _ = domain.PickPriceTier(parsed.Tiers, 100001)
	if picked.Prompt != 2 {
		t.Fatalf("above the ceiling: %+v", picked)
	}
}

// Everything the reader cannot map exactly stays unread: the console shows the
// text, and no price is invented from it.
func TestParseBillingExpressionRefusesWhatItCannotBill(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"off-peak multiplier", `tier("base", p * 1) * (hour("UTC") >= 1 && hour("UTC") < 4 ? 2 : 0.5)`},
		{"free window", `tier("base", p * 1) * (hour("UTC") >= 1 && hour("UTC") < 4 ? 2 : 0)`},
		{"overlapping windows", `tier("base", p * 1) * (hour("UTC") >= 1 && hour("UTC") < 6 ? 2 : 1) * (hour("UTC") >= 4 && hour("UTC") < 8 ? 3 : 1)`},
		{"impossible band", `tier("base", p * 1) * (hour("UTC") >= 10 && hour("UTC") < 3 ? 2 : 1)`},
		{"empty band", `tier("base", p * 1) * (hour("UTC") >= 3 && hour("UTC") < 3 ? 2 : 1)`},
		{"unknown variable", `tier("base", x * 2)`},
		{"unknown arithmetic", `tier("base", p * 1.5 + c * 4.5 + 0.1)`},
		{"two tiers added", `tier("a", p * 1) + tier("b", p * 2)`},
		{"scaled base", `tier("base", p * 1) * 2`},
		{"unknown timezone", `tier("base", p * 1) * (hour("Not/AZone") >= 1 ? 2 : 1)`},
		{"two timezones", `tier("base", p * 1) * (hour("UTC") >= 1 && hour("Asia/Shanghai") < 4 ? 2 : 1)`},
		{"ladder with cache writes", `len <= 100 ? tier("a", p * 1 + cw * 2) : tier("b", p * 2)`},
		{"zero threshold", `len <= 0 ? tier("a", p * 1) : tier("b", p * 2)`},
		{"three branches", `len <= 100 ? tier("a", p * 1) : len <= 200 ? tier("b", p * 2) : tier("c", p * 3)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if parsed, ok := ParseBillingExpression(tc.raw); ok {
				t.Fatalf("accepted %s as %+v", tc.raw, parsed)
			}
		})
	}
}

// The narrow entry point must not hand a base price to a caller that cannot
// carry the ladder or the windows around it.
func TestParseBillingExprRefusesStructuredExpressions(t *testing.T) {
	if _, ok := ParseBillingExpr(`tier("base", p * 1.5 + c * 4.5)`); !ok {
		t.Fatal("plain coefficients must still parse")
	}
	for _, raw := range []string{
		`tier("base", p * 1) * (hour("UTC") >= 9 ? 2 : 1)`,
		`len <= 100 ? tier("a", p * 1) : tier("b", p * 2)`,
	} {
		if coefs, ok := ParseBillingExpr(raw); ok {
			t.Fatalf("handed base coefficients %+v for %s", coefs, raw)
		}
	}
}

// NormalizePrice carries the structures onto the quote, and a ladder quote says
// so instead of pretending to be a single price.
func TestNormalizePriceCarriesWindowsAndLadder(t *testing.T) {
	quoted, ok := NormalizePrice(ModelPrice{
		Model:       "m",
		BillingExpr: `tier("base", p * 1.5) * (hour("UTC") >= 1 && hour("UTC") < 4 ? 2 : 1)`,
	}, 500000)
	if !ok || quoted.Unparsed {
		t.Fatalf("windowed expression: %+v ok=%v", quoted, ok)
	}
	if quoted.InputPerMillion != 1.5 || len(quoted.Windows) != 1 || quoted.Timezone != "UTC" {
		t.Fatalf("quote: %+v", quoted)
	}
	if quoted.Status != "calculable" {
		t.Fatalf("a windowed price is still calculable, got status %q", quoted.Status)
	}

	tiered, ok := NormalizePrice(ModelPrice{
		Model:       "m",
		BillingExpr: `len <= 1000 ? tier("a", p * 2.5) : tier("b", p * 2)`,
	}, 500000)
	if !ok || tiered.Unparsed {
		t.Fatalf("ladder expression: %+v ok=%v", tiered, ok)
	}
	if tiered.Status != "tiered" || len(tiered.Tiers) != 2 {
		t.Fatalf("quote: %+v", tiered)
	}
	// The flat amounts describe the shortest-prompt rung, so a reader that
	// ignores Tiers still sees a price the site publishes.
	if tiered.InputPerMillion != 2.5 {
		t.Fatalf("base amount = %v, want the short-prompt rung", tiered.InputPerMillion)
	}
}
