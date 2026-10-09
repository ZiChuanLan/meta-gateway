import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { SiteNewsPanel } from "./SiteNewsPanel";

const SEEN_KEY = "meta-gateway.site-news-seen";

const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const item = (overrides: Record<string, unknown> = {}) => ({
  id: 1,
  site_id: 3,
  site_name: "WONG",
  upstream_id: "95",
  content: "codex分组模型仅允许codex客户端使用",
  extra: "",
  kind: "default",
  published_at: "2026-10-08T14:22:58.312Z",
  first_seen_at: "2026-10-09T17:00:00.000Z",
  fetched_at: "2026-10-09T17:00:00.000Z",
  ...overrides,
});

function setup(feed: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path === "/admin/site-news") return response(feed);
      if (path === "/admin/site-news/refresh") {
        return response({
          sites: 22,
          fetched: 18,
          failed: 4,
          added: 0,
          at: "2026-10-09T17:00:00Z",
        });
      }
      return response({ error: `unexpected ${path}` }, 500);
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <SessionProvider>
          <SiteNewsPanel />
        </SessionProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("upstream notices panel", () => {
  beforeEach(() => {
    // A signed-in console: the panel reads the admin API, so the session has to
    // hand it a client. The fetch itself is stubbed above.
    localStorage.setItem("meta-gateway.admin-token", "test-token");
    // The assertions below read the console's Chinese copy.
    localStorage.setItem("meta-gateway.locale", "zh-CN");
    // `shouldAdvanceTime` keeps testing-library's waitFor alive: with a hard fake
    // clock its own polling never fires.
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it("marks notices first seen since the last visit as new", async () => {
    // Read yesterday: everything the gateway first saw today is new to a reader.
    localStorage.setItem(SEEN_KEY, "2026-10-08T00:00:00.000Z");
    setup({
      items: [
        item(),
        item({ id: 2, site_name: "CoeeApi", first_seen_at: "2026-10-07T00:00:00.000Z" }),
      ],
      sites: { readable: 22, reported: 12 },
      last_fetched: "2026-10-09T17:00:00Z",
    });

    await waitFor(() => expect(screen.getByText("WONG")).toBeTruthy());
    // Exactly one badge: the row the gateway read after the stored stamp.
    await waitFor(() => expect(screen.getAllByText("新")).toHaveLength(1));
    expect(screen.getByText("上游站点消息")).toBeTruthy();
    expect(screen.getByText("22 个站点可读 · 12 个有公告")).toBeTruthy();
  });

  it("moves the read stamp a few seconds later, so the badge lasts one visit", async () => {
    localStorage.setItem(SEEN_KEY, "2026-10-08T00:00:00.000Z");
    setup({ items: [item()], sites: { readable: 22, reported: 1 } });

    await waitFor(() => expect(screen.getByText("WONG")).toBeTruthy());
    expect(screen.getAllByText("新")).toHaveLength(1);
    expect(localStorage.getItem(SEEN_KEY)).toBe("2026-10-08T00:00:00.000Z");

    await act(async () => {
      vi.advanceTimersByTime(6_000);
    });
    expect(localStorage.getItem(SEEN_KEY)).not.toBe("2026-10-08T00:00:00.000Z");
  });

  it("says what the boards cover when nothing has been published", async () => {
    setup({ items: [], sites: { readable: 22, reported: 0 } });
    await waitFor(() => expect(screen.getByText("还没有抓到站点公告。")).toBeTruthy());
    expect(screen.getByText("22 个站点可读 · 0 个有公告")).toBeTruthy();
  });
});
