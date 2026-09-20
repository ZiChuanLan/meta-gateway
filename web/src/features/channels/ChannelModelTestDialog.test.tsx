import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { ToastProvider } from "../../toast";
import { ChannelModelTestDialog } from "./ChannelModelTestDialog";

const MODELS = [
  { name: "gpt-5", adopted: true },
  { name: "claude-sonnet-4", adopted: false },
  { name: "gemini-3-pro", adopted: false },
  { name: "deepseek-v3", adopted: false },
  { name: "qwen3-max", adopted: false },
  { name: "llama-4", adopted: false },
];

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function renderDialog(models = MODELS) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <ChannelModelTestDialog
              channelId={7}
              channelName="api.example.com"
              models={models}
              onClose={() => {}}
            />
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

/** Records every smoke-test call and lets the test release them by hand. */
function stubDeferredTests() {
  const calls: Array<{ model: string; body: Record<string, unknown> }> = [];
  const releases: Array<() => void> = [];
  let inFlight = 0;
  let peak = 0;

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).split("?")[0] ?? "";
    const method = (init?.method ?? "GET").toUpperCase();
    if (path === "/admin/try/channel-model" && method === "POST") {
      const body = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
      calls.push({ model: String(body.model), body });
      inFlight += 1;
      peak = Math.max(peak, inFlight);
      await new Promise<void>((resolve) => releases.push(resolve));
      inFlight -= 1;
      return jsonResponse({
        model: body.model,
        ok: true,
        status_code: 200,
        latency_ms: 120,
      });
    }
    return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
  });
  vi.stubGlobal("fetch", fetchMock);

  const flush = async () => {
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
  };

  /** Let the queued workers dispatch, then release everything until idle. */
  const drain = async () => {
    for (let guard = 0; guard < 50; guard += 1) {
      await flush();
      const next = releases.shift();
      if (next === undefined) return;
      next();
    }
    throw new Error("smoke test never settled");
  };

  return {
    calls,
    releases,
    flush,
    drain,
    peak: () => peak,
  };
}

describe("ChannelModelTestDialog", () => {
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

  it("tests a single candidate model without adopting it", async () => {
    const harness = stubDeferredTests();
    renderDialog();

    // The row is offered for a model the channel does not serve yet — the
    // whole reason the check bypasses routing.
    const row = screen.getByText("claude-sonnet-4").closest("li");
    expect(row).not.toBeNull();
    const run = row!.querySelector("button");
    expect(run).not.toBeNull();
    run!.click();
    await harness.flush();

    expect(harness.calls).toHaveLength(1);
    expect(harness.calls[0]?.model).toBe("claude-sonnet-4");
    expect(harness.calls[0]?.body).toMatchObject({
      channel_id: 7,
      max_tokens: 1,
    });

    harness.releases.shift()?.();
    await waitFor(() =>
      expect(screen.getByText("Passed")).toBeInTheDocument(),
    );
    expect(screen.getByText("120 ms")).toBeInTheDocument();
  });

  it("bounds test-all concurrency and reports every verdict", async () => {
    const harness = stubDeferredTests();
    renderDialog();

    screen.getByRole("button", { name: /^test all$/i }).click();
    // Six models, four workers: the channel must never see more than four
    // concurrent synthetic calls.
    for (let tick = 0; tick < 6; tick += 1) await harness.flush();
    expect(harness.releases).toHaveLength(4);
    expect(harness.peak()).toBe(4);

    await harness.drain();

    expect(harness.calls.map((call) => call.model).sort()).toEqual([
      "claude-sonnet-4",
      "deepseek-v3",
      "gemini-3-pro",
      "gpt-5",
      "llama-4",
      "qwen3-max",
    ]);
    await waitFor(() =>
      expect(screen.getAllByText("Passed")).toHaveLength(MODELS.length),
    );
  });

  it("surfaces a refusal and can filter down to the failures", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input).split("?")[0] ?? "";
        if (path === "/admin/try/channel-model") {
          const body = JSON.parse(String(init?.body ?? "{}")) as {
            model?: string;
          };
          return body.model === "gpt-5"
            ? jsonResponse({
                model: "gpt-5",
                ok: true,
                status_code: 200,
                latency_ms: 120,
              })
            : jsonResponse({
                model: "claude-sonnet-4",
                ok: false,
                status_code: 404,
                latency_ms: 88,
                error: "upstream status 404: no such model",
              });
        }
        return jsonResponse({ error: `unexpected ${path}` }, 500);
      }),
    );

    renderDialog([
      { name: "gpt-5", adopted: true },
      { name: "claude-sonnet-4", adopted: false },
    ]);

    screen.getByRole("button", { name: /^test all$/i }).click();

    await waitFor(() =>
      expect(screen.getByText("upstream status 404: no such model")).toBeInTheDocument(),
    );
    expect(screen.getAllByText("Passed")).toHaveLength(1);
    expect(screen.getAllByText("Failed")).toHaveLength(1);

    // The failures-only view is offered once there is something to narrow to.
    const filter = screen.getByRole("button", { name: /failures only/i });
    expect(filter).toBeEnabled();
    filter.click();
    await waitFor(() =>
      expect(screen.queryByText("gpt-5")).not.toBeInTheDocument(),
    );
    expect(screen.getByText("claude-sonnet-4")).toBeInTheDocument();
  });

  it("stops dispatching new tests when the operator stops the run", async () => {
    const harness = stubDeferredTests();
    renderDialog();

    screen.getByRole("button", { name: /^test all$/i }).click();
    for (let tick = 0; tick < 6; tick += 1) await harness.flush();
    expect(harness.calls).toHaveLength(4);

    screen.getByRole("button", { name: /^stop$/i }).click();
    await harness.drain();

    // The four in-flight probes finish; the remaining two are never sent.
    expect(harness.calls).toHaveLength(4);
  });
});
