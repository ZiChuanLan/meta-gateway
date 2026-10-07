import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, useLocation } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { Dashboard, DashboardView, MEMBER_DASHBOARD_CAPS } from "./Dashboard";
import { memberDashboardSource } from "../member/MemberDashboardSource";

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location">{location.search}</div>;
}

/** Every endpoint the overview touches, so the range wiring is observable. */
function renderDashboard(initialEntry = "/") {
  const windowed: { path: string; params: URLSearchParams }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      const path = url.pathname;
      let body: unknown = [];
      if (path === "/readyz") return new Response("ok", { status: 200 });
      if (path === "/admin/usage/summary") {
        if (url.searchParams.get("since")) windowed.push({ path, params: url.searchParams });
        body = {
          request_count: url.searchParams.get("since") ? 40 : 4000,
          prompt_tokens: 10,
          completion_tokens: 20,
          total_tokens: 1000,
          cache_read_tokens: 250,
          cache_creation_tokens: 5,
          ok_count: 38,
          client_error_count: 1,
          server_error_count: 1,
          other_count: 0,
          cost: 1.5,
        };
      } else if (path === "/admin/usage/series") {
        windowed.push({ path, params: url.searchParams });
        const since = new Date("2026-09-17T00:00:00.000Z");
        body = {
          since: since.toISOString(),
          until: new Date("2026-09-18T00:00:00.000Z").toISOString(),
          bucket_seconds: 3600,
          requests: [1, 5, 0, 9],
          failed: [0, 1, 0, 2],
          tokens: [10, 50, 0, 90],
          prompt_tokens: [1, 5, 0, 9],
          completion_tokens: [9, 45, 0, 81],
          cache_read_tokens: [0, 0, 0, 0],
          cache_creation_tokens: [0, 0, 0, 0],
          cost: [0.1, 0.5, 0, 0.9],
        };
      } else if (path === "/admin/usage/top-models") {
        windowed.push({ path, params: url.searchParams });
        body = [
          { model: "gpt-image-2", requests: 9, total_tokens: 900, cost: 0.9, failed: 2 },
          { model: "gemini-flash", requests: 4, total_tokens: 400, cost: 0.4, failed: 0 },
        ];
      } else if (path === "/admin/channels/overview") {
        body = [
          {
            channel: { id: 1, name: "relay-a", status: "enabled" },
            member_count: 0,
            enabled_members: 0,
            healthy_members: 0,
          },
        ];
      } else if (path === "/admin/channels" || path === "/admin/downstream-keys") {
        body = [];
      } else if (path === "/admin/proxy-logs") {
        body = [
          {
            id: 1,
            request_id: "req-1",
            channel_id: 1,
            model: "gpt-image-2",
            status: 200,
            latency_ms: 900,
            attempt: 1,
            created_at: new Date().toISOString(),
          },
        ];
      }
      return new Response(JSON.stringify(body), {
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <SessionProvider>
          <MemoryRouter initialEntries={[initialEntry]}>
            <Dashboard />
            <LocationProbe />
          </MemoryRouter>
        </SessionProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { windowed, ...utils };
}

describe("dashboard time range", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("drives the series, ranking and summary with the resolved window", async () => {
    const { windowed } = renderDashboard();
    await screen.findAllByText("gpt-image-2");
    const series = windowed.filter((r) => r.path === "/admin/usage/series");
    expect(series.length).toBeGreaterThan(0);
    // The default window is 24h; the server-side aggregate must be windowed.
    expect(series.at(-1)!.params.get("since")).toBeTruthy();
    expect(series.at(-1)!.params.get("until")).toBeTruthy();
    // The ranking is aggregated in SQL, so the model name is not derived from
    // a truncated page of usage rows.
    expect(screen.getAllByText("gpt-image-2").length).toBeGreaterThan(0);
    expect(screen.getByText("gemini-flash")).toBeInTheDocument();
  });

  it("re-anchors every windowed query when a preset is picked", async () => {
    const { windowed } = renderDashboard();
    await screen.findAllByText("gpt-image-2");
    const before = windowed.filter((r) => r.path === "/admin/usage/series").at(-1)!;
    const beforeSince = new Date(before.params.get("since")!).getTime();

    fireEvent.click(screen.getByRole("tab", { name: "30 days" }));

    await waitFor(() => {
      const latest = windowed.filter((r) => r.path === "/admin/usage/series").at(-1)!;
      const span =
        new Date(latest.params.get("until")!).getTime() -
        new Date(latest.params.get("since")!).getTime();
      // 30 days, not the previous 24 hours.
      expect(span).toBeGreaterThan(29 * 24 * 3600_000);
    });
    const after = windowed.filter((r) => r.path === "/admin/usage/series").at(-1)!;
    expect(new Date(after.params.get("since")!).getTime()).toBeLessThan(beforeSince);
    expect(screen.getByTestId("location").textContent).toContain("range=30d");
  });

  // The window control used to own a full-width bar above the fold; it now
  // lives in the chart panel header, next to the readout it explains.
  it("keeps the window control inside the chart panel header", async () => {
    const { container } = renderDashboard();
    await screen.findAllByText("gpt-image-2");
    expect(container.querySelector(".dashboard-range-bar")).toBeNull();
    const header = container.querySelector(".cockpit-chart-header");
    const picker = header?.querySelector(".time-range");
    expect(picker).not.toBeNull();
    expect(
      header!.compareDocumentPosition(picker!) & Node.DOCUMENT_POSITION_CONTAINED_BY,
    ).toBeTruthy();
  });

  it("zooms into a single bucket by requesting that sub-window", async () => {
    const { windowed, container } = renderDashboard();
    await screen.findAllByText("gpt-image-2");
    const bars = container.querySelectorAll(".chart-bar:not(.is-failed)");
    expect(bars.length).toBeGreaterThan(0);
    const countBefore = windowed.filter((r) => r.path === "/admin/usage/series").length;
    fireEvent.click(bars[1]!);

    await waitFor(() => {
      const series = windowed.filter((r) => r.path === "/admin/usage/series");
      expect(series.length).toBeGreaterThan(countBefore);
      const span =
        new Date(series.at(-1)!.params.get("until")!).getTime() -
        new Date(series.at(-1)!.params.get("since")!).getTime();
      // The drill-down asks for one hour (the bucket size of the overview).
      expect(span).toBe(3600_000);
      expect(series.at(-1)!.params.get("buckets")).toBe("12");
    });
    // The chart header switches to the detail view and offers a way back.
    expect(screen.getByLabelText("Back to overview")).toBeInTheDocument();
  });
});

/**
 * The member's own band, and what must NOT be on a member's page.
 *
 * The overview is shared by staff and members, and it used to open with the
 * operator's endpoint strip and channel-health matrix for both. This pins the
 * member side: their identity and budgets at the top, the four ways in, and none
 * of the gateway's own instrumentation or flourishes.
 */
describe("member overview", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("opens with the member's own band instead of the operator's readouts", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = new URL(String(input), "http://localhost").pathname;
        let body: unknown = [];
        if (path === "/me") {
          body = {
            user: { id: 7, username: "lan", name: "Lan", role: "member", policy_id: 2 },
            policy: { id: 2, name: "Standard", all_models: true, max_keys: 10, rpm: 120 },
            branding: { name: "Meta Gateway", api_base_url: "https://gw.example/v1" },
            credit: {
              total: 5000000,
              used: 2000000,
              available: 3000000,
              unlimited: false,
              cost_total: 50,
              cost_used: 4.92,
              cost_available: 45.08,
              cost_unlimited: false,
            },
          };
        } else if (path === "/me/usage/summary") {
          body = {
            request_count: 12,
            prompt_tokens: 100,
            completion_tokens: 50,
            total_tokens: 150,
            cache_read_tokens: 0,
            cache_creation_tokens: 0,
            ok_count: 11,
            client_error_count: 1,
            server_error_count: 0,
            other_count: 0,
            cost: 0.4,
          };
        } else if (path === "/me/usage/series") {
          body = {
            since: new Date("2026-10-06T00:00:00.000Z").toISOString(),
            until: new Date("2026-10-07T00:00:00.000Z").toISOString(),
            bucket_seconds: 3600,
            requests: [1, 2, 3],
            failed: [0, 0, 1],
            tokens: [10, 20, 30],
            prompt_tokens: [6, 12, 18],
            completion_tokens: [4, 8, 12],
            cache_read_tokens: [0, 0, 0],
            cache_creation_tokens: [0, 0, 0],
            cost: [0.01, 0.02, 0.03],
          };
        }
        return new Response(JSON.stringify(body), {
          headers: { "Content-Type": "application/json" },
        });
      }),
    );
    render(
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
      >
        <I18nProvider>
          <MemoryRouter initialEntries={["/"]}>
            <DashboardView source={memberDashboardSource} caps={MEMBER_DASHBOARD_CAPS} />
          </MemoryRouter>
        </I18nProvider>
      </QueryClientProvider>,
    );

    // The band reads /me: the base URL, the plan and its limits, and the two
    // budgets with the remaining balance as the figure rather than five numbers
    // to compare. (The greeting itself is the page heading, which this test
    // mounts without a host — Dashboard supplies it from the same query.)
    expect(await screen.findByText("https://gw.example/v1")).toBeInTheDocument();
    expect(screen.getByText(/Standard/)).toBeInTheDocument();
    expect(screen.getByText("3,000,000")).toBeInTheDocument();
    expect(screen.getByText("$45.08")).toBeInTheDocument();
    expect(screen.getByText(/2,000,000\s*\/\s*5,000,000/)).toBeInTheDocument();
    // The four ways in point at the member's own pages.
    expect(screen.getByRole("link", { name: /New token/ })).toHaveAttribute("href", "/keys");
    expect(screen.getByRole("link", { name: /Browse models/ })).toHaveAttribute("href", "/models");

    // None of the operator's instrumentation, and none of the brand flourishes.
    expect(screen.queryByText("Channel health")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Clients call this URL with a downstream token."),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Replay/ })).not.toBeInTheDocument();
  });
});
