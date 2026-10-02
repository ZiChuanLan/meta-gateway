import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, useLocation, useNavigate } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { Logs } from "./Logs";

function LocationProbe() {
  const location = useLocation();
  const navigate = useNavigate();
  return <>
    <div data-testid="location">{location.search}</div>
    <button onClick={() => navigate("/logs?model=gpt-image-2&q=req-image&upstream_request_id=up-image")}>Follow log link</button>
  </>;
}

function renderLogs(initialEntry = "/logs", rows?: unknown[]) {
  const requests: URLSearchParams[] = [];
  const histogramRequests: URLSearchParams[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    let body: unknown = [];
    if (url.pathname === "/admin/proxy-logs") {
      requests.push(url.searchParams);
      body = rows ?? [
        { id: 2, request_id: "req-fast", model: "fast-model", status: 200, latency_ms: 100, attempt: 1 },
        { id: 1, request_id: "req-slow", model: "slow-model", status: 502, latency_ms: 6000, attempt: 1 },
      ];
    } else if (url.pathname === "/admin/downstream-keys") {
      // The log page resolves downstream_key_id → name for both the token
      // filter and the expanded row's chain.
      body = [
        { id: 9, name: "cli-token", enabled: true, created_at: "2026-09-01T00:00:00Z" },
        { id: 4, name: "web-app", enabled: true, created_at: "2026-09-01T00:00:00Z" },
      ];
    } else if (url.pathname.endsWith("/latency-histogram")) {
      histogramRequests.push(url.searchParams);
      body = {
        buckets: [1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0],
        total: 2,
        slow_count: 1,
        p50_ms: 100,
        p95_ms: 6000,
        p99_ms: 6000,
        matched: 2,
        sample_size: 20000,
      };
    }
    return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
  }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(<QueryClientProvider client={queryClient}><I18nProvider><SessionProvider>
    <MemoryRouter initialEntries={[initialEntry]}><Logs /><LocationProbe /></MemoryRouter>
  </SessionProvider></I18nProvider></QueryClientProvider>);
  return { requests, histogramRequests, ...utils };
}

describe("proxy log filters", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

  it("applies search, model and upstream request ID together", async () => {
    const { requests } = renderLogs();
    await screen.findByText("fast-model");
    expect(screen.queryByRole("button", { name: "Clear filters" })).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole("textbox", { name: "Search model, error, path, request ID" }), { target: { value: " quota " } });
    fireEvent.click(screen.getByRole("button", { name: "Exact filters" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Model" }), { target: { value: " gpt-image-2 " } });
    fireEvent.change(screen.getByRole("textbox", { name: "Upstream request ID" }), { target: { value: " up-123 " } });
    expect(requests).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(Object.fromEntries(requests.at(-1)!)).toMatchObject({ q: "quota", model: "gpt-image-2", upstream_request_id: "up-123" }));
  });

  it("submits the same filters from the form and clears slow-only too", async () => {
    const { requests } = renderLogs("/logs?model=old&q=quota&upstream_request_id=up-old&status=failed");
    await screen.findByText("fast-model");
    fireEvent.click(screen.getByRole("checkbox", { name: "Slow only (≥5s)" }));
    expect(screen.queryByText("fast-model")).not.toBeInTheDocument();
    expect(screen.getByText("slow-model")).toBeInTheDocument();
    const upstream = screen.getByRole("textbox", { name: "Upstream request ID" });
    fireEvent.change(upstream, { target: { value: "up-new" } });
    fireEvent.submit(upstream.closest("form")!);
    await waitFor(() => expect(requests.at(-1)!.get("upstream_request_id")).toBe("up-new"));
    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.getByRole("checkbox", { name: "Slow only (≥5s)" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Failures only" })).not.toBeChecked();
    expect(screen.getByTestId("location")).toBeEmptyDOMElement();
    expect(await screen.findByText("fast-model")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Clear filters" })).not.toBeInTheDocument();
  });

  it("keeps Clear available when an applied filter is erased but not submitted", async () => {
    renderLogs("/logs?model=old");
    await screen.findByText("fast-model");
    fireEvent.change(screen.getByRole("textbox", { name: "Model" }), { target: { value: "" } });
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeInTheDocument();
  });

  // "Which token made this call" is the one dimension the row cannot imply:
  // the id is logged, the name is not, so both the filter and the expanded row
  // read it back from the token list.
  it("filters the list by client token and clears it again", async () => {
    const { requests } = renderLogs();
    await screen.findByText("fast-model");
    fireEvent.click(screen.getByRole("button", { name: "Exact filters" }));
    fireEvent.change(screen.getByRole("combobox", { name: "Token" }), { target: { value: "9" } });
    await waitFor(() => expect(requests.at(-1)!.get("downstream_key_id")).toBe("9"));
    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => expect(requests.at(-1)!.get("downstream_key_id")).toBeNull());
    expect(screen.getByRole("combobox", { name: "Token" })).toHaveValue("0");
  });

  it("opens the exact filters when the URL already carries a token filter", async () => {
    renderLogs("/logs?downstream_key_id=9");
    await screen.findByText("fast-model");
    expect(screen.getByRole("combobox", { name: "Token" })).toHaveValue("9");
  });

  it("updates drafts when a link navigates to different log filters", async () => {
    renderLogs("/logs?model=old");
    await screen.findByText("fast-model");
    fireEvent.click(screen.getByRole("button", { name: "Follow log link" }));
    expect(screen.getByRole("textbox", { name: "Model" })).toHaveValue("gpt-image-2");
    expect(screen.getByRole("textbox", { name: "Search model, error, path, request ID" })).toHaveValue("req-image");
    expect(screen.getByRole("textbox", { name: "Upstream request ID" })).toHaveValue("up-image");
  });
});

describe("log row drill-down", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

  // A row answers "what happened"; the operator still has to ask "for whom,
  // through what, and to which endpoint". The expansion carries that chain plus
  // the forensics the collapsed row has no room for.
  it("expands into the request chain and the row's details", async () => {
    renderLogs("/logs", [
      {
        id: 5, request_id: "req-chain", model: "gpt-5", status: 200, latency_ms: 1472,
        attempt: 2, channel_id: 3, route_id: 7, route_pattern: "gpt-*",
        path: "chat/completions", client_family: "claude-cli", stream: true,
        first_byte_ms: 300, downstream_key_id: 9, session_key: "sess-1",
        upstream_url: "https://up.example.com/v1/chat/completions",
        upstream_model: "gpt-5-real", upstream_request_id: "up-777",
        key_fingerprint: "9f2c41ab77de0088", tokens_per_second: 42.5,
        upstream_key_id: 141, upstream_key_name: "cc",
        prompt_tokens: 100, completion_tokens: 20, total_tokens: 120,
        reasoning_effort: "max", mapped_reasoning_effort: "high",
        created_at: "2026-09-30T10:00:00Z",
      },
    ]);
    fireEvent.click(await screen.findByText("gpt-5"));

    expect(await screen.findByText("Request chain")).toBeInTheDocument();
    // Scoped to the chain: the token filter select carries the same name as an
    // <option>, and the collapsed row is still on screen.
    const chain = document.querySelector(".log-chain") as HTMLElement;
    const details = document.querySelector(".log-details") as HTMLElement;
    // Token id → name, read back from the token list.
    expect(within(chain).getByText("cli-token")).toBeInTheDocument();
    // The hop is a link to the token, through the Keys page's search box —
    // that page has no per-row detail view to link to.
    expect(within(chain).getByRole("link", { name: "cli-token" })).toHaveAttribute(
      "href",
      "/keys?search=cli-token",
    );
    // The stored path is the relay's endpoint name; the client's ingress path
    // has the /v1 prefix it is mounted under.
    expect(within(chain).getByText("/v1/chat/completions")).toBeInTheDocument();
    expect(within(chain).getByText("https://up.example.com/v1/chat/completions")).toBeInTheDocument();
    // Which upstream key served it: the id is stored, the name is joined in, and
    // the hop goes straight to that channel's key list (?keys= deep-link).
    expect(within(chain).getByText("cc")).toBeInTheDocument();
    expect(within(chain).getByText("#141")).toBeInTheDocument();
    expect(within(chain).getByRole("link", { name: "cc" })).toHaveAttribute(
      "href",
      "/channels?keys=3",
    );
    // Details: what the upstream called it, what it cost, which key served it.
    expect(within(details).getByText("up-777")).toBeInTheDocument();
    expect(within(details).getByText("42.5 tok/s")).toBeInTheDocument();
    expect(within(details).getByText("9f2c41ab77de0088")).toBeInTheDocument();
    expect(within(details).getByText("max → high")).toBeInTheDocument();
    expect(within(details).getByText("100 / 20")).toBeInTheDocument();
  });

  // The bare grey bar in front of the number was an unreadable scale: the
  // tooltip has to say what it measures and what the colours mean.
  it("explains the latency bar on hover", async () => {
    renderLogs("/logs", [
      { id: 5, request_id: "req-bar", model: "gpt-5", status: 200, latency_ms: 1472, attempt: 1, channel_id: 3 },
    ]);
    await screen.findByText("gpt-5");
    expect(
      screen.getByTitle("The bar scales 0–10s: this row is 1472 ms (15%). Amber past 1s, red past 5s."),
    ).toBeInTheDocument();
  });

  // An UNNAMED key (most of the live DB: 41 of 78 credentials carry no name)
  // falls back to its id — which must not then repeat itself in the note.
  it("shows an unnamed upstream key as its id, without repeating the id", async () => {
    renderLogs("/logs", [
      {
        id: 8, request_id: "req-unnamed", model: "gpt-5", status: 200, latency_ms: 700,
        attempt: 1, channel_id: 3, upstream_key_id: 181,
        key_fingerprint: "76cd89af5347720d",
      },
    ]);
    fireEvent.click(await screen.findByText("gpt-5"));
    await screen.findByText("Request chain");
    const chain = document.querySelector(".log-chain") as HTMLElement;
    const hop = chain.querySelectorAll(".log-chain-step")[4] as HTMLElement;
    expect(hop.querySelector(".log-chain-label")?.textContent).toBe("Upstream key");
    expect(hop.querySelector(".log-chain-value")?.textContent).toBe("#181");
    expect(hop.querySelector(".log-chain-note")).toBeNull();
  });

  // A row written before the id column existed still knows the key by hash.
  // Showing a blank hop would read as "no key was used", so the fingerprint
  // takes the value slot and the note says why.
  it("falls back to the key fingerprint on a row with no key id", async () => {
    renderLogs("/logs", [
      {
        id: 7, request_id: "req-legacy", model: "gpt-5", status: 200, latency_ms: 900,
        attempt: 1, channel_id: 3, key_fingerprint: "9f2c41ab77de0088",
      },
    ]);
    fireEvent.click(await screen.findByText("gpt-5"));
    await screen.findByText("Request chain");
    const chain = document.querySelector(".log-chain") as HTMLElement;
    expect(within(chain).getByText("9f2c41ab77de0088")).toBeInTheDocument();
    expect(
      within(chain).getByText("fingerprint only — this row predates key logging"),
    ).toBeInTheDocument();
  });

  // A failed attempt meters no tokens: "0 / 0" would read like a model that
  // answered with none, so the split stays out of the sheet.
  it("omits the token split for an attempt that metered nothing", async () => {
    renderLogs("/logs", [
      { id: 6, request_id: "req-fail", model: "gpt-5", status: 503, latency_ms: 403, attempt: 1, channel_id: 3, error_detail: '{"error":"busy"}' },
    ]);
    fireEvent.click(await screen.findByText("gpt-5"));
    await screen.findByText("Details");
    expect(screen.queryByText("0 / 0")).not.toBeInTheDocument();
    expect(screen.getByText("{\"error\":\"busy\"}")).toBeInTheDocument();
  });
});

describe("log page reading order", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

  // "How many rows, how many of them failed" is the question the page opens
  // with, so the readouts sit above every panel rather than between two.
  it("opens with the readout strip, above the distribution", async () => {
    const { container } = renderLogs();
    await screen.findByText("fast-model");
    const strip = container.querySelector(".logs-overview");
    const panel = container.querySelector(".latency-panel");
    expect(strip).not.toBeNull();
    expect(panel).not.toBeNull();
    expect(
      strip!.compareDocumentPosition(panel!) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  // The distribution used to be a collapsed strip under the table, which made
  // "what is slow right now" the last thing anyone saw.
  it("renders the distribution above the log list", async () => {
    const { container } = renderLogs();
    await screen.findByText("fast-model");
    const panel = container.querySelector(".latency-panel");
    const list = container.querySelector(".logs-split");
    expect(panel).not.toBeNull();
    expect(list).not.toBeNull();
    expect(
      panel!.compareDocumentPosition(list!) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    // Expanded by default, with the readouts visible rather than hidden.
    expect(screen.getByText("p95")).toBeInTheDocument();
    expect(screen.getByText("p99")).toBeInTheDocument();
  });

  it("scopes both the list and the distribution to the selected range", async () => {
    const { requests, histogramRequests } = renderLogs();
    await screen.findByText("fast-model");
    fireEvent.click(screen.getByRole("tab", { name: "15 min" }));
    await waitFor(() => {
      const last = requests.at(-1)!;
      expect(last.get("since")).toBeTruthy();
      expect(last.get("until")).toBeTruthy();
    });
    await waitFor(() => {
      const last = histogramRequests.at(-1)!;
      expect(last.get("since")).toBeTruthy();
      expect(last.get("until")).toBeTruthy();
      // A bounded window asks for a real sample rather than the newest 1,000.
      expect(last.get("sample")).toBe("20000");
    });
  });

  it("keeps a custom absolute window in the URL so it survives a reload", async () => {
    renderLogs();
    await screen.findByText("fast-model");
    fireEvent.click(screen.getByRole("tab", { name: "Custom" }));
    fireEvent.change(screen.getByLabelText("From"), {
      target: { value: "2026-09-17T10:00:00" },
    });
    fireEvent.change(screen.getByLabelText("To"), {
      target: { value: "2026-09-17T11:30:00" },
    });
    await waitFor(() =>
      expect(screen.getByTestId("location").textContent).toContain("range=custom"),
    );
    const search = screen.getByTestId("location").textContent ?? "";
    // Second precision is browser-dependent (`step=1` may normalize ":00"
    // away), so assert the minute the user actually picked.
    expect(search).toContain("from=2026-09-17T10%3A00");
    expect(search).toContain("to=2026-09-17T11%3A30");
  });

  it("warns instead of querying an inverted custom window", async () => {
    renderLogs();
    await screen.findByText("fast-model");
    fireEvent.click(screen.getByRole("tab", { name: "Custom" }));
    fireEvent.change(screen.getByLabelText("From"), {
      target: { value: "2026-09-17T12:00:00" },
    });
    fireEvent.change(screen.getByLabelText("To"), {
      target: { value: "2026-09-17T09:00:00" },
    });
    expect(
      screen.getByText("The end time is before the start time"),
    ).toBeInTheDocument();
  });
});

describe("shared alias attribution", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

  // One client-facing alias can be served by several real upstream models, so
  // after the fact the row is the only place that says which one ran. Losing
  // this makes a round-robin alias unaccountable.
  it("names the real upstream model when an alias stood in for it", async () => {
    renderLogs("/logs", [
      { id: 3, request_id: "req-a", model: "unified", upstream_model: "claude-sonnet-4", status: 200, latency_ms: 10, attempt: 1 },
      { id: 2, request_id: "req-b", model: "unified", upstream_model: "gpt-5-mini", status: 200, latency_ms: 10, attempt: 1 },
      { id: 1, request_id: "req-c", model: "plain", upstream_model: "plain", status: 200, latency_ms: 10, attempt: 1 },
    ]);

    expect(await screen.findByText("origin claude-sonnet-4")).toBeInTheDocument();
    expect(screen.getByText("origin gpt-5-mini")).toBeInTheDocument();
    // The alias is still what the caller sent, so it stays the row's headline.
    expect(screen.getAllByText("unified")).toHaveLength(2);
    // No rewrite happened here — repeating the name as a badge is pure noise.
    expect(screen.queryByText("origin plain")).not.toBeInTheDocument();
  });
});
