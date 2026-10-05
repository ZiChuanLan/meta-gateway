package domain_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// The price ladder and the schedule are the two new inputs to every bill, so
// these tests pin the arithmetic rather than the plumbing: which rung a request
// falls in, which window applies, and what an operator is allowed to store.
//
// Times are built with time.Date in a fixed zone so "which weekday is this"
// does not depend on the machine running the suite.

func TestPickPriceTierChoosesByCeiling(t *testing.T) {
	ladder := []domain.PriceTier{
		{MaxPromptTokens: 0, Prompt: 3, Completion: 9}, // open ended
		{MaxPromptTokens: 200000, Prompt: 0.5, Completion: 1.5},
		{MaxPromptTokens: 32000, Prompt: 0.15, Completion: 0.6},
	}
	cases := []struct {
		name         string
		promptTokens int
		wantPrompt   float64
	}{
		{"well inside the small rung", 1, 0.15},
		{"exactly on the small ceiling", 32000, 0.15},
		{"one token past it", 32001, 0.5},
		{"exactly on the large ceiling", 200000, 0.5},
		{"past every explicit ceiling falls to the open rung", 200001, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tier, ok := domain.PickPriceTier(ladder, tc.promptTokens)
			if !ok {
				t.Fatal("no tier picked")
			}
			if tier.Prompt != tc.wantPrompt {
				t.Fatalf("prompt price = %v, want %v", tier.Prompt, tc.wantPrompt)
			}
		})
	}
}

// The operator may type the rungs in any order; the ladder is ordered by
// ceiling, not by position in the array.
func TestPickPriceTierIgnoresInputOrder(t *testing.T) {
	shuffled := []domain.PriceTier{
		{MaxPromptTokens: 200000, Prompt: 0.5},
		{MaxPromptTokens: 0, Prompt: 3},
		{MaxPromptTokens: 32000, Prompt: 0.15},
	}
	tier, _ := domain.PickPriceTier(shuffled, 1000)
	if tier.Prompt != 0.15 {
		t.Fatalf("1000 tokens priced at %v, want the 32k rung (0.15)", tier.Prompt)
	}
	tier, _ = domain.PickPriceTier(shuffled, 400000)
	if tier.Prompt != 3 {
		t.Fatalf("400k tokens priced at %v, want the open rung (3)", tier.Prompt)
	}
}

// A ladder with no open-ended rung still prices a request above every ceiling:
// the dearest rung applies. Billing zero would be a silent gift.
func TestPickPriceTierFallsBackToTheDearestRung(t *testing.T) {
	ladder := []domain.PriceTier{
		{MaxPromptTokens: 1000, Prompt: 0.1},
		{MaxPromptTokens: 8000, Prompt: 0.4},
	}
	tier, ok := domain.PickPriceTier(ladder, 900000)
	if !ok || tier.Prompt != 0.4 {
		t.Fatalf("tier = %+v ok=%v, want the 8k rung", tier, ok)
	}
}

// An empty ladder means "use the flat prices" — the pre-tier behaviour.
func TestEffectivePricesWithoutLadderKeepsFlatPrices(t *testing.T) {
	layer := domain.PriceLayer{Prompt: 1.5, Completion: 6, Cache: 0.3, PerRequest: 0.02}
	prompt, completion, cache, perRequest := layer.EffectivePrices(500000)
	if prompt != 1.5 || completion != 6 || cache != 0.3 || perRequest != 0.02 {
		t.Fatalf("flat prices changed: %v %v %v %v", prompt, completion, cache, perRequest)
	}
	if !layer.Priced() {
		t.Fatal("a layer with flat prices must count as priced")
	}
}

// A ladder alone makes a layer priced. This is the case the old "are the
// columns non-zero" test got wrong: the columns are all zero and the request
// would have been billed as free.
func TestLadderAloneMakesALayerPriced(t *testing.T) {
	layer := domain.PriceLayer{
		Tiers: []domain.PriceTier{{MaxPromptTokens: 0, Prompt: 2, Completion: 8}},
	}
	if !layer.Priced() {
		t.Fatal("a ladder-only layer must count as priced")
	}
	prompt, completion, _, _ := layer.EffectivePrices(10)
	if prompt != 2 || completion != 8 {
		t.Fatalf("ladder prices = %v/%v, want 2/8", prompt, completion)
	}
}

func TestScheduleMultiplierMatchesWindow(t *testing.T) {
	// A weekday night band that runs over midnight, plus a weekend discount.
	windows := []domain.PriceWindow{
		{Days: []int{1, 2, 3, 4, 5}, FromHour: 22, ToHour: 6, Multiplier: 0.5},
		{Days: []int{6, 7}, FromHour: 0, ToHour: 24 % 24, Multiplier: 0.7},
	}
	// Monday 23:00 — inside the night band.
	if got := domain.ScheduleMultiplier(windows, time.Date(2026, 10, 5, 23, 0, 0, 0, time.UTC)); got != 0.5 {
		t.Fatalf("monday 23:00 = %v, want 0.5", got)
	}
	// Tuesday 03:00 — the same band, after midnight.
	if got := domain.ScheduleMultiplier(windows, time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)); got != 0.5 {
		t.Fatalf("tuesday 03:00 = %v, want 0.5", got)
	}
	// Monday 12:00 — no window, full price.
	if got := domain.ScheduleMultiplier(windows, time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)); got != 1 {
		t.Fatalf("monday noon = %v, want 1", got)
	}
	// Saturday 12:00 — the whole-weekend window.
	if got := domain.ScheduleMultiplier(windows, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)); got != 0.7 {
		t.Fatalf("saturday noon = %v, want 0.7", got)
	}
	// Sunday 23:00 — weekend window covers the whole day, night band does not
	// (its days are weekdays).
	if got := domain.ScheduleMultiplier(windows, time.Date(2026, 10, 11, 23, 0, 0, 0, time.UTC)); got != 0.7 {
		t.Fatalf("sunday 23:00 = %v, want 0.7", got)
	}
}

// Equal hours mean "all day": the intuitive reading of 0 → 0.
func TestWindowWithEqualHoursCoversTheWholeDay(t *testing.T) {
	windows := []domain.PriceWindow{{FromHour: 9, ToHour: 9, Multiplier: 0.8}}
	if got := domain.ScheduleMultiplier(windows, time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)); got != 0.8 {
		t.Fatalf("3am = %v, want 0.8", got)
	}
	if got := domain.ScheduleMultiplier(windows, time.Date(2026, 10, 5, 21, 0, 0, 0, time.UTC)); got != 0.8 {
		t.Fatalf("9pm = %v, want 0.8", got)
	}
}

// No windows means no adjustment, and a layer without one multiplies by 1.
func TestTimeMultiplierWithoutWindowsIsOne(t *testing.T) {
	layer := domain.PriceLayer{}
	if got := layer.TimeMultiplier(time.Now()); got != 1 {
		t.Fatalf("multiplier = %v, want 1", got)
	}
}

func TestValidatePriceTiersRejectsBadLadders(t *testing.T) {
	cases := []struct {
		name  string
		tiers []domain.PriceTier
		ok    bool
	}{
		{"empty means no ladder", nil, true},
		{"one rung", []domain.PriceTier{{MaxPromptTokens: 8000, Prompt: 1}}, true},
		{"one open-ended rung", []domain.PriceTier{{Prompt: 1}}, true},
		{"two open-ended rungs", []domain.PriceTier{{Prompt: 1}, {Prompt: 2}}, false},
		{"negative ceiling", []domain.PriceTier{{MaxPromptTokens: -1}}, false},
		{"negative price", []domain.PriceTier{{MaxPromptTokens: 10, Prompt: -0.1}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domain.ValidatePriceTiers(tc.tiers)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected a rejection")
			}
		})
	}
}

func TestValidatePriceWindowsRejectsBadSchedules(t *testing.T) {
	cases := []struct {
		name    string
		windows []domain.PriceWindow
		ok      bool
	}{
		{"empty means no schedule", nil, true},
		{"typical night band", []domain.PriceWindow{{Days: []int{1, 2, 3, 4, 5}, FromHour: 22, ToHour: 6, Multiplier: 0.6}}, true},
		{"weekday zero is not ISO", []domain.PriceWindow{{Days: []int{0}, FromHour: 1, ToHour: 2, Multiplier: 1}}, false},
		{"hour out of range", []domain.PriceWindow{{FromHour: 24, ToHour: 3, Multiplier: 1}}, false},
		{"zero multiplier would make usage free", []domain.PriceWindow{{FromHour: 1, ToHour: 2, Multiplier: 0}}, false},
		{"absurd multiplier", []domain.PriceWindow{{FromHour: 1, ToHour: 2, Multiplier: 100000}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domain.ValidatePriceWindows(tc.windows)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected a rejection")
			}
		})
	}
}

// Round-tripping through the stored form must be lossless: this is what makes
// "validate then re-encode" safe at the HTTP boundary.
func TestPriceLayerJSONRoundTrip(t *testing.T) {
	tiers := []domain.PriceTier{{MaxPromptTokens: 32000, Prompt: 0.15, Completion: 0.6, Cache: 0.03}}
	windows := []domain.PriceWindow{{Days: []int{6, 7}, FromHour: 0, ToHour: 0, Multiplier: 0.5}}
	tiersJSON, err := domain.EncodePriceTiers(tiers)
	if err != nil {
		t.Fatalf("encode tiers: %v", err)
	}
	windowsJSON, err := domain.EncodePriceWindows(windows)
	if err != nil {
		t.Fatalf("encode windows: %v", err)
	}
	layer, err := domain.ResolvePriceLayer(0, 0, 0, 0, tiersJSON, windowsJSON)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(layer.Tiers) != 1 || layer.Tiers[0].Completion != 0.6 {
		t.Fatalf("tiers did not survive: %+v", layer.Tiers)
	}
	if len(layer.Windows) != 1 || layer.Windows[0].Multiplier != 0.5 {
		t.Fatalf("windows did not survive: %+v", layer.Windows)
	}
	// Empty strings encode back to empty.
	if encoded, err := domain.EncodePriceTiers(nil); err != nil || encoded != "" {
		t.Fatalf("nil encode = %q (%v), want empty", encoded, err)
	}
	if encoded, err := domain.EncodePriceWindows(nil); err != nil || encoded != "" {
		t.Fatalf("nil windows encode = %q (%v), want empty", encoded, err)
	}
}

// Malformed JSON must not lose the layer: the flat prices survive and the
// problem is reported for the log.
func TestResolvePriceLayerKeepsFlatPricesOnBadJSON(t *testing.T) {
	layer, err := domain.ResolvePriceLayer(1, 2, 0, 0, "{not json", "")
	if err == nil {
		t.Fatal("expected a parse error to be reported")
	}
	if layer.Prompt != 1 || layer.Completion != 2 {
		t.Fatalf("flat prices lost: %+v", layer)
	}
	if !layer.Priced() {
		t.Fatal("the layer still carries flat prices and must read as priced")
	}
	if len(layer.Tiers) != 0 {
		t.Fatalf("a broken ladder must not be applied: %+v", layer.Tiers)
	}
}
