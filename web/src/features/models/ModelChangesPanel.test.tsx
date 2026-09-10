import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { ToastProvider } from "../../toast";
import { ModelChangesPanel } from "./ModelChangesPanel";

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
  const view = render(<QueryClientProvider client={queryClient}><I18nProvider><ToastProvider><SessionProvider><ModelChangesPanel /></SessionProvider></ToastProvider></I18nProvider></QueryClientProvider>);
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
