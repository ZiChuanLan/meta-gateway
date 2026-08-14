/** Compact token/request counts: 1.2M / 34.5k / 812. */
export function formatTokens(n: number) {
	if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
	if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
	return String(n);
}

/** Compact dollar rendering: $1.2k / $3.50 / $0.000123. */
export function formatCost(value: number) {
	if (value >= 1000) return `$${value.toFixed(0)}`;
	if (value >= 1) return `$${value.toFixed(2)}`;
	if (value >= 0.01) return `$${value.toFixed(4)}`;
	if (value === 0) return "$0.00";
	return `$${value.toFixed(6)}`;
}

/**
 * Per-component log cost: cache-read tokens are billed at their own unit
 * price when configured, otherwise they fall back to the prompt price.
 * Returns the USD cost or null when no pricing applies.
 */
export function logCostUsd(input: {
	promptTokens: number;
	completionTokens: number;
	cacheReadTokens: number;
	pricePromptPer1k?: number;
	priceCompletionPer1k?: number;
	priceCachePer1k?: number;
}): number | null {
	const promptPrice = input.pricePromptPer1k ?? 0;
	const completionPrice = input.priceCompletionPer1k ?? 0;
	if (promptPrice <= 0 && completionPrice <= 0) return null;
	const cache = Math.min(Math.max(0, input.cacheReadTokens ?? 0), Math.max(0, input.promptTokens ?? 0));
	const prompt = Math.max(0, input.promptTokens ?? 0);
	const uncached = Math.max(0, prompt - cache);
	const cachePrice = (input.priceCachePer1k ?? 0) > 0 ? input.priceCachePer1k! : promptPrice;
	const total =
		(uncached / 1000) * promptPrice +
		(cache / 1000) * cachePrice +
		((input.completionTokens ?? 0) / 1000) * completionPrice;
	return total > 0 ? total : null;
}
