import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { ApiClient } from "../../api/client";
import { BillingRulePanel, BillingRuleDialog } from "./BillingRulePanel";
import { QuoteActions } from "./QuoteActions";
import type { BillingRule, BillingView, PriceQuote } from "./pricingTypes";
const client = new ApiClient("local-test");
vi.mock("../../session", () => ({ useSession: () => ({ client }) }));
const view: BillingView = {
  rule: null,
  revision: 0,
  effective_state: "legacy",
  owner: "model:m",
  currency: "USD",
  effective_rates: {
    input_per_1k: 0.001,
    output_per_1k: 0.002,
    cache_per_1k: 0.0001,
    cache_write_per_1k: 0.001,
    per_request: 0,
    timezone: "UTC",
  },
};
const quote: PriceQuote = {
  id: 1,
  scope_key: "site:m",
  model: "m",
  source: "site_public",
  purpose: "upstream",
  status: "calculable",
  unit: { kind: "site_credit", code: "credit:site/1" },
  observed_at: "2026-10-12T00:00:00Z",
  values: { mode: "token", input_per_million: 2, output_per_million: 4, cache_read_per_million: 0 },
};
let calls: Array<{ path: string; body: Record<string, unknown> }>;
function wrap(
  ui: React.ReactNode,
  qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  }),
) {
  return (
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <I18nProvider>{ui}</I18nProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}
beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const body = init?.body ? JSON.parse(String(init.body)) : {};
      if (init?.method) {
        calls.push({ path, body });
        return new Response(JSON.stringify({ ...body.rule, revision: 1 }), { status: 200 });
      }
      return new Response(
        JSON.stringify(
          path.startsWith("/admin/price-quotes")
            ? { items: [] }
            : path === "/admin/ratios"
              ? []
              : view,
        ),
        { status: 200 },
      );
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
it("converts display /1M to storage /1k and preserves explicit free output and unknown cache", async () => {
  render(wrap(<BillingRulePanel model="m" />));
  const input = await screen.findByLabelText("Ordinary input (USD / 1M tokens)");
  expect(input).toHaveValue(1);
  fireEvent.change(input, { target: { value: "3" } });
  fireEvent.change(screen.getByLabelText("Output (USD / 1M tokens)"), { target: { value: "0" } });
  fireEvent.change(screen.getByLabelText("Cache read (USD / 1M tokens)"), {
    target: { value: "" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(calls).toHaveLength(1));
  const rule = calls[0]!.body.rule as Record<string, unknown>;
  expect(rule).toMatchObject({ state: "explicit", input_per_1k: 0.003, output_per_1k: 0 });
  expect(rule).not.toHaveProperty("cache_per_1k");
  expect(calls[0]!.body.expected_revision).toBe(0);
  await screen.findByText("Saved. Future requests use the new rule.");
  expect(input).toHaveValue(3);
});
it("keeps invalid input unsaved and marks blank required values invalid", async () => {
  render(wrap(<BillingRulePanel model="m" />));
  const input = await screen.findByLabelText("Ordinary input (USD / 1M tokens)");
  fireEvent.change(input, { target: { value: "" } });
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  expect(calls).toHaveLength(0);
});
it("keeps dirty input after a version conflict", async () => {
  const original = fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) =>
      init?.method
        ? new Response(JSON.stringify({ error: "pricing changed" }), { status: 409 })
        : original(path, init),
    ),
  );
  render(wrap(<BillingRulePanel model="m" />));
  const input = await screen.findByLabelText("Ordinary input (USD / 1M tokens)");
  fireEvent.change(input, { target: { value: "3" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await screen.findByText(/price list changed/);
  expect(input).toHaveValue(3);
});
it("guards escape-close of a dirty billing dialog", async () => {
  const close = vi.fn();
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  render(wrap(<BillingRuleDialog model="m" onClose={close} />));
  fireEvent.change(await screen.findByLabelText("Ordinary input (USD / 1M tokens)"), {
    target: { value: "7" },
  });
  await act(async () => {});
  fireEvent.keyDown(document, { key: "Escape" });
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(close).not.toHaveBeenCalled();
});
it("freezes a copy target, requires native conversion and submits the reviewed revision", async () => {
  const qc = new QueryClient();
  const ui = render(
    wrap(
      <QuoteActions
        quote={quote}
        target={{ scope: "model", key: "m", revision: 2 }}
        current={view.effective_rates}
      />,
      qc,
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Use as my price" }));
  expect(screen.getByRole("button", { name: "Confirm and use it" })).toBeDisabled();
  fireEvent.change(screen.getByLabelText("USD per 1 site credit"), {
    target: { value: ".5" },
  });
  ui.rerender(
    wrap(
      <QuoteActions
        quote={{ ...quote, id: 9, values: { ...quote.values, input_per_million: 999 } }}
        target={{ scope: "model", key: "m", revision: 3 }}
        current={view.effective_rates}
      />,
      qc,
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Confirm and use it" }));
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(calls[0]!.body).toEqual({ quote_id: 1, expected_revision: 2, usd_per_unit: 0.5 });
});

it("stores manual upstream observations without writing the gateway rule", async () => {
  const { ManualQuoteDialog } = await import("./ManualQuoteDialog");
  const close = vi.fn();
  render(wrap(<ManualQuoteDialog model="m" siteId={7} onClose={close} />));
  expect(screen.getByRole("button", { name: "Save quote observation" })).toBeDisabled();
  fireEvent.change(screen.getByLabelText("Ordinary input / 1M tokens"), { target: { value: "2" } });
  fireEvent.change(screen.getByLabelText("Output / 1M tokens"), { target: { value: "0" } });
  fireEvent.click(screen.getByRole("button", { name: "Save quote observation" }));
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(calls[0]!.path).toBe("/admin/price-quotes");
  expect(calls[0]!.body).toMatchObject({
    model: "m",
    site_id: 7,
    unit: { kind: "site_credit" },
    values: { mode: "token", input_per_million: 2, output_per_million: 0 },
  });
  expect(calls[0]!.body).not.toHaveProperty("rule");
});

it("promotes a legacy Local timezone into a supported IANA zone", async () => {
  const original = fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) =>
      path.startsWith("/admin/billing-rule") && !init?.method
        ? new Response(
            JSON.stringify({
              ...view,
              effective_rates: { ...view.effective_rates, timezone: "Local" },
            }),
          )
        : original(path, init),
    ),
  );
  render(wrap(<BillingRulePanel model="m" />));
  const zone = await screen.findByLabelText("Billing timezone");
  expect(zone).not.toHaveValue("Local");
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(calls).toHaveLength(1));
  expect((calls[0]!.body.rule as Record<string, unknown>).timezone).toBe(
    Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
  );
});

it("edits the model multiplier without altering a member rule", async () => {
  render(wrap(<BillingRulePanel model="m" memberId={17} />));
  // The multiplier control sits with the price it scales, so it appears once
  // the rule view has loaded.
  fireEvent.click(await screen.findByRole("button", { name: "Model multiplier" }));
  const input = await screen.findByLabelText("Model multiplier");
  fireEvent.change(input, { target: { value: "1.5" } });
  fireEvent.click(screen.getByRole("dialog").querySelector("button[type=submit]")!);
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(calls[0]).toEqual({ path: "/admin/ratios/m", body: { ratio: 1.5 } });
});

const copiedRule: BillingRule = {
  state: "explicit",
  mode: "token",
  input_per_1k: 0.002,
  timezone: "UTC",
  source: "quote_copy",
  quote_id: 1,
  revision: 4,
};
function withQuote(ruleView: BillingView = view) {
  const original = fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) =>
      path.startsWith("/admin/price-quotes")
        ? new Response(JSON.stringify({ items: [quote] }), { status: 200 })
        : path.startsWith("/admin/billing-rule") && !init?.method
          ? new Response(JSON.stringify(ruleView), { status: 200 })
          : original(path, init),
    ),
  );
}
function toggle(details: HTMLDetailsElement, open: boolean) {
  details.open = open;
  fireEvent(details, new Event("toggle"));
}
it("answers with the price in force and folds the sources into one line", async () => {
  withQuote();
  render(wrap(<BillingRulePanel model="m" />));
  // The panel's answer, in rates rather than in rule vocabulary.
  expect(await screen.findByText(/Charging now: .*\/ 1M input/)).toBeInTheDocument();
  // One line of evidence, closed by default; the rows live inside it.
  const evidence = document.querySelector("details.billing-evidence") as HTMLDetailsElement;
  expect(evidence.open).toBe(false);
  expect(evidence.querySelector("summary")?.textContent).toMatch(/Price the site publishes/);
  expect(evidence.querySelectorAll(".quote-entry")).toHaveLength(1);
  // The price entry is folded too, and is the same editor behind the fold.
  const change = document.querySelector("details.billing-change") as HTMLDetailsElement;
  expect(change.open).toBe(false);
  expect(change.querySelector("summary")?.textContent).toMatch(/Change the price/);
  toggle(change, true);
  expect(screen.getByLabelText("Ordinary input (USD / 1M tokens)")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
  // Opening the evidence still leads to the explicit, versioned copy action.
  toggle(evidence, true);
  fireEvent.click(screen.getByRole("button", { name: "Use as my price" }));
  expect(screen.getByRole("button", { name: "Confirm and use it" })).toBeInTheDocument();
});
it("marks the source the price in force was copied from", async () => {
  withQuote({ ...view, rule: copiedRule, revision: copiedRule.revision });
  render(wrap(<BillingRulePanel model="m" />));
  expect(await screen.findByText("This is what you charge now")).toBeInTheDocument();
});
