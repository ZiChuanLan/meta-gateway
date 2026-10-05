package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// This file is the price model's vocabulary: context-length ladders and
// time-of-day windows, plus the two pure functions the billing path uses to
// pick from them.
//
// It is deliberately separate from models.go — the shapes here are read on
// every relay, parsed from storage, and validated at save time, and keeping
// them together makes that one story instead of three.

// PriceTier is one rung of a context-length price ladder.
//
// Most providers charge more per token for a long prompt than a short one, and
// until now the gateway could only express a single price per layer. A ladder
// is a list of rungs; each rung covers requests up to its ceiling, and the
// first rung a request fits under prices it.
type PriceTier struct {
	// MaxPromptTokens is the inclusive ceiling this rung covers. 0 means
	// open-ended ("everything above the last explicit ceiling") and is only
	// meaningful on the final rung.
	MaxPromptTokens int     `json:"max_prompt_tokens"`
	Prompt          float64 `json:"prompt"`
	Completion      float64 `json:"completion"`
	Cache           float64 `json:"cache"`
	PerRequest      float64 `json:"per_request"`
}

// PriceWindow multiplies a layer's prices during a recurring time window.
//
// Peak/off-peak pricing is mostly about the multiplier, never the rung, so the
// two features compose: a long prompt at night is priced by its rung and then
// scaled by the window.
type PriceWindow struct {
	// Days are ISO weekday numbers (1 = Monday … 7 = Sunday); empty means every
	// day.
	Days []int `json:"days"`
	// FromHour/ToHour are hours in the gateway's local timezone, 0–23. An end
	// that is not after the start runs over midnight (22 → 6). Equal values mean
	// the whole day.
	FromHour   int     `json:"from_hour"`
	ToHour     int     `json:"to_hour"`
	Multiplier float64 `json:"multiplier"`
}

// priceLadderLimit / priceWindowLimit bound what one layer may carry. A ladder
// is a pricing device, not a spreadsheet; a runaway value would be read on
// every relay.
const (
	priceLadderLimit = 20
	priceWindowLimit = 20
	// maxPriceMultiplier keeps a typo from multiplying every bill by a
	// thousand. It matches the ratio ceiling the console enforces.
	maxPriceMultiplier = 1000.0
)

// ParsePriceTiers reads a layer's ladder. An empty value means "no ladder" and
// returns nil; anything else must be a JSON array of rungs.
func ParsePriceTiers(raw string) ([]PriceTier, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var tiers []PriceTier
	if err := json.Unmarshal([]byte(raw), &tiers); err != nil {
		return nil, fmt.Errorf("price tiers: %w", err)
	}
	return tiers, nil
}

// ParsePriceWindows reads a layer's schedule; an empty value means "no
// schedule".
func ParsePriceWindows(raw string) ([]PriceWindow, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var windows []PriceWindow
	if err := json.Unmarshal([]byte(raw), &windows); err != nil {
		return nil, fmt.Errorf("price schedule: %w", err)
	}
	return windows, nil
}

// EncodePriceTiers renders a ladder for storage; nil encodes as "".
func EncodePriceTiers(tiers []PriceTier) (string, error) {
	if len(tiers) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(tiers)
	if err != nil {
		return "", fmt.Errorf("encode price tiers: %w", err)
	}
	return string(raw), nil
}

// EncodePriceWindows renders a schedule for storage; nil encodes as "".
func EncodePriceWindows(windows []PriceWindow) (string, error) {
	if len(windows) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(windows)
	if err != nil {
		return "", fmt.Errorf("encode price schedule: %w", err)
	}
	return string(raw), nil
}

// PickPriceTier returns the rung that prices a request with promptTokens input
// tokens.
//
// Rungs are ordered by ceiling regardless of how the operator typed them, and a
// request above every explicit ceiling falls to the last rung rather than
// billing nothing: the dearest configured price is the honest answer for "more
// than we priced", and free would be a silent gift.
func PickPriceTier(tiers []PriceTier, promptTokens int) (PriceTier, bool) {
	if len(tiers) == 0 {
		return PriceTier{}, false
	}
	ordered := make([]PriceTier, len(tiers))
	copy(ordered, tiers)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i].MaxPromptTokens, ordered[j].MaxPromptTokens
		// The open-ended rung always sorts last: it is the top of the ladder.
		if left == 0 {
			return false
		}
		if right == 0 {
			return true
		}
		return left < right
	})
	for _, tier := range ordered {
		if tier.MaxPromptTokens == 0 || promptTokens <= tier.MaxPromptTokens {
			return tier, true
		}
	}
	return ordered[len(ordered)-1], true
}

// ScheduleMultiplier returns the multiplier in force at `at`, or 1 when no
// window matches.
//
// The first matching window wins rather than multiplying several together: an
// operator who writes overlapping windows gets the one they wrote first
// instead of a product that compounds into an accidental surcharge.
func ScheduleMultiplier(windows []PriceWindow, at time.Time) float64 {
	weekday := int(at.Weekday())
	if weekday == 0 {
		weekday = 7 // time.Weekday is Sunday-first; ISO numbers start on Monday.
	}
	hour := at.Hour()
	for _, window := range windows {
		if !window.coversDay(weekday) || !window.coversHour(hour) {
			continue
		}
		return window.Multiplier
	}
	return 1
}

func (w PriceWindow) coversDay(weekday int) bool {
	if len(w.Days) == 0 {
		return true
	}
	for _, day := range w.Days {
		if day == weekday {
			return true
		}
	}
	return false
}

func (w PriceWindow) coversHour(hour int) bool {
	if w.FromHour == w.ToHour {
		return true // the whole day
	}
	if w.FromHour < w.ToHour {
		return hour >= w.FromHour && hour < w.ToHour
	}
	// Over midnight: 22 → 6 covers 22, 23, 0 … 5.
	return hour >= w.FromHour || hour < w.ToHour
}

// ValidatePriceTiers checks an operator-supplied ladder before it is stored, so
// the billing path never has to guess what a malformed rung meant.
func ValidatePriceTiers(tiers []PriceTier) error {
	if len(tiers) == 0 {
		return nil
	}
	if len(tiers) > priceLadderLimit {
		return fmt.Errorf("at most %d price tiers", priceLadderLimit)
	}
	openEnded := 0
	seenCeilings := make(map[int]bool)
	for i, tier := range tiers {
		if tier.MaxPromptTokens < 0 {
			return fmt.Errorf("tier %d: max_prompt_tokens must be >= 0", i+1)
		}
		if tier.MaxPromptTokens > 0 && seenCeilings[tier.MaxPromptTokens] {
			return fmt.Errorf("tier %d: duplicate input ceiling %d", i+1, tier.MaxPromptTokens)
		}
		seenCeilings[tier.MaxPromptTokens] = true
		if tier.MaxPromptTokens == 0 {
			openEnded++
		}
		for name, value := range map[string]float64{
			"prompt":      tier.Prompt,
			"completion":  tier.Completion,
			"cache":       tier.Cache,
			"per_request": tier.PerRequest,
		} {
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("tier %d: %s must be >= 0", i+1, name)
			}
		}
	}
	if openEnded > 1 {
		return fmt.Errorf("only one tier may have max_prompt_tokens = 0 (the open-ended rung)")
	}
	return nil
}

// ValidatePriceWindows checks an operator-supplied schedule before it is stored.
func ValidatePriceWindows(windows []PriceWindow) error {
	if len(windows) == 0 {
		return nil
	}
	if len(windows) > priceWindowLimit {
		return fmt.Errorf("at most %d price windows", priceWindowLimit)
	}
	for i, window := range windows {
		for _, day := range window.Days {
			if day < 1 || day > 7 {
				return fmt.Errorf("window %d: days use ISO numbers 1–7 (Monday–Sunday)", i+1)
			}
		}
		if window.FromHour < 0 || window.FromHour > 23 || window.ToHour < 0 || window.ToHour > 23 {
			return fmt.Errorf("window %d: hours must be between 0 and 23", i+1)
		}
		if window.Multiplier <= 0 || window.Multiplier > maxPriceMultiplier || math.IsNaN(window.Multiplier) || math.IsInf(window.Multiplier, 0) {
			return fmt.Errorf("window %d: multiplier must be greater than 0 and at most %g", i+1, maxPriceMultiplier)
		}
	}
	return nil
}

// PriceLayer is one resolved price layer — the flat per-1k prices together with
// the parsed ladder and schedule — and the answer to "does this layer price at
// all".
//
// It exists because "is this layer priced" is no longer a question the flat
// columns can answer: a member whose columns are all zero but whose ladder is
// set does price, and a caller that only checked the columns would bill the
// request as free.
type PriceLayer struct {
	Prompt     float64
	Completion float64
	Cache      float64
	PerRequest float64
	Tiers      []PriceTier
	Windows    []PriceWindow
}

// Priced reports whether this layer carries any price at all.
func (l PriceLayer) Priced() bool {
	return l.Prompt > 0 || l.Completion > 0 || l.PerRequest > 0 || len(l.Tiers) > 0
}

// ResolvePriceLayer parses a layer's stored JSON into a usable value.
//
// A malformed ladder or schedule is reported AND dropped, never fatal: the
// relay must not stall on one bad row, and the fallback (flat prices, no
// windows) is exactly what the layer billed before the operator added the
// value that broke. Save-time validation is what keeps malformed values out of
// storage in the first place; this is the belt to that braces.
func ResolvePriceLayer(prompt, completion, cache, perRequest float64, tiersJSON, scheduleJSON string) (PriceLayer, error) {
	layer := PriceLayer{Prompt: prompt, Completion: completion, Cache: cache, PerRequest: perRequest}
	var problems []string
	if tiers, err := ParsePriceTiers(tiersJSON); err != nil {
		problems = append(problems, err.Error())
	} else {
		layer.Tiers = tiers
	}
	if windows, err := ParsePriceWindows(scheduleJSON); err != nil {
		problems = append(problems, err.Error())
	} else {
		layer.Windows = windows
	}
	if len(problems) > 0 {
		return layer, fmt.Errorf("price layer: %s", strings.Join(problems, "; "))
	}
	return layer, nil
}

// EffectivePrices picks the prices in force for a request.
//
// The ladder wins over the flat columns when it is present, and the amount of
// input the request carried chooses the rung — the caller passes the same
// figure it bills as prompt tokens, so the rung and the charge can never
// disagree. An empty ladder returns the flat prices unchanged, so a layer that
// never configured tiers behaves as it always did.
func (l PriceLayer) EffectivePrices(promptTokens int) (prompt, completion, cache, perRequest float64) {
	if len(l.Tiers) == 0 {
		return l.Prompt, l.Completion, l.Cache, l.PerRequest
	}
	tier, ok := PickPriceTier(l.Tiers, promptTokens)
	if !ok {
		return l.Prompt, l.Completion, l.Cache, l.PerRequest
	}
	return tier.Prompt, tier.Completion, tier.Cache, tier.PerRequest
}

// TimeMultiplier returns the schedule multiplier in force at the given moment;
// 1 when the layer has no windows.
func (l PriceLayer) TimeMultiplier(at time.Time) float64 {
	if len(l.Windows) == 0 {
		return 1
	}
	return ScheduleMultiplier(l.Windows, at)
}
