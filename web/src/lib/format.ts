import { useSyncExternalStore } from "react";
/** Compact token/request counts: 1.2M / 34.5k / 812. */
export function formatTokens(n: number) {
	if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
	if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
	return String(n);
}

/**
 * The site's money presentation.
 *
 * Amounts travel through the app in the ledger's unit (USD — the same number
 * `usage_records.cost` holds). The console and the member app render them
 * through a symbol and a rate the operator sets once, so a site that prices in
 * yuan shows yuan everywhere without rewriting a single stored amount.
 *
 * It is a module-level value rather than a React context on purpose: `formatCost`
 * is called from plain helpers, table renderers and log formatters that are not
 * all inside one provider, and threading a context through them would be a
 * bigger change than the feature. The shells set it once when they load the
 * settings.
 */

let symbol = "$";
let rate = 1;
let currencySnapshot = { symbol, rate };
const currencyListeners = new Set<() => void>();
const subscribeCurrency = (listener: () => void) => { currencyListeners.add(listener); return () => { currencyListeners.delete(listener); }; };
/** Trigger dependent renderers when a display setting is loaded or changed. */
export function useCurrency() {
 return useSyncExternalStore(subscribeCurrency, () => currencySnapshot, () => DEFAULT_CURRENCY);
}

/** Defaults used until the shells have read the site settings. */
export const DEFAULT_CURRENCY = { symbol: "$", rate: 1 };

/** Reads the current presentation. */
export function currentCurrency() {
	return currencySnapshot;
}

/**
 * Applies the site's settings. An empty symbol or a non-positive rate falls
 * back to the defaults instead of printing nonsense: this value is written from
 * a server response, and a broken response must not be able to garble every
 * amount on the page.
 */
export function setCurrency(next: { symbol?: string; rate?: number } | null | undefined) {
	const nextSymbol = String(next?.symbol ?? "").trim();
	symbol = nextSymbol && nextSymbol.length <= 8 ? nextSymbol : DEFAULT_CURRENCY.symbol;
	const nextRate = Number(next?.rate ?? 1);
	rate = Number.isFinite(nextRate) && nextRate > 0 ? nextRate : 1;
 if (currencySnapshot.symbol !== symbol || currencySnapshot.rate !== rate) {
  currencySnapshot = { symbol, rate };
  currencyListeners.forEach((listener) => listener());
 }
}

/** Converts a stored amount into the display currency. */
export function displayAmount(value: number) {
	return value * rate;
}

/** Compact money rendering: ¥1.2k / $3.50 / $0.000123. */
export function formatCost(value: number) {
	const shown = displayAmount(value);
	if (shown >= 1000) return `${symbol}${shown.toFixed(0)}`;
	if (shown >= 1) return `${symbol}${shown.toFixed(2)}`;
	if (shown >= 0.01) return `${symbol}${shown.toFixed(4)}`;
	if (shown === 0) return `${symbol}0.00`;
	return `${symbol}${shown.toFixed(6)}`;
}

/** Unit prices must not turn small nonzero charges into a displayed zero. */
export function formatUnitPrice(value: number) {
 const shown = displayAmount(value);
 if (!Number.isFinite(shown)) return "—";
 if (shown !== 0 && Math.abs(shown) < 0.000001) return `${symbol}${Number(shown.toPrecision(10)).toString()}`;
 return `${symbol}${Number(shown.toPrecision(10)).toLocaleString("en-US", { minimumFractionDigits: Math.abs(shown) >= 1 || shown === 0 ? 2 : 0, maximumFractionDigits: 10 })}`;
}
