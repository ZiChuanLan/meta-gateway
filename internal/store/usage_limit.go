package store

// The per-channel usage budget.
//
// A channel is an account, and an account has a finite amount of money behind
// it. The operator knows how much ("this key came with $10"); the gateway is the
// only place that knows how much has been spent. So the budget lives on the
// channel, is measured in the same two units the ledger already records (cost
// and tokens), and 0 means "no limit" in either — matching the key / group /
// team quota columns.
//
// Reaching a limit parks the channel (status auto_disabled) and records which
// limit came due. It is not a failure: the upstream is fine, the operator's own
// decision simply came due, and the console says so instead of showing a
// connectivity error. Raising or clearing the limit releases the channel again.

// ExceedsUsageLimit reports whether an account has reached one of its budgets.
// Reaching means >= : "stop at $10" is satisfied by having spent $10.
func ExceedsUsageLimit(usedCost float64, usedTokens int64, limitCost float64, limitTokens int64) bool {
	if limitCost > 0 && usedCost >= limitCost {
		return true
	}
	return limitTokens > 0 && usedTokens >= limitTokens
}

// UsageLimitHit names the budgets an account has reached: "cost", "tokens", or
// "cost,tokens". It is stored as-is and phrased by the console, so the row does
// not have to carry a sentence in one language.
func UsageLimitHit(usedCost float64, usedTokens int64, limitCost float64, limitTokens int64) string {
	hit := ""
	if limitCost > 0 && usedCost >= limitCost {
		hit = "cost"
	}
	if limitTokens > 0 && usedTokens >= limitTokens {
		if hit != "" {
			return "cost,tokens"
		}
		return "tokens"
	}
	return hit
}
