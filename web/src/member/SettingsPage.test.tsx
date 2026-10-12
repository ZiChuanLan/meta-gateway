import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, Link, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { UnsavedChangesBoundary } from "../lib/RouterBoundary";
import { teamText } from "../team/text";
import { SettingsPage } from "./SettingsPage";
import type { Account, PreferencesView } from "../team/types";
const initial: PreferencesView = {
  preferences: { failover: "inherit", max_retries: null },
  can_edit: true,
  site: { failover_enabled: true, retry_times: 2 },
  effective: { failover_enabled: true, retry_times: 2 },
};
const account: Account = {
  csrf: "test",
  user: {
    id: 7,
    username: "test",
    name: "Test",
    role: "member",
    status: "active",
    policy_id: 1,
    created_at: "2026-10-12",
    key_count: 0,
    quota_total_tokens: 0,
    quota_used_tokens: 0,
    quota_total_cost: 0,
    quota_used_cost: 0,
  },
  policy: {
    id: 1,
    name: "Test",
    models: [],
    member_ids: [],
    all_models: true,
    max_keys: 5,
    rpm: 60,
    allow_routing: true,
    allow_request_preferences: true,
  },
  branding: {
    name: "Gateway",
    logo_url: "",
    accent: "#275b85",
    notice: "",
    login_description: "",
    api_base_url: "",
    show_usage: true,
    show_routing: true,
  },
  credit: {
    total: 0,
    used: 0,
    available: 0,
    unlimited: true,
    cost_total: 0,
    cost_used: 0,
    cost_available: 0,
    cost_unlimited: true,
  },
};
beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function mount(fail = false) {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/me/preferences") {
      if (init?.method === "PUT")
        return new Response(
          JSON.stringify(
            fail
              ? { error: "save_failed" }
              : { ...initial, preferences: JSON.parse(String(init.body)) },
          ),
          { status: fail ? 500 : 200 },
        );
      return new Response(JSON.stringify(initial));
    }
    return new Response("[]");
  });
  vi.stubGlobal("fetch", fetcher);
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  });
  const element = (
    <SettingsPage
      account={account}
      appearance="modern"
      scheme="light"
      density="comfortable"
      onAppearance={vi.fn()}
      onScheme={vi.fn()}
      onDensity={vi.fn()}
      t={teamText("en")}
      onDisconnected={vi.fn()}
    />
  );
  function Host() {
    return (
      <UnsavedChangesBoundary>
        <Link to="/other">leave</Link>
        <Outlet />
      </UnsavedChangesBoundary>
    );
  }
  const router = createMemoryRouter(
    [
      {
        path: "/",
        element: <Host />,
        children: [
          { path: "account", element },
          { path: "other", element: <p>Other page</p> },
        ],
      },
    ],
    { initialEntries: ["/account"] },
  );
  render(
    <QueryClientProvider client={qc}>
      <I18nProvider>
        <RouterProvider router={router} />
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { qc, fetcher };
}
it("holds member preference input across query refresh and navigation", async () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  const { qc } = mount();
  const field = await screen.findByLabelText("Cross-channel failover");
  fireEvent.change(field, { target: { value: "off" } });
  await act(async () => {
    qc.setQueryData(["user", 7, "preferences"], {
      ...initial,
      preferences: { failover: "on", max_retries: 1 },
    });
  });
  expect(field).toHaveValue("off");
  fireEvent.click(screen.getByRole("link", { name: "leave" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(field).toHaveValue("off");
});
it("clears member dirty state only after the server confirms the preference payload", async () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  const { fetcher } = mount();
  fireEvent.change(await screen.findByLabelText("Cross-channel failover"), {
    target: { value: "off" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([p, i]) => p === "/me/preferences" && i?.method === "PUT"),
    ).toBe(true),
  );
  await screen.findByText("Saved");
  fireEvent.click(screen.getByRole("link", { name: "leave" }));
  await screen.findByText("Other page");
  expect(confirm).not.toHaveBeenCalled();
});
it("preserves preference input and protection after a failed save", async () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  mount(true);
  fireEvent.change(await screen.findByLabelText("Cross-channel failover"), {
    target: { value: "off" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await screen.findAllByRole("alert");
  fireEvent.click(screen.getByRole("link", { name: "leave" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(screen.getByLabelText("Cross-channel failover")).toHaveValue("off");
});
