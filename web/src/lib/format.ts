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
