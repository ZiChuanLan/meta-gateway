import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { ToastProvider } from "../toast";
import { Store } from "./Store";

const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const hosted = {
  id: "opencode-host",
  name: "订阅账号宿主",
  version: "0.3.0",
  kind: "addon",
  source: "sidecar",
  installed: true,
  enabled: true,
  can_toggle: true,
  meta_json: JSON.stringify({
    sidecar: { url: "http://127.0.0.1:9300", api_key: "plugin-key", channel_path: "/v1" },
  }),
};
const jev = {
  id: "jev-router",
  name: "Jev auto routing",
  version: "1.0.0",
  description: "Pick the model with TypeSafe Jev.",
  kind: "addon",
  source: "sidecar",
  installed: true,
  enabled: false,
  can_toggle: true,
};

/** The market lists TWO plugins: one installed (but switched off), one not. */
function setup() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path === "/admin/plugins/status") return response([jev, hosted]);
      if (path === "/admin/plugins/market")
        return response({
          sources: [{ id: "official", name: "ZiChuanLan", url: "https://example.com" }],
          plugins: [
            {
              id: "jev-router",
              name: "Jev auto routing",
              version: "1.0.0",
              description: "Pick the model with TypeSafe Jev.",
              author: "ZiChuanLan",
              url: "https://example.com/jev-router.zip",
              install: { type: "github-release" },
              source: { id: "official", name: "ZiChuanLan", url: "https://example.com" },
            },
            {
              id: "brand-new",
              name: "Brand new plugin",
              version: "2.0.0",
              url: "https://example.com/brand-new.zip",
              install: { type: "github-release" },
              source: { id: "official", name: "ZiChuanLan", url: "https://example.com" },
            },
          ],
        });
      if (path === "/admin/plugins/hooks") return response({ hooks: [] });
      if (path === "/admin/discovery/models-preview")
        return response({
          adapter: "openai",
          models: ["opencode-zen-free/big-pickle", "opencode-zen-free/deepseek-v4.1-flash-free"],
        });
      if (path === "/admin/connections")
        return response({
          channel: { id: 1 },
          site: { id: 1 },
          credential: { id: 1 },
          adopted: [],
        });
      // The create-channel button is gated on the sidecar record, so the
      // installed-plugin list has to carry the channel path too.
      if (path === "/admin/plugins")
        return response([{ id: "opencode-host", meta_json: hosted.meta_json }]);
      return response({ error: `unexpected ${path}` }, 500);
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <MemoryRouter initialEntries={["/console/store"]}>
              <Store />
            </MemoryRouter>
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function marketCard(name: string) {
  const card = screen
    .getAllByText(name)
    .map((node) => node.closest(".market-card"))
    .find((node) => node !== null);
  expect(card).toBeTruthy();
  return card as HTMLElement;
}

function moduleCard(name: string) {
  const card = screen
    .getAllByText(name)
    .map((node) => node.closest(".module-card"))
    .find((node) => node !== null);
  expect(card).toBeTruthy();
  return card as HTMLElement;
}

describe("extension store market cards", () => {
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

  // Installing and then switching a plugin off must not turn the market entry
  // back into an installable one: it is still installed, only disabled.
  it("keeps an installed-but-disabled plugin marked as installed in the market", async () => {
    setup();
    // Both cards come from the market payload, so wait for it before reading them.
    await screen.findByText("Brand new plugin");
    const card = marketCard("Jev auto routing");
    expect(within(card).queryByRole("button", { name: /Install/ })).not.toBeInTheDocument();
    expect(within(card).getByText("Installed · v1.0.0")).toBeInTheDocument();
    expect(within(card).getByText("Disabled")).toBeInTheDocument();
    expect(within(card).getByText(/currently off/)).toBeInTheDocument();
    // A plugin that is genuinely not installed still offers its install button.
    const fresh = marketCard("Brand new plugin");
    expect(within(fresh).getByRole("button", { name: /Install/ })).toBeInTheDocument();
    expect(within(fresh).queryByText(/Installed/)).not.toBeInTheDocument();
    // The disabled plugin is also the one listed as inactive (its activate
    // button lives there), so the store never hides where it went.
    await screen.findByText("Inactive & installable");
    const inactive = moduleCard("Jev auto routing");
    expect(within(inactive).getByRole("button", { name: /Activate/ })).toBeInTheDocument();
  });

  // The plugin answers /v1/models with what its signed-in accounts serve, so a
  // channel built from it must adopt that list instead of asking the operator
  // to type model names.
  it("creates the channel from the models the plugin reports", async () => {
    setup();
    await screen.findByText("订阅账号宿主");
    // An enabled plugin lives in the active list (a row), not a market card.
    const row = screen.getAllByText("订阅账号宿主")[0]?.closest(".plugin-row");
    if (!row) throw new Error("the active plugin row is missing");
    fireEvent.click(within(row as HTMLElement).getByRole("button", { name: /Create channel/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /^Create$/ }));
    const calls = () =>
      (globalThis.fetch as unknown as { mock: { calls: [string, RequestInit][] } }).mock.calls;
    await waitFor(() =>
      expect(calls().map((call) => String(call[0]))).toContain("/admin/connections"),
    );
    expect(calls().map((call) => String(call[0]))).toContain("/admin/discovery/models-preview");
    const [, init] = calls().find((call) => String(call[0]) === "/admin/connections")!;
    const body = JSON.parse(String(init.body)) as { models_csv: string };
    expect(body.models_csv).toBe(
      "opencode-zen-free/big-pickle,opencode-zen-free/deepseek-v4.1-flash-free",
    );
  });
});
