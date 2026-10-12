import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { ModelsWorkspace } from "./ModelsWorkspace";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** A catalog row, shaped as `/me/model-catalog` answers. */
const catalogRow = (name: string) => ({
  name,
  vendor: "OpenAI",
  kind: "chat",
  context_window: 32000,
  input_modalities: "text",
  output_modalities: "text",
  candidates: 1,
});

/** A priced quote, shaped as one batch item. */
const batchItem = (model: string) => ({
  model,
  currency: "USD",
  evaluated_at: "2026-10-05T00:00:00Z",
  timezone: "UTC",
  input_tokens: 0,
  candidates: 1,
  ratio: 1,
  input_per_1k: { min: 0.001, max: 0.001 },
  output_per_1k: { min: 0.002, max: 0.002 },
  cache_per_1k: { min: 0, max: 0 },
  per_request: { min: 0, max: 0 },
  tiered: false,
  scheduled: false,
  priced_candidates: 1,
  status: "priced",
});

/** Answers the batch with one quote per requested name, the catalog otherwise. */
function stubFetch(requests: string[], models: string[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      requests.push(path);
      if (path.startsWith("/me/model-pricing/batch")) {
        const asked = new URL(path, "http://gateway").searchParams.getAll("model");
        return new Response(JSON.stringify({ items: asked.map(batchItem), unavailable: [] }));
      }
      return new Response(JSON.stringify(models.map(catalogRow)));
    }),
  );
}

it("shares directory search, preserves connect, and clears an empty filter", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  const requests: string[] = [];
  stubFetch(requests, ["gpt-test"]);
  const connect = vi.fn();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(
    <QueryClientProvider client={qc}>
      <I18nProvider>
        <ModelsWorkspace
          userID={1}
          locale="en"
          canRoute={false}
          plans={[]}
          planId={0}
          onPlanChange={vi.fn()}
          onCreatePlan={vi.fn()}
          onConnect={connect}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect((await screen.findAllByText("gpt-test"))[0]).toBeInTheDocument();
  expect(container.querySelector(".model-directory .models-simple-toolbar")).toBeInTheDocument();
  // The directory renders cards now (same model, same shared facts): the
  // assertion is about the shared atoms being present, not about the container.
  expect(container.querySelector(".model-card-grid")).toBeInTheDocument();
  expect(container.querySelector(".model-card .model-facts")).toBeInTheDocument();
  expect(container.querySelector(".model-facts")).toHaveTextContent("32.0k");
  expect(container.querySelector(".model-facts")).not.toHaveTextContent("Unknown");
  expect(screen.queryByRole("combobox", { name: "Model family" })).toBeNull();
  expect(container.querySelector(".model-price-input")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "View gpt-test details" }));
  fireEvent.click(screen.getAllByRole("button", { name: /connect/i })[0]!);
  expect(connect).toHaveBeenCalledWith("gpt-test");
  fireEvent.click(screen.getAllByRole("button", { name: "Close" })[0]!);
  fireEvent.change(container.querySelector(".models-search input")!, {
    target: { value: "missing" },
  });
  expect(screen.getByText("No matching models")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
  expect(screen.getAllByText("gpt-test")[0]).toBeInTheDocument();
  expect(requests).toContain("/me/model-catalog");
  expect(requests.every((path) => path.startsWith("/me/"))).toBe(true);
  expect(container.querySelector(".model-card-workspace")).toBeInTheDocument();
  // One page asks the batch once; there is no per-card price request.
  const priceCalls = requests.filter((path) => path.startsWith("/me/model-pricing/batch"));
  expect(priceCalls).toHaveLength(1);
  expect(new URL(priceCalls[0]!, "http://gateway").searchParams.getAll("model")).toEqual([
    "gpt-test",
  ]);
  expect(requests.some((path) => path.startsWith("/me/model-pricing?"))).toBe(false);
});

it("asks the price batch for the current page only when the page turns", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  const requests: string[] = [];
  const names = Array.from({ length: 25 }, (_, index) => `model-${index}`);
  stubFetch(requests, names);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <I18nProvider>
        <ModelsWorkspace
          userID={1}
          locale="en"
          canRoute={false}
          plans={[]}
          planId={0}
          onPlanChange={vi.fn()}
          onCreatePlan={vi.fn()}
          onConnect={vi.fn()}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect((await screen.findAllByText("model-0")).length).toBeGreaterThan(0);
  const first = requests.filter((path) => path.startsWith("/me/model-pricing/batch"));
  expect(first).toHaveLength(1);
  expect(new URL(first[0]!, "http://gateway").searchParams.getAll("model")).toHaveLength(20);

  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  expect((await screen.findAllByText("model-20")).length).toBeGreaterThan(0);
  const batches = requests.filter((path) => path.startsWith("/me/model-pricing/batch"));
  expect(batches).toHaveLength(2);
  // The second batch carries page two's names alone — page one is not re-asked.
  expect(new URL(batches[1]!, "http://gateway").searchParams.getAll("model")).toEqual([
    "model-20",
    "model-21",
    "model-22",
    "model-23",
    "model-24",
  ]);
});
