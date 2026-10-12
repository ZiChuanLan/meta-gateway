import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SiteObservations, SiteProbeSiteStatus } from "../../api/types";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { ToastProvider } from "../../toast";
import { SiteObservationWorkspace } from "./SiteObservationWorkspace";

const policy = { ratio_threshold: 0.9, min_samples: 5, low_rounds: 2, high_rounds: 2 };
const site: SiteProbeSiteStatus = {
  site_id: 1,
  site_name: "Sample site",
  probe_source_kind: "newapi",
  probe_source_enabled: false,
  monitor_count: 32,
  policy,
  auto_apply: false,
};
const clients: QueryClient[] = [];
function backend({
  failure = false,
  group = false,
  sitePatch = {},
  items: itemCount = 32,
  collectResult = { failed: 0 },
}: {
  failure?: boolean;
  group?: boolean;
  /** Whatever the case under test needs the site row to say. */
  sitePatch?: Partial<typeof site>;
  items?: number;
  collectResult?: Record<string, unknown>;
} = {}) {
  const siteRow = { ...site, ...sitePatch };
  const items = Array.from({ length: itemCount }, (_, i) => ({
    id: `1/${i}`,
    site_id: 1,
    raw_model: `model-${String(i + 1).padStart(2, "0")}`,
    observed_at: "2026-10-11T00:00:00Z",
    price: {},
    series: {
      source: "site" as const,
      scope: "site_model",
      ratio: null,
      samples: 0,
      ok: 0,
      failed: 0,
      ignored: 0,
      stale: false,
      buckets: [],
      paths: [],
    },
    weak_evidence: false,
  }));
  if (group)
    items.push({ ...items[0]!, id: "1/other", group_name: "vip" } as (typeof items)[number]);
  const data: SiteObservations = {
    sites: [siteRow],
    items,
    since: "2026-10-10T00:00:00Z",
    until: "2026-10-11T00:00:00Z",
  };
  const save = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => json({ id: 1 }));
  const collect = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
    json(collectResult),
  );
  const apply = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
    json({ actions: [], dry_run: true }),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path === "/admin/site-probe/observations")
        return failure ? json({ error: "read failed" }, 500) : json(data);
      if (path === "/admin/sites")
        return json([
          {
            id: 1,
            name: site.site_name,
            probe_source_kind: "newapi",
            probe_auto: true,
            probe_source_enabled: false,
          },
        ]);
      if (path === "/admin/runtime-settings")
        return json({ editable: { site_probe_interval_seconds: 900 } });
      if (path === "/admin/site-probe/report")
        return json({
          rows: [
            { raw_model: "route-only", route: "local-route", site_id: 1, members: [], rounds: [] },
          ],
          sites: [siteRow],
        });
      if (path === "/admin/site-probe/source") return save(input, init);
      if (path === "/admin/site-probe/collect") return collect(input, init);
      if (path === "/admin/site-probe/apply") return apply(input, init);
      return json({});
    }),
  );
  return { save, collect, apply };
}
function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <MemoryRouter>
              <SiteObservationWorkspace />
            </MemoryRouter>
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
  localStorage.setItem("meta-gateway.admin-token", "test-token");
});
afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  clients.length = 0;
  vi.unstubAllGlobals();
});
it("lists every collected record independently of local route rows", async () => {
  const actions = backend();
  mount();
  await screen.findByText("model-32");
  expect(document.querySelectorAll(".observation-model:not(.is-header)")).toHaveLength(32);
  expect(screen.queryByText("route-only")).toBeNull();
  expect(actions.collect).not.toHaveBeenCalled();
  expect(actions.apply).not.toHaveBeenCalled();
  const catalogue = document.querySelector("details.observation-catalogue") as HTMLDetailsElement;
  expect(catalogue.open).toBe(true);
  catalogue.open = false;
  fireEvent(catalogue, new Event("toggle"));
  expect(catalogue.open).toBe(false);
});
it("keeps the collection total when search narrows the visible models", async () => {
  backend();
  mount();
  await screen.findByText("model-32");
  fireEvent.change(screen.getByRole("searchbox"), { target: { value: "model-32" } });
  expect(document.querySelectorAll(".observation-model:not(.is-header)")).toHaveLength(1);
  expect(screen.getByText(/32 models in the latest successful collection/)).toBeInTheDocument();
  expect(screen.getByText(/1 after filtering/)).toBeInTheDocument();
});
it("keeps duplicate model observations when their upstream groups differ", async () => {
  backend({ group: true });
  mount();
  await screen.findByText("vip");
  expect(screen.getAllByText("model-01")).toHaveLength(2);
  expect(document.querySelectorAll(".observation-model:not(.is-header)")).toHaveLength(33);
});
it("reports initial read errors with retry instead of an empty successful snapshot", async () => {
  backend({ failure: true });
  mount();
  await screen.findByRole("button", { name: /retry/i });
  expect(screen.queryByText("model-01")).toBeNull();
});
it("saving source settings preserves a disabled collection switch", async () => {
  const actions = backend();
  mount();
  await screen.findByText("model-01");
  fireEvent.click(screen.getByRole("button", { name: "Source settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Save source" }));
  await waitFor(() => expect(actions.save).toHaveBeenCalledTimes(1));
  const init = actions.save.mock.calls[0]?.[1] as RequestInit;
  const body = JSON.parse(String(init.body));
  expect(body.enabled).toBe(false);
  expect(body.site_id).toBe(1);
  expect(body.auto).toBe(true);
});

it("names why a site shows no collected models instead of a bare 0", async () => {
  // One mount per state: the point of the reason line is that these states are
  // told apart, so each has to be rendered on its own.
  const cases: { patch: Partial<typeof site>; expected: RegExp }[] = [
    { patch: { probe_source_enabled: false }, expected: /Scheduled collection is off/ },
    {
      patch: { probe_source_enabled: true, probe_last_error: "connection refused" },
      expected: /The last collection failed/,
    },
    { patch: { probe_source_enabled: true }, expected: /Never collected/ },
    {
      patch: {
        probe_source_enabled: true,
        probe_last_run_at: "2026-10-11T00:00:00Z",
        last_run_status: "ok",
        monitor_count: 0,
      },
      expected: /publishes no model records/,
    },
    {
      patch: {
        probe_source_enabled: true,
        probe_last_run_at: "2026-10-09T00:00:00Z",
        last_run_status: "ok",
        monitor_count: 32,
      },
      expected: /none of them inside the selected period/,
    },
  ];
  for (const { patch, expected } of cases) {
    backend({ items: 0, sitePatch: patch });
    mount();
    expect(await screen.findByText(expected)).toBeInTheDocument();
    expect(screen.getByText("No model collected in this period")).toBeInTheDocument();
    cleanup();
  }
});
it("does not call a filtered-out model a failed collection", async () => {
  backend();
  mount();
  await screen.findByText("model-01");
  fireEvent.change(screen.getByRole("searchbox"), { target: { value: "nothing-matches" } });
  expect(await screen.findByText(/No model matches/)).toBeInTheDocument();
  expect(screen.queryByText(/publishes no model records/)).toBeNull();
});
it("reports what a collect round did instead of returning in silence", async () => {
  const actions = backend({ collectResult: { failed: 0, collected: 3 } });
  mount();
  await screen.findByText("model-01");
  // Both the toolbar and the selected site offer "Collect now"; the round this
  // test is about is the toolbar's all-sites collect.
  const toolbar = document.querySelector(".observation-toolbar") as HTMLElement;
  fireEvent.click(within(toolbar).getByRole("button", { name: "Collect now" }));
  await waitFor(() => expect(actions.collect).toHaveBeenCalledTimes(1));
  expect(
    await screen.findByText(/Round finished: 3 sites collected, 0 failed/),
  ).toBeInTheDocument();
});
it("reports a single site's round, including one that collected nothing", async () => {
  const actions = backend({
    sitePatch: { probe_source_enabled: true },
    collectResult: {
      runs: [{ site_id: 1, status: "ok", monitor_count: 0 }],
      failed: 0,
      actions: [],
    },
  });
  mount();
  await screen.findByText("model-01");
  const detail = document.querySelector(".observation-detail") as HTMLElement;
  fireEvent.click(within(detail).getByRole("button", { name: "Collect now" }));
  await waitFor(() => expect(actions.collect).toHaveBeenCalledTimes(1));
  const init = actions.collect.mock.calls[0]?.[1] as RequestInit;
  expect(JSON.parse(String(init.body))).toEqual({ site_ids: [1] });
  expect(
    await screen.findByText(/collected fine but returned no model records/),
  ).toBeInTheDocument();
});
it("explains the directory import before it writes anything", async () => {
  backend();
  mount();
  await screen.findByText("model-01");
  fireEvent.click(screen.getByRole("button", { name: "Import public directory" }));
  const dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent(/writes only to sites you already have/);
  expect(dialog).toHaveTextContent(/are never created/);
});
