import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { ModelPriceSummary, type ModelPriceQuote } from "./ModelPriceSummary";
import { PriceFields } from "./PriceFields";
import type { TeamRequest } from "../../team/types";
import { setCurrency } from "../../lib/format";
const quote: ModelPriceQuote = {
  model: "gpt-test",
  currency: "USD",
  evaluated_at: "2026-10-05T00:00:00Z",
  timezone: "UTC",
  input_tokens: 1000,
  candidates: 2,
  ratio: 2,
  input_per_1k: { min: 0.001, max: 0.002 },
  output_per_1k: { min: 0.003, max: 0.003 },
  cache_per_1k: { min: 0, max: 0 },
  per_request: { min: 0.02, max: 0.02 },
  tiered: true,
  scheduled: true,
};
beforeEach(() => {
  localStorage.setItem("meta-gateway.locale", "en");
  setCurrency({ symbol: "$", rate: 1 });
});
afterEach(cleanup);
it("renders the same scoped reference without sending credentials to admin endpoints", async () => {
  const calls = vi.fn().mockResolvedValue(quote);
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <I18nProvider>
        <ModelPriceSummary model="gpt-test" scope="member" request={calls as TeamRequest} />
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("$1.00 – $2.00")).toBeInTheDocument();
  expect(screen.getByText("$3.00")).toBeInTheDocument();
  expect(calls.mock.calls[0]?.[0]).toContain("/me/model-pricing?");
  expect(screen.queryByRole("spinbutton")).toBeNull();
  expect(screen.queryByRole("button", { name: "Update reference" })).toBeNull();
  expect(screen.getByText(/first-tier rates/)).toBeInTheDocument();
  expect(calls.mock.calls[0]?.[0]).toContain("input_tokens=0");
});
it("does not turn missing or malformed pricing into a free quote", async () => {
  const request = vi.fn().mockResolvedValue([]);
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <I18nProvider>
        <ModelPriceSummary model="gpt-test" scope="admin" request={request as TeamRequest} />
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("Pricing is unavailable. Please retry.")).toBeInTheDocument();
  expect(screen.queryByText("$0.00")).not.toBeInTheDocument();
});
it("does not save an empty or negative price as zero but accepts explicit zero", () => {
  const change = vi.fn(),
    valid = vi.fn();
  render(
    <I18nProvider>
      <PriceFields value={{ price_prompt_per_1k: 1 }} onChange={change} onValidityChange={valid} />
    </I18nProvider>,
  );
  const input = screen.getByLabelText("Input price (USD / 1K tokens)");
  fireEvent.change(input, { target: { value: "" } });
  expect(valid).toHaveBeenLastCalledWith(false);
  expect(change).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: "-1" } });
  expect(valid).toHaveBeenLastCalledWith(false);
  expect(change).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: "0" } });
  expect(valid).toHaveBeenLastCalledWith(true);
  expect(change).toHaveBeenLastCalledWith({ price_prompt_per_1k: 0 });
});
