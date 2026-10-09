import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../i18n";
import { downloadText } from "../../lib/csv";
import type { ProxyLog } from "../../api/types";
import { LogsView } from "../Logs";
import { MEMBER_LOG_CAPS, type LogsSource } from "./LogsSource";

vi.mock("../../lib/csv", async (original) => ({
  ...(await original<typeof import("../../lib/csv")>()),
  downloadText: vi.fn(),
}));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
it("keeps time filters and refresh without a phantom histogram or upstream filters", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  const logs = vi.fn().mockResolvedValue([]);
  const source: LogsSource = { logs, keys: vi.fn().mockResolvedValue([]) };
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(
    <QueryClientProvider client={qc}>
      <I18nProvider>
        <MemoryRouter>
          <LogsView source={source} caps={MEMBER_LOG_CAPS} />
        </MemoryRouter>
      </I18nProvider>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(logs).toHaveBeenCalledTimes(1));
  expect(screen.getByText("Time range")).toBeInTheDocument();
  expect(screen.queryByText("Latency distribution")).not.toBeInTheDocument();
  expect(container.querySelector(".latency-panel .dashboard-empty")).toBeNull();
  expect(screen.queryByText("All channels")).not.toBeInTheDocument();
  expect(container.querySelector("input[aria-label='Upstream request ID']")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  // One fetch: re-anchoring a rolling window moves the query key, and that IS
  // the refresh.
  await waitFor(() => expect(logs).toHaveBeenCalledTimes(2));
  expect(qc.getQueryState(["proxy-log-histogram"])?.error).toBeUndefined();
});

it("retains the same histogram when an account-scoped source provides it", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  const histogram = vi.fn().mockResolvedValue({
    buckets: [1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
    total: 1,
    slow_count: 0,
    p50_ms: 100,
    p95_ms: 100,
    p99_ms: 100,
    matched: 1,
    sample_size: 1000,
  });
  const source: LogsSource = {
    logs: vi.fn().mockResolvedValue([]),
    keys: vi.fn().mockResolvedValue([]),
    latencyHistogram: histogram,
  };
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <I18nProvider>
        <MemoryRouter>
          <LogsView source={source} caps={MEMBER_LOG_CAPS} />
        </MemoryRouter>
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByRole("img")).toBeInTheDocument();
  expect(screen.getByText("Latency distribution")).toBeInTheDocument();
  expect(histogram).toHaveBeenCalledTimes(1);
  expect(screen.queryByText("All channels")).toBeNull();
});

it("exports the visible slow-only member rows without administrator columns", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  const rows = [
    {
      id: 1,
      request_id: "fast-id",
      model: "fast-model",
      status: 200,
      latency_ms: 100,
      attempt: 1,
      channel_id: 0,
      created_at: "2026-10-05T00:00:00Z",
    },
    {
      id: 2,
      request_id: "slow-id",
      model: "slow-model",
      status: 200,
      latency_ms: 6000,
      attempt: 1,
      channel_id: 0,
      created_at: "2026-10-05T00:00:00Z",
    },
  ] as ProxyLog[];
  const source: LogsSource = {
    logs: vi.fn().mockResolvedValue(rows),
    keys: vi.fn().mockResolvedValue([]),
  };
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <I18nProvider>
        <MemoryRouter>
          <LogsView source={source} caps={MEMBER_LOG_CAPS} />
        </MemoryRouter>
      </I18nProvider>
    </QueryClientProvider>,
  );
  await screen.findByText("slow-model");
  fireEvent.click(screen.getByRole("checkbox", { name: /slow/i }));
  fireEvent.click(screen.getByRole("button", { name: /export/i }));
  const content = String(vi.mocked(downloadText).mock.calls[0]?.[1]);
  expect(content).toContain("slow-id");
  expect(content).not.toContain("fast-id");
  const header = content.split("\n")[0];
  expect(header).not.toMatch(/channel|route|upstream/i);
});
