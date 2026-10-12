import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StrictMode } from "react";
import { Route, Routes, RouterProvider, createMemoryRouter } from "react-router-dom";
import type { WebDAVSettings, WebDAVStatus } from "../api/types";
import { I18nProvider } from "../i18n";
import { UnsavedChangesBoundary } from "../lib/RouterBoundary";
import { SessionProvider } from "../session";
import { ToastProvider } from "../toast";
import { Exchange } from "./Exchange";

const CHANNELS = [
  {
    id: 1,
    name: "Alpha site",
    base_url: "https://alpha.example",
    group_name: "default",
    status: "enabled",
  },
  {
    id: 2,
    name: "Beta site",
    base_url: "https://beta.example",
    group_name: "default",
    status: "enabled",
  },
];

function settings(overrides: Partial<WebDAVSettings> = {}): WebDAVSettings {
  return {
    enabled: false,
    upload_enabled: false,
    url: "https://stored.example/dav/",
    username: "",
    has_password: false,
    has_backup_password: false,
    upload_url: "",
    upload_username: "",
    has_upload_password: false,
    has_upload_backup_password: false,
    cron: "off",
    download_cron: "off",
    upload_cron: "off",
    download_scheduler_armed: false,
    upload_scheduler_armed: false,
    download_configured: false,
    upload_configured: false,
    configured: false,
    scheduler_armed: false,
    source: "store",
    ...overrides,
  };
}

function webdavStatus(): WebDAVStatus {
  return { configured: false, scheduler_armed: false, in_progress: false };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** What the fake server answers on the next settings GET / PUT. */
let settingsReply: () => WebDAVSettings = () => settings();
let putReply: () => Response | Promise<Response> = () => json(settings());
let putBodies: unknown[] = [];

function installFetch() {
  settingsReply = () => settings();
  putReply = () => json(settings());
  putBodies = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://localhost").pathname;
      const method = (init?.method ?? "GET").toUpperCase();
      if (path === "/admin/channels") return json(CHANNELS);
      if (path === "/admin/webdav/status") return json(webdavStatus());
      if (path === "/admin/webdav/settings" && method === "GET") return json(settingsReply());
      if (path === "/admin/webdav/settings" && method === "PUT") {
        putBodies.push(JSON.parse(String(init?.body ?? "{}")));
        return putReply();
      }
      return json({ error: `unexpected ${method} ${path}` }, 500);
    }),
  );
}

/**
 * The production shape: the exchange page under one data router whose single
 * splat route carries the unsaved-changes guard, with App's own <Routes>
 * underneath. StrictMode matches main.tsx.
 */
function mount(options: { initialEntries?: string[] } = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const router = createMemoryRouter(
    [
      {
        path: "*",
        element: (
          <UnsavedChangesBoundary>
            <Routes>
              <Route path="/exchange" element={<Exchange />} />
              <Route path="/other" element={<span data-testid="other">other</span>} />
            </Routes>
          </UnsavedChangesBoundary>
        ),
      },
    ],
    { initialEntries: options.initialEntries ?? ["/exchange"] },
  );
  render(
    <StrictMode>
      <QueryClientProvider client={client}>
        <I18nProvider>
          <ToastProvider>
            <SessionProvider>
              <RouterProvider router={router} />
            </SessionProvider>
          </ToastProvider>
        </I18nProvider>
      </QueryClientProvider>
    </StrictMode>,
  );
  return { router, client };
}

/** The cloud task renders the two directions in DOM order: download first. */
function directionCard(index: number): HTMLElement {
  const card = document.querySelectorAll(".webdav-direction-card")[index];
  if (!card) throw new Error(`No WebDAV direction card at index ${index}`);
  return card as HTMLElement;
}

function downloadUrlField(): HTMLElement {
  return within(directionCard(0)).getByLabelText("Cloud folder link");
}

describe("exchange task workspace", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
    installFetch();
  });

  afterEach(() => {
    cleanup();
    localStorage.clear();
    sessionStorage.clear();
    vi.unstubAllGlobals();
  });

  it("puts every task on one page, with the format legend folded", async () => {
    mount();
    // Import is where the file picker lives, so it is reachable without tabs.
    expect(await screen.findByRole("heading", { name: "Import assets" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Export channels" })).toBeVisible();
    expect(screen.getByText("Cloud backup sync")).toBeVisible();
    // The format legend is reference material: mounted, folded and closed.
    expect(screen.getByText("What can I import?").closest("details")).not.toHaveAttribute("open");
  });

  it("keeps the WebDAV draft without persisting on the way", async () => {
    mount();
    const field = downloadUrlField();
    await waitFor(() => expect(field).toHaveValue("https://stored.example/dav/"));
    fireEvent.change(field, { target: { value: "https://typed.example/dav/" } });

    // Scrolling past the other panels is not a save: nothing is persisted.
    expect(putBodies).toHaveLength(0);
    // The form never unmounts, so the typed value is still there.
    expect(downloadUrlField()).toHaveValue("https://typed.example/dav/");
  });

  it("keeps the export channel selection while the rest of the page is in view", async () => {
    mount();
    const alpha = await screen.findByRole("checkbox", { name: /Alpha site/ });
    fireEvent.click(alpha);
    expect(alpha).toBeChecked();
    // Touching another panel does not remount the export selection.
    fireEvent.change(downloadUrlField(), { target: { value: "https://typed.example/dav/" } });
    expect(screen.getByRole("checkbox", { name: /Alpha site/ })).toBeChecked();
  });

  it("does not let a background settings refresh overwrite a dirty draft", async () => {
    const { client } = mount({ initialEntries: ["/exchange?task=cloud"] });
    const field = downloadUrlField();
    await waitFor(() => expect(field).toHaveValue("https://stored.example/dav/"));
    fireEvent.change(field, { target: { value: "https://typed.example/dav/" } });

    settingsReply = () => settings({ url: "https://server.example/dav/" });
    await act(async () => {
      await client.invalidateQueries({ queryKey: ["webdav-settings"] });
    });

    expect(downloadUrlField()).toHaveValue("https://typed.example/dav/");
  });

  it("keeps the draft when a real save fails", async () => {
    mount({ initialEntries: ["/exchange?task=cloud"] });
    const field = downloadUrlField();
    await waitFor(() => expect(field).toHaveValue("https://stored.example/dav/"));
    fireEvent.change(field, { target: { value: "https://typed.example/dav/" } });

    putReply = () => json({ error: "webdav_unreachable" }, 502);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await screen.findByRole("alert");
    expect(putBodies).toHaveLength(1);
    expect(downloadUrlField()).toHaveValue("https://typed.example/dav/");
  });

  it("treats a successful save as clean, so leaving needs no prompt", async () => {
    const { router } = mount({ initialEntries: ["/exchange?task=cloud"] });
    const field = downloadUrlField();
    await waitFor(() => expect(field).toHaveValue("https://stored.example/dav/"));
    fireEvent.change(field, { target: { value: "https://typed.example/dav/" } });

    // The server confirms the save; a stale cached document must not immediately
    // overwrite it while the refresh is still pending.
    const saved = settings({ url: "https://typed.example/dav/", has_password: true });
    settingsReply = () => saved;
    putReply = () => json(saved);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByText("Saved.");
    expect(downloadUrlField()).toHaveValue("https://typed.example/dav/");

    const confirm = vi.fn(() => true);
    vi.stubGlobal("confirm", confirm);
    await act(async () => {
      await router.navigate("/other");
    });
    expect(confirm).not.toHaveBeenCalled();
    expect(await screen.findByTestId("other")).toBeInTheDocument();
  });

  it("holds a dirty page and its draft when the operator declines to leave", async () => {
    const { router } = mount({ initialEntries: ["/exchange?task=cloud"] });
    const field = downloadUrlField();
    await waitFor(() => expect(field).toHaveValue("https://stored.example/dav/"));
    fireEvent.change(field, { target: { value: "https://typed.example/dav/" } });

    const confirm = vi.fn(() => false);
    vi.stubGlobal("confirm", confirm);
    await act(async () => {
      await router.navigate("/other");
    });

    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId("other")).not.toBeInTheDocument();
    expect(downloadUrlField()).toHaveValue("https://typed.example/dav/");
  });
});

it("preserves newer input while testing a connection persists an earlier draft", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  localStorage.setItem("meta-gateway.admin-token", "test-token");
  installFetch();
  const original = fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) =>
      String(input).includes("/admin/webdav/test")
        ? Promise.resolve(json({ ok: true }))
        : original(input, init),
    ),
  );
  mount({ initialEntries: ["/exchange?task=cloud"] });
  const field = downloadUrlField();
  await waitFor(() => expect(field).toHaveValue("https://stored.example/dav/"));
  fireEvent.change(field, { target: { value: "https://first.example/dav/" } });
  let finish!: (r: Response) => void;
  putReply = () =>
    new Promise<Response>((resolve) => {
      finish = resolve;
    });
  fireEvent.click(within(directionCard(0)).getByRole("button", { name: /Test connection/ }));
  await waitFor(() => expect(putBodies).toHaveLength(1));
  fireEvent.change(field, { target: { value: "https://newer.example/dav/" } });
  await act(async () => finish(json(settings({ url: "https://first.example/dav/" }))));
  expect(field).toHaveValue("https://newer.example/dav/");
});
