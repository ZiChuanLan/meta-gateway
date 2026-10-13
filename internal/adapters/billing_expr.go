package adapters

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// BillingExpression is a published tiered billing expression read into the
// gateway's own vocabulary: per-1M coefficients, an optional ladder of rungs
// chosen by prompt size, and an optional set of time windows.
//
// This is a reader, not an engine. The upstream hands us a string, and the one
// thing this file must never do is evaluate it — the expression comes from
// somebody else's server, and the gateway's answer to it is to translate the
// shapes it can recognise into structures the billing path already bills with
// (domain.PriceTier, domain.PriceWindow). Anything it cannot map exactly is
// refused, so the caller shows the raw text instead of a price nobody measured.
type BillingExpression struct {
	Coefs    BillingCoefs
	Tiers    []domain.PriceTier
	Windows  []domain.PriceWindow
	Timezone string
}

// ParseBillingExpression reads one published expression.
//
// Recognised shapes, both measured on real sites:
//
//	tier("base", p * 1.5 + c * 4.5 + cr * 0.05)
//	tier("base", …) * (hour("UTC") >= 1 && hour("UTC") < 4 ? 2 : 1)
//	len <= 272000 ? tier("standard", …) : tier("long_context", …)
//
// Everything else — arithmetic beyond the additive term list, a window whose
// multiplier is not the true branch of a `? … : 1`, several tiers added
// together — returns false. The caller must keep the raw text for those.
func ParseBillingExpression(raw string) (BillingExpression, bool) {
	expr := strings.TrimSpace(raw)
	if expr == "" {
		return BillingExpression{}, false
	}
	if parsed, ok := parseLadderExpression(expr); ok {
		return parsed, true
	}
	return parseProductExpression(expr)
}

// parseProductExpression reads `[tier(…)] * (window) * (window) …`, which is how
// a site applies peak-hour multipliers to a single-tier base price.
func parseProductExpression(expr string) (BillingExpression, bool) {
	factors := splitTop(expr, '*')
	if len(factors) == 0 {
		return BillingExpression{}, false
	}
	var out BillingExpression
	timezone := ""
	sawTier := false
	for _, factor := range factors {
		factor = strings.TrimSpace(factor)
		if body, ok := unwrapParens(factor); ok {
			// A window factor always arrives parenthesised: `* (hour(…) ? 2 : 1)`.
			if window, zone, ok := parseWindowFactor(body); ok {
				out.Windows = append(out.Windows, window)
				if timezone != "" && timezone != zone {
					return BillingExpression{}, false
				}
				timezone = zone
				continue
			}
			// The base may be wrapped in one redundant pair.
			factor = body
		}
		if sawTier {
			return BillingExpression{}, false
		}
		coefs, ok := parseTierCall(factor)
		if !ok {
			return BillingExpression{}, false
		}
		out.Coefs = coefs
		sawTier = true
	}
	if !sawTier {
		return BillingExpression{}, false
	}
	// Two overlapping windows would multiply on the site and not in the
	// gateway, which takes the first match. Refusing keeps a number we cannot
	// bill identically out of the catalogue.
	if windowsOverlap(out.Windows) {
		return BillingExpression{}, false
	}
	if len(out.Windows) > 0 {
		if err := domain.ValidatePriceWindows(out.Windows); err != nil {
			return BillingExpression{}, false
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return BillingExpression{}, false
		}
		out.Timezone = timezone
	}
	return out, true
}

// parseLadderExpression reads the two-rung form
//
//	len <= 272000 ? tier("standard", …) : tier("long_context", …)
//
// onto the gateway's ladder: the narrow branch keeps the ceiling, the other
// branch is the open-ended rung.
func parseLadderExpression(expr string) (BillingExpression, bool) {
	question := indexTop(expr, '?')
	if question < 0 {
		return BillingExpression{}, false
	}
	condition := strings.TrimSpace(expr[:question])
	rest := strings.TrimSpace(expr[question+1:])
	colon := indexTop(rest, ':')
	if colon < 0 {
		return BillingExpression{}, false
	}
	yes := strings.TrimSpace(rest[:colon])
	no := strings.TrimSpace(rest[colon+1:])
	// `len <= <ceiling>` and its strict variant; a threshold on the wrong side
	// swaps which branch is the narrow one.
	variable, op, threshold, ok := parseLengthCondition(condition)
	if !ok || variable != "len" {
		return BillingExpression{}, false
	}
	narrow, wide := yes, no
	switch op {
	case "<=":
	case "<":
		threshold--
	case ">":
		narrow, wide = no, yes
	case ">=":
		narrow, wide = no, yes
		threshold--
	default:
		return BillingExpression{}, false
	}
	if threshold <= 0 {
		return BillingExpression{}, false
	}
	narrowCoefs, ok := parseTierCall(narrow)
	if !ok {
		return BillingExpression{}, false
	}
	wideCoefs, ok := parseTierCall(wide)
	if !ok {
		return BillingExpression{}, false
	}
	// A rung has one cache price (the read side). An expression that also prices
	// cache writes would lose that amount on the way in, so it is refused rather
	// than quietly dropped.
	if narrowCoefs.HasCacheWrite || wideCoefs.HasCacheWrite {
		return BillingExpression{}, false
	}
	tiers := []domain.PriceTier{
		{
			MaxPromptTokens: threshold,
			Prompt:          narrowCoefs.Prompt,
			Completion:      narrowCoefs.Completion,
			Cache:           narrowCoefs.CacheRead,
		},
		{
			Prompt:     wideCoefs.Prompt,
			Completion: wideCoefs.Completion,
			Cache:      wideCoefs.CacheRead,
		},
	}
	if err := domain.ValidatePriceTiers(tiers); err != nil {
		return BillingExpression{}, false
	}
	return BillingExpression{Coefs: narrowCoefs, Tiers: tiers}, true
}

// parseLengthCondition reads `len <= 272000`.
func parseLengthCondition(condition string) (string, string, int, bool) {
	for _, op := range []string{"<=", ">=", "<", ">"} {
		if index := strings.Index(condition, op); index > 0 {
			name := strings.TrimSpace(condition[:index])
			value := strings.TrimSpace(condition[index+len(op):])
			number, err := strconv.ParseFloat(value, 64)
			if err != nil || number != math.Trunc(number) || number <= 0 || number > 1e9 {
				return "", "", 0, false
			}
			return name, op, int(number), true
		}
	}
	return "", "", 0, false
}

// parseWindowFactor reads the inside of a window factor:
//
//	hour("UTC") >= 1 && hour("UTC") < 4 ? 2 : 1
//	hour("UTC") >= 22 || hour("UTC") < 6 ? 0.5 : 1
//
// The false branch must be exactly 1: any other value would describe an
// off-peak multiplier the gateway cannot express (a window is a multiplier
// around a base price, never a second base).
func parseWindowFactor(body string) (domain.PriceWindow, string, bool) {
	question := indexTop(body, '?')
	if question < 0 {
		return domain.PriceWindow{}, "", false
	}
	condition := strings.TrimSpace(body[:question])
	rest := strings.TrimSpace(body[question+1:])
	colon := indexTop(rest, ':')
	if colon < 0 {
		return domain.PriceWindow{}, "", false
	}
	multiplier, err := strconv.ParseFloat(strings.TrimSpace(rest[:colon]), 64)
	if err != nil || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier <= 0 {
		return domain.PriceWindow{}, "", false
	}
	fallback, err := strconv.ParseFloat(strings.TrimSpace(rest[colon+1:]), 64)
	if err != nil || fallback != 1 {
		return domain.PriceWindow{}, "", false
	}

	orParts := splitTop(condition, '|')
	andParts := []string{}
	if len(orParts) == 1 {
		andParts = splitTop(condition, '&')
	} else {
		// `a || b` describes one band that runs over midnight when one side is a
		// start bound and the other an end bound.
		for _, part := range orParts {
			part = strings.TrimSpace(part)
			if part == "" {
				return domain.PriceWindow{}, "", false
			}
			andParts = append(andParts, part)
		}
	}
	var start, end *int
	timezone := ""
	for _, part := range andParts {
		zone, op, hour, ok := parseHourCondition(strings.TrimSpace(part))
		if !ok {
			return domain.PriceWindow{}, "", false
		}
		if timezone != "" && timezone != zone {
			return domain.PriceWindow{}, "", false
		}
		timezone = zone
		switch op {
		case ">=", ">":
			value := hour
			if op == ">" {
				value++
			}
			if value > 23 {
				return domain.PriceWindow{}, "", false
			}
			if start == nil || value > *start {
				start = &value
			}
		default:
			value := hour
			if op == "<=" {
				value++
			}
			// `< 0` covers nothing at all: the site would never apply this
			// multiplier, and a gateway window that did would invent a price.
			if value <= 0 {
				return domain.PriceWindow{}, "", false
			}
			if value > 24 {
				return domain.PriceWindow{}, "", false
			}
			if end == nil || value < *end {
				end = &value
			}
		}
	}
	if start == nil && end == nil {
		return domain.PriceWindow{}, "", false
	}
	window := domain.PriceWindow{Multiplier: multiplier, ToHour: 23}
	switch {
	case start != nil && end != nil:
		// Our model reads `from >= to` as a band running over midnight, while
		// `a && b` on one hour is empty on the site. Only the empty case has to
		// be refused; the OR form above is what a real night band uses.
		if len(orParts) == 1 && *start >= *end {
			return domain.PriceWindow{}, "", false
		}
		window.FromHour = *start
		window.ToHour = *end % 24
	case start != nil:
		// Open-ended: from the start bound to the end of the day. ToHour 0 with
		// a later start is exactly that; FromHour 0 with ToHour 0 is a whole day.
		window.FromHour = *start
		window.ToHour = 0
	default:
		window.FromHour = 0
		window.ToHour = *end % 24
	}
	return window, timezone, true
}

// parseHourCondition reads `hour("UTC") >= 1`.
func parseHourCondition(part string) (string, string, int, bool) {
	const prefix = "hour("
	if !strings.HasPrefix(part, prefix) {
		return "", "", 0, false
	}
	close := strings.Index(part, ")")
	if close < 0 {
		return "", "", 0, false
	}
	zone := strings.TrimSpace(part[len(prefix):close])
	if len(zone) < 2 || zone[0] != '"' || zone[len(zone)-1] != '"' {
		return "", "", 0, false
	}
	zone = strings.Trim(zone, `"`)
	rest := strings.TrimSpace(part[close+1:])
	for _, op := range []string{">=", "<=", ">", "<"} {
		if !strings.HasPrefix(rest, op) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(rest, op))
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || number != math.Trunc(number) || number < 0 || number > 24 {
			return "", "", 0, false
		}
		return zone, op, int(number), true
	}
	return "", "", 0, false
}

// parseTierCall reads `tier("<label>", p * 1.5 + c * 4.5 + cr * 0.05)`. The
// label is descriptive — the gateway prices by the coefficients.
func parseTierCall(expr string) (BillingCoefs, bool) {
	body, ok := singleTierBody(strings.TrimSpace(expr))
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

// unwrapParens strips one pair of parentheses when it wraps the whole string.
func unwrapParens(expr string) (string, bool) {
	if len(expr) < 2 || expr[0] != '(' || expr[len(expr)-1] != ')' {
		return "", false
	}
	depth := 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(expr)-1 {
				return "", false // the parens close early: not one wrapping pair
			}
		}
		if depth < 0 {
			return "", false
		}
	}
	if depth != 0 {
		return "", false
	}
	return strings.TrimSpace(expr[1 : len(expr)-1]), true
}

// splitTop splits on a separator that sits outside parentheses. `&&` and `||`
// arrive doubled; the caller passes one ampersand or one pipe and the second
// character is consumed with the first.
func splitTop(expr string, separator byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
		default:
			if expr[i] != separator || depth != 0 {
				continue
			}
			end := i
			if (separator == '&' || separator == '|') && i+1 < len(expr) && expr[i+1] == separator {
				i++
			}
			parts = append(parts, expr[start:end])
			start = i + 1
		}
	}
	parts = append(parts, expr[start:])
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// indexTop finds a separator outside parentheses.
func indexTop(expr string, separator byte) int {
	depth := 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
		default:
			if expr[i] == separator && depth == 0 {
				return i
			}
		}
	}
	return -1
}

// windowsOverlap reports whether two windows can both match the same hour. The
// site multiplies its factors; the gateway's scheduler takes the first match,
// so an overlap is a difference in money.
func windowsOverlap(windows []domain.PriceWindow) bool {
	for i := 0; i < len(windows); i++ {
		for j := i + 1; j < len(windows); j++ {
			for hour := 0; hour < 24; hour++ {
				if windows[i].CoversHour(hour) && windows[j].CoversHour(hour) {
					return true
				}
			}
		}
	}
	return false
}
