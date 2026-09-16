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
      body = { buckets: [1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0], total: 2, slow_count: 1, p50_ms: 100, p95_ms: 6000 };
    }
    return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
  }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><I18nProvider><SessionProvider>
    <MemoryRouter initialEntries={[initialEntry]}><Logs /><LocationProbe /></MemoryRouter>
  </SessionProvider></I18nProvider></QueryClientProvider>);
  return requests;
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
    const requests = renderLogs();
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
    const requests = renderLogs("/logs?model=old&q=quota&upstream_request_id=up-old&status=failed");
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
