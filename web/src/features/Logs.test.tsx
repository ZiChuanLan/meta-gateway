import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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

function renderLogs(initialEntry = "/logs") {
  const requests: URLSearchParams[] = [];
  const histogramRequests: URLSearchParams[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    let body: unknown = [];
    if (url.pathname === "/admin/proxy-logs") {
      requests.push(url.searchParams);
      body = [
        { id: 2, request_id: "req-fast", model: "fast-model", status: 200, latency_ms: 100, attempt: 1 },
        { id: 1, request_id: "req-slow", model: "slow-model", status: 502, latency_ms: 6000, attempt: 1 },
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

  it("updates drafts when a link navigates to different log filters", async () => {
    renderLogs("/logs?model=old");
    await screen.findByText("fast-model");
    fireEvent.click(screen.getByRole("button", { name: "Follow log link" }));
    expect(screen.getByRole("textbox", { name: "Model" })).toHaveValue("gpt-image-2");
    expect(screen.getByRole("textbox", { name: "Search model, error, path, request ID" })).toHaveValue("req-image");
    expect(screen.getByRole("textbox", { name: "Upstream request ID" })).toHaveValue("up-image");
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
