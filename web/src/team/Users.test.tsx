import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { UsersLayout } from "./UsersLayout";
import { MembersPanel } from "./panels/MembersPanel";
import { OverviewPanel } from "./panels/OverviewPanel";
import { QuotasPanel } from "./panels/QuotasPanel";
import type { TeamRequest } from "./types";

/**
 * The user-management module: which boards exist, and the two writes that were
 * previously impossible from the console — a member's spend allowance and a
 * tenant group's quotas.
 *
 * The transport is a plain function, exactly as the module receives it in
 * production: the module must not know about the console's session (an ESLint
 * boundary enforces that), so the test does not have to fake one either.
 */

const member = {
  id: 2,
  name: "Alice",
  username: "alice",
  role: "member",
  status: "active",
  policy_id: 1,
  key_count: 1,
  created_at: "2026-10-02",
  quota_total_tokens: 1000,
  quota_used_tokens: 250,
  quota_total_cost: 10,
  quota_used_cost: 2.5,
};
const policy = {
  id: 1,
  name: "Default",
  models: [],
  member_ids: [],
  all_models: false,
  max_keys: 5,
  rpm: 60,
  allow_routing: false,
  allow_request_preferences: false,
};
const group = {
  name: "default",
  quota_total_tokens: 0,
  quota_used_tokens: 0,
  quota_total_cost: 0,
  quota_used_cost: 0,
  rate_per_minute: 0,
  rate_burst: 0,
};
const branding = {
  name: "Meta Gateway",
  accent: "#275b85",
  logo_url: "",
  notice: "",
  login_description: "",
  api_base_url: "",
  show_usage: true,
  show_routing: true,
};

interface Call {
  path: string;
  method: string;
  body?: Record<string, unknown>;
}

function renderUsers({
  role = "owner",
  mode = "team",
  entry = "/members",
}: { role?: string; mode?: "personal" | "team"; entry?: string } = {}) {
  const calls: Call[] = [];
  const request: TeamRequest = async <T,>(path: string, init?: RequestInit) => {
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push({
      path,
      method,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    });
    if (path === "/admin/mode") return { mode, has_owner: true, role } as T;
    if (path === "/admin/team/settings")
      return { settings: { mode, branding }, has_owner: true, role } as T;
    if (path === "/admin/team/users") return [member] as T;
    if (path === "/admin/team/policies") return [policy] as T;
    if (path === "/admin/groups") return [group] as T;
    return [] as T;
  };
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <MemoryRouter initialEntries={[entry]}>
          <Routes>
            <Route path="/" element={<UsersLayout request={request} />}>
              <Route path="overview" element={<OverviewPanel />} />
              <Route path="members" element={<MembersPanel />} />
              <Route path="quotas" element={<QuotasPanel />} />
            </Route>
          </Routes>
        </MemoryRouter>
      </I18nProvider>
    </QueryClientProvider>,
  );
  return calls;
}

describe("user management module", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
  });
  afterEach(cleanup);

  // The module is the multi-user switch. A gateway that has not switched it on
  // gets one board — the one that explains and performs the switch — instead of
  // a roster of empty rooms.
  it("shows only the switch while the gateway is personal", async () => {
    renderUsers({ mode: "personal" });
    expect(await screen.findByRole("link", { name: "Overview" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Members & invitations" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Pricing" })).not.toBeInTheDocument();
  });

  it("lists every board for an owner once the module is on", async () => {
    renderUsers();
    for (const label of [
      "Overview",
      "Members & invitations",
      "Access & routing",
      "Credit & quotas",
      "Pricing",
      "Codes",
      "Third-party sign-in",
      "Member interface",
    ])
      expect(await screen.findByRole("link", { name: label })).toBeInTheDocument();
  });

  // An admin runs people, not the gateway: the boards they cannot operate are
  // absent rather than disabled.
  it("offers an admin only the boards they may operate", async () => {
    renderUsers({ role: "admin" });
    expect(await screen.findByRole("link", { name: "Members & invitations" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Member interface" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Access & routing" })).not.toBeInTheDocument();
  });

  // The roster used to print the token pool alone, which is why "how much is
  // left" had no answer for a prepaid member: money runs out first.
  it("shows both budgets a member is limited by", async () => {
    renderUsers();
    expect(await screen.findByText("Alice")).toBeInTheDocument();
    expect(screen.getByText(/250 \/ 1,000/)).toBeInTheDocument();
    expect(screen.getByText(/\$2\.50 \/ \$10\.00/)).toBeInTheDocument();
  });

  it("saves both budgets from the member's own page", async () => {
    const calls = renderUsers();
    fireEvent.click(await screen.findByRole("button", { name: /Manage/ }));
    const tokens = await screen.findByLabelText("Token credit limit");
    const spend = screen.getByLabelText("Spend allowance");
    fireEvent.change(tokens, { target: { value: "5000" } });
    fireEvent.change(spend, { target: { value: "25" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      const patch = calls.find((c) => c.method === "PATCH");
      expect(patch?.path).toBe("/admin/team/users/2");
      expect(patch?.body).toMatchObject({
        quota_total_tokens: 5000,
        quota_total_cost: 25,
      });
    });
  });

  it("saves a tenant group's quotas in one request", async () => {
    const calls = renderUsers({ entry: "/quotas" });
    expect(await screen.findByText("Tenant group quotas")).toBeInTheDocument();
    // The group row arrives with the query, so the button is awaited too.
    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
    fireEvent.change(await screen.findByLabelText("Token credit limit"), {
      target: { value: "2000000" },
    });
    fireEvent.change(screen.getByLabelText("Spend allowance"), {
      target: { value: "50" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      const put = calls.find((c) => c.method === "PUT");
      expect(put?.path).toBe("/admin/groups/default");
      // The endpoint replaces the row, so every field travels — sending only
      // what changed would zero the rest.
      expect(put?.body).toMatchObject({
        quota_total_tokens: 2000000,
        quota_total_cost: 50,
        rate_per_minute: 0,
        rate_burst: 0,
      });
    });
  });
});
