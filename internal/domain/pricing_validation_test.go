package domain

import (
	"math"
	"testing"
)

func TestPriceValidationRejectsAmbiguousAndNonFiniteSettings(t *testing.T) {
	for _, tiers := range [][]PriceTier{{{MaxPromptTokens: 1000, Prompt: 1}, {MaxPromptTokens: 1000, Prompt: 2}}, {{Prompt: math.NaN()}}, {{Completion: math.Inf(1)}}} {
		if ValidatePriceTiers(tiers) == nil {
			t.Fatalf("accepted ambiguous/invalid tier: %v", tiers)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if ValidatePriceWindows([]PriceWindow{{Multiplier: value}}) == nil {
			t.Fatal("accepted non-finite multiplier")
		}
	}
}
