import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { ToastProvider } from "../../toast";
import { ModelChangesPanel } from "./ModelChangesPanel";

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location">{location.pathname}{location.search}</div>;
}

const member = { member_id: 11, route_id: 7, route_name: "Public route", model_pattern: "public-model", channel_id: 1, upstream_model: "old-model", group_name: "default" };
const removed = { id: 1, channel_id: 1, channel_name: "Channel A", model_name: "old-model", kind: "removed", status: "pending", detected_at: "2026-08-20T00:00:00Z", candidates: ["new-model"], members: [member, { ...member, member_id: 12 }] };
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
function setup(options: { applyError?: boolean; empty?: boolean } = {}) {
  const calls: { path: string; body: Record<string, unknown> }[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (init?.method === "POST") {
      const body = JSON.parse(String(init.body));
      calls.push({ path, body });
      if (path.endsWith("/preview")) return response({ preview_token: "server-preview", items: [{ ...member, source_channel_id: 1, source_model: "old-model", target_channel_id: body.target_channel_id, target_model: body.target_model }] });
      if (path.endsWith("/apply")) return options.applyError ? response({ error: "snapshot changed" }, 409) : response({ updated: 1 });
      if (path.endsWith("/ignore")) return response({ updated: 1 });
    }
    if (path.endsWith("/models/changes")) return response({ items: options.empty ? [] : [removed, { ...removed, id: 2, channel_id: 2, channel_name: "Channel B", members: [{ ...member, member_id: 21, channel_id: 2 }] }], summary: options.empty ? { added: 0, removed: 0, affected_routes: 0 } : { added: 0, removed: 2, affected_routes: 1 } });
    if (path.endsWith("/admin/channels")) return response([{ id: 1, name: "Channel A", status: "enabled" }, { id: 2, name: "Channel B", status: "enabled" }]);
    if (path.includes("/discovery/models?channel_id=")) return response([{ model_name: path.endsWith("=2") ? "other-model" : "new-model", available: true }]);
    return response({ error: `unexpected ${path}` }, 500);
  });
  vi.stubGlobal("fetch", fetch);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = render(<QueryClientProvider client={queryClient}><I18nProvider><ToastProvider><SessionProvider><MemoryRouter initialEntries={["/models"]}><ModelChangesPanel /></MemoryRouter></SessionProvider></ToastProvider></I18nProvider></QueryClientProvider>);
  return { ...view, calls, queryClient };
}
async function openReplacement() {
  fireEvent.click(await screen.findByRole("button", { name: "View changes" }));
  fireEvent.click(screen.getAllByRole("button", { name: "Choose replacement" })[0]!);
  await screen.findByRole("option", { name: "new-model — Added" });
  fireEvent.change(screen.getByLabelText("Target model"), { target: { value: "new-model" } });
}
beforeEach(() => { localStorage.clear(); sessionStorage.clear(); localStorage.setItem("meta-gateway.locale", "en"); localStorage.setItem("meta-gateway.admin-token", "test-token"); });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
describe("upstream model maintenance", () => {
  it("stays compact with no changes and preserves history filters across navigation", async () => {
    const first = setup({ empty: true });
    fireEvent.click(await screen.findByRole("button", { name: "Change history" }));
    fireEvent.change(screen.getByLabelText("Search model or channel"), { target: { value: "deepseek" } });
    expect(await screen.findByText("No matching changes")).toBeInTheDocument();
    first.unmount();
    setup({ empty: true });
    expect(screen.getByLabelText("Search model or channel")).toHaveValue("deepseek");
  });
  it("requires preview before apply and sends only selected members with its server token", async () => {
    const { calls } = setup();
    await openReplacement();
    fireEvent.click(screen.getByLabelText(/Member #12/));
    expect(calls).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Preview replacement" }));
    await screen.findByRole("button", { name: "Apply (1 members)" });
    expect(screen.getByRole("heading", { name: "Confirm these mapping changes" })).toHaveFocus();
    fireEvent.click(screen.getByRole("button", { name: "Apply (1 members)" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Updated upstream mappings for 1 members.");
    expect(calls[0]?.body).toEqual({ change_ids: [1], member_ids: [11], target_channel_id: 1, target_model: "new-model" });
    expect(calls[1]?.body).toEqual({ ...calls[0]!.body, preview_token: "server-preview" });
  });
  it("makes cross-channel selection explicit and clears the previous target", async () => {
    const { calls } = setup();
    await openReplacement();
    expect(screen.getByLabelText("Target channel")).toBeDisabled();
    fireEvent.click(screen.getByLabelText("Choose a model from another channel"));
    fireEvent.change(screen.getByLabelText("Target channel"), { target: { value: "2" } });
    expect(screen.getByLabelText("Target model")).toHaveValue("");
    await screen.findByRole("option", { name: "other-model" });
    fireEvent.change(screen.getByLabelText("Target model"), { target: { value: "other-model" } });
    fireEvent.click(screen.getByRole("button", { name: "Preview replacement" }));
    await waitFor(() => expect(calls[0]?.body.target_channel_id).toBe(2));
  });
  it("rejects mixed-channel bulk selection in the UI", async () => {
    setup();
    fireEvent.click(await screen.findByRole("button", { name: "View changes" }));
    fireEvent.click(screen.getByLabelText("Select change old-model / Channel A"));
    fireEvent.click(screen.getByLabelText("Select change old-model / Channel B"));
    expect(screen.getByRole("button", { name: "Replace selected changes (2)" })).toBeDisabled();
  });
  it("does not allow resubmitting a failed stale preview", async () => {
    setup({ applyError: true });
    await openReplacement();
    fireEvent.click(screen.getByRole("button", { name: "Preview replacement" }));
    fireEvent.click(await screen.findByRole("button", { name: "Apply (1 members)" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("preview expired");
    expect(screen.getByRole("button", { name: "Apply (1 members)" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Back to selection" }));
    expect(screen.getByRole("button", { name: "Preview replacement" })).toBeEnabled();
    expect(screen.getByLabelText("Target model")).toHaveFocus();
  });
  it("ignores only after confirmation, without invoking replacement", async () => {
    const { calls } = setup();
    fireEvent.click(await screen.findByRole("button", { name: "View changes" }));
    fireEvent.click(screen.getAllByRole("button", { name: "Ignore" })[0]!);
    expect(calls).toHaveLength(0);
    fireEvent.click(screen.getAllByRole("button", { name: "Ignore" }).at(-1)!);
    await waitFor(() => expect(calls).toEqual([{ path: "/admin/models/changes/ignore", body: { ids: [1] } }]));
  });
});

// Confidence signals, bulk-ignore of harmless removals, and the adopt
// deep-link — zh locale, with a router so the deep link can be observed.
describe("upstream model maintenance signals", () => {
  beforeEach(() => {
    localStorage.setItem("meta-gateway.locale", "zh-CN");
  });

  const signalMember = { member_id: 31, route_id: 9, route_name: "impacted-model", model_pattern: "impacted-model", channel_id: 7, upstream_model: "impacted-model", group_name: "default" };
  const signalItems = [
    {
      id: 21, channel_id: 7, channel_name: "WONG", model_name: "gone-model", kind: "removed", status: "pending",
      detected_at: new Date(Date.now() - 3 * 86_400_000).toISOString(), candidates: [], members: [],
      confirmed: true, miss_count: 3, partial_keys: true, flap_count: 2, runtime_blocked: true, blocked_at: "2026-09-10T08:00:00Z",
    },
    {
      id: 22, channel_id: 7, channel_name: "WONG", model_name: "brand-new", kind: "added", status: "pending",
      detected_at: new Date().toISOString(), candidates: [], members: [],
    },
    {
      id: 23, channel_id: 7, channel_name: "WONG", model_name: "impacted-model", kind: "removed", status: "pending",
      detected_at: new Date().toISOString(), candidates: [], members: [signalMember],
    },
  ];
  const signalSummary = { added: 1, removed: 2, affected_routes: 1, confirmed: 1 };

  function setupWithRouter() {
    const ignoreCalls: number[][] = [];
    const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/models/changes/ignore")) {
        const body = JSON.parse(String(init?.body ?? "{}")) as { ids?: number[] };
        ignoreCalls.push(body.ids ?? []);
        return response({ updated: (body.ids ?? []).length });
      }
      if (path.endsWith("/models/changes")) return response({ items: signalItems, summary: signalSummary });
      return response({ error: `unexpected ${path}` }, 500);
    });
    vi.stubGlobal("fetch", fetch);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <I18nProvider>
          <ToastProvider>
            <SessionProvider>
              <MemoryRouter initialEntries={["/models"]}>
                <Routes>
                  <Route path="/models" element={<><ModelChangesPanel /><div data-testid="location" /></>} />
                  <Route path="/models/channel/:channelId" element={<LocationProbe />} />
                </Routes>
              </MemoryRouter>
            </SessionProvider>
          </ToastProvider>
        </I18nProvider>
      </QueryClientProvider>,
    );
    return { ignoreCalls };
  }

  it("surfaces the confirmed count, badges, and missing duration", async () => {
    setupWithRouter();
    fireEvent.click(await screen.findByRole("button", { name: "查看变更" }));
    expect(await screen.findByText("已确认 1")).toBeInTheDocument();
    const row = screen.getByText("gone-model").closest("article")!;
    expect(within(row).getByText("已确认缺失")).toBeInTheDocument();
    expect(within(row).getByText("部分 Key 未响应")).toBeInTheDocument();
    expect(within(row).getByText("反复上下线 ×2")).toBeInTheDocument();
    expect(within(row).getByText("运行时观测到不可用")).toBeInTheDocument();
    expect(within(row).getByText("已持续缺失 3 天")).toBeInTheDocument();
    // The heading span renders before the member list, so the first match
    // is the row title.
    const impacted = screen.getAllByText("impacted-model")[0]!.closest("article")!;
    expect(within(impacted).queryByText("已确认缺失")).not.toBeInTheDocument();
  });

  it("deep-links a pending addition to the channel models page", async () => {
    setupWithRouter();
    fireEvent.click(await screen.findByRole("button", { name: "查看变更" }));
    const row = (await screen.findByText("brand-new")).closest("article")!;
    fireEvent.click(within(row).getByRole("button", { name: "去接入" }));
    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent("/models/channel/7?model=brand-new");
    });
  });

  it("bulk-ignores pending removals without route impact", async () => {
    const { ignoreCalls } = setupWithRouter();
    // Only the member-free removal counts as harmless.
    fireEvent.click(await screen.findByRole("button", { name: "查看变更" }));
    fireEvent.click(await screen.findByRole("button", { name: "忽略无影响（1）" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "忽略" }));
    await waitFor(() => expect(ignoreCalls).toEqual([[21]]));
  });
});
