import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { I18nProvider } from "../../i18n";
import {
  PriceTiersEditor,
  PriceWindowsEditor,
  encodeTiers,
  encodeWindows,
  parseTiers,
  parseWindows,
  pricingNumber, validTiers, validWindows,
} from "./PricingEditors";

/**
 * The editors own the conversion between the API's JSON text and the arrays the
 * form edits. A round-trip that loses a value would change a price silently, so
 * the conversion is pinned here rather than only through the dialogs that use
 * it.
 */
describe("pricing editors", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
  });
  afterEach(cleanup);

  it("round-trips a ladder and a schedule", () => {
    const tiers = [
      { max_prompt_tokens: 32000, prompt: 0.15, completion: 0.6, cache: 0, per_request: 0 },
      { max_prompt_tokens: 0, prompt: 1.5, completion: 6, cache: 0.3, per_request: 0.02 },
    ];
    const windows = [{ days: [6, 7], from_hour: 0, to_hour: 0, multiplier: 0.5 }];
    expect(parseTiers(encodeTiers(tiers))).toEqual(tiers);
    expect(parseWindows(encodeWindows(windows))).toEqual(windows);
  });

  // An empty ladder is how a layer returns to flat pricing, so removing the
  // last rung must clear the column rather than store "[]".
  it("encodes an empty list as an empty string", () => {
    expect(encodeTiers([])).toBe("");
    expect(encodeWindows([])).toBe("");
    expect(parseTiers("")).toEqual([]);
    expect(parseWindows(undefined)).toEqual([]);
  });

  // The dialog is an editor: opening it on a value it cannot read must not
  // throw, or the operator could not fix that value.
  it("treats malformed text as an empty ladder", () => {
    expect(parseTiers("{not json")).toEqual([]);
    expect(parseWindows('{"days":1}')).toEqual([]);
  });

  it("drops out-of-range values from stored text", () => {
    const parsed = parseWindows(
      JSON.stringify([{ days: [0, 3, 9, 3], from_hour: 44, to_hour: -2, multiplier: 0 }]),
    );
    expect(parsed).toHaveLength(1);
    // Weekday 0/9 are not ISO days and the duplicate is collapsed; hours clamp
    // into the day; a zero multiplier would give usage away and falls back to 1.
    expect(parsed[0]).toEqual({ days: [3], from_hour: 23, to_hour: 0, multiplier: 1 });
  });

  it("adds and removes ladder rows", () => {
    const seen: unknown[] = [];
    const { rerender } = render(
      <QueryClientProvider client={new QueryClient()}>
        <I18nProvider>
          <PriceTiersEditor value={[]} onChange={(next) => seen.push(next)} />
        </I18nProvider>
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Add tier" }));
    expect(seen).toHaveLength(1);
    expect((seen[0] as unknown[]).length).toBe(1);
    // Removing the only rung hands back an empty list, which encodes to "".
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <I18nProvider>
          <PriceTiersEditor
            value={[{ max_prompt_tokens: 100, prompt: 1, completion: 2, cache: 0, per_request: 0 }]}
            onChange={(next) => seen.push(next)}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Remove tier" }));
    expect(seen.at(-1)).toEqual([]);
  });

  it("toggles weekdays and reports the every-day state", () => {
    const seen: unknown[] = [];
    render(
      <QueryClientProvider client={new QueryClient()}>
        <I18nProvider>
          <PriceWindowsEditor
            value={[{ days: [], from_hour: 22, to_hour: 6, multiplier: 0.8 }]}
            onChange={(next) => seen.push(next)}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );
    // No days selected means every day, and the panel says so.
    expect(screen.getByText("Every day")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Wed" }));
    expect((seen.at(-1) as Array<{ days: number[] }>)[0]?.days).toEqual([3]);
  });
});

 describe("pricing draft safety",()=>{
 it("does not turn blank or negative input into zero",()=>{
  expect(Number.isNaN(pricingNumber(""))).toBe(true);
  expect(pricingNumber("0")).toBe(0);
  expect(pricingNumber("-2")).toBe(-2);
  expect(validTiers([{max_prompt_tokens:0,prompt:NaN,completion:1,cache:0,per_request:0}])).toBe(false);
  expect(()=>encodeTiers([{max_prompt_tokens:0,prompt:NaN,completion:1,cache:0,per_request:0}])).toThrow();
 });
 it("rejects ambiguous ceilings and fractional hours",()=>{
  const tier={max_prompt_tokens:1000,prompt:1,completion:2,cache:0,per_request:0};
  expect(validTiers([tier,tier])).toBe(false);
  expect(validTiers([{...tier,max_prompt_tokens:1.5}])).toBe(false);
  expect(validWindows([{days:[],from_hour:1.5,to_hour:6,multiplier:1}])).toBe(false);
 });
 it("accepts tiny positive multipliers without silently raising them",()=>{
  const value=[{days:[],from_hour:22,to_hour:6,multiplier:0.001}];
  expect(validWindows(value)).toBe(true);
  expect(JSON.parse(encodeWindows(value))[0].multiplier).toBe(0.001);
  expect(()=>encodeWindows([{...value[0]!,multiplier:0}])).toThrow();
 });
 });
