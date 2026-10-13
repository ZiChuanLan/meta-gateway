import type { PriceTier, PriceWindow } from "../../api/types";

export type QuoteUnit = {
  kind: "iso" | "site_credit" | "unknown";
  code: string;
  quota_per_unit?: number;
  display_currency?: string;
  display_symbol?: string;
  display_rate?: number;
};
export type BillingRule = {
  state: "explicit" | "free" | "inherit";
  mode: "token" | "request" | "hybrid";
  input_per_1k?: number;
  output_per_1k?: number;
  cache_per_1k?: number;
  cache_write_per_1k?: number;
  per_request?: number;
  tiers?: PriceTier[];
  windows?: PriceWindow[];
  timezone: string;
  source: string;
  revision: number;
  quote_id?: number;
};
export type BillingView = {
  rule: BillingRule | null;
  revision: number;
  effective_state: string;
  owner: string;
  currency: string;
  effective_rates: {
    input_per_1k: number;
    output_per_1k: number;
    cache_per_1k: number;
    cache_write_per_1k: number;
    per_request: number;
    /** The time window's multiplier in force right now, from the server clock. */
    multiplier: number;
    timezone: string;
  };
  tiers?: PriceTier[];
  windows?: PriceWindow[];
};
export type PriceQuote = {
  id: number;
  scope_key?: string;
  model: string;
  site_id?: number;
  account_id?: number;
  upstream_group?: string;
  source: string;
  purpose: "reference" | "upstream";
  status: string;
  unit: QuoteUnit;
  observed_at: string;
  expires_at?: string;
  raw?: string;
  /**
   * The site that published it, filled by the list endpoint. Reference quotes
   * come from the model catalog and have none.
   */
  site_name?: string;
  values: {
    mode: string;
    input_per_million?: number;
    output_per_million?: number;
    cache_read_per_million?: number;
    cache_write_per_million?: number;
    per_request?: number;
  };
  /**
   * A published billing expression can carry a ladder (a rung per prompt size)
   * and time windows (a multiplier during peak hours). The values above stay the
   * base price; a card that ignores these shows a price the bill will sometimes
   * exceed.
   */
  tiers?: PriceTier[];
  windows?: PriceWindow[];
  timezone?: string;
};
export function nativeAmount(value: number | undefined, unit: QuoteUnit, creditLabel: string) {
  if (value === undefined || !Number.isFinite(value)) return "—";
  const amount = value.toLocaleString(undefined, { maximumSignificantDigits: 10 });
  return `${amount} ${unit.kind === "iso" ? unit.code : creditLabel}`;
}

/**
 * A time window as the operator reads it: "01:00–04:00 ×2", with an end hour of
 * 0 meaning the band runs to midnight (the one case an hour 0–23 notation needs
 * care with).
 */
export function windowLabel(window: PriceWindow) {
  const from = String(window.from_hour).padStart(2, "0");
  const to = window.to_hour === 0 ? "24" : String(window.to_hour).padStart(2, "0");
  const repeat = window.days?.length ? ` ${window.days.join(",")}` : "";
  return { range: `${from}:00–${to}:00${repeat}`, multiplier: `×${window.multiplier}` };
}

/** "Everything up to N tokens", or "everything above the last rung". */
export function tierCeilingLabel(tier: PriceTier, longerLabel: string) {
  return tier.max_prompt_tokens > 0 ? `≤ ${tier.max_prompt_tokens.toLocaleString()}` : longerLabel;
}

/**
 * Who published a price, as the operator has to read it: the site it was
 * collected from, or the model catalog that knows it. A list of prices from one
 * model is a list of publishers — untitled it reads as one price sampled many
 * times, which is what made the same model look like it had six prices.
 */
export function quotePublisher(quote: PriceQuote, labels: { catalog: string; upstream: string }) {
  return (
    quote.site_name?.trim() || (quote.purpose === "reference" ? labels.catalog : labels.upstream)
  );
}
