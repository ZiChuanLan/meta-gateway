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
};
export function nativeAmount(value: number | undefined, unit: QuoteUnit, creditLabel: string) {
  if (value === undefined || !Number.isFinite(value)) return "—";
  const amount = value.toLocaleString(undefined, { maximumSignificantDigits: 10 });
  return `${amount} ${unit.kind === "iso" ? unit.code : creditLabel}`;
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
