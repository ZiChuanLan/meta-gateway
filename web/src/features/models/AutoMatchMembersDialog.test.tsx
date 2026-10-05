import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Route } from "../../api/types";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { ToastProvider } from "../../toast";
import { AutoMatchMembersDialog } from "./AutoMatchMembersDialog";

const clients: QueryClient[] = [];

function route(): Route {
  return {
    id: 1,
    model_pattern: "deepseek-v4-flash",
    enabled: true,
    routing_mode: "auto",
  } as Route;
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function mockBackend(options: {
  items?: Array<{ channel_id: number; channel_name: string; source: string; model?: string }>;
} = {}) {
  const attach = vi.fn((body: Record<string, unknown>) => json({
    // The server reports what it actually did; the dialog surfaces both counts.
    added: (body.channel_ids as number[]).length,
    skipped: 0,
  }));
  const previews: URL[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/admin/discovery/model-channels") {
      previews.push(url);
      return json({
        items: options.items ?? [
          { channel_id: 11, channel_name: "serving", source: "models_csv" },
          { channel_id: 12, channel_name: "also-serving", source: "discovered" },
        ],
      });
    }
    if (url.pathname === "/admin/routes/1/auto-match") {
      return attach(JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>);
    }
    return json({});
  }));
  return { attach, previews };
}

function renderDialog({
  group = "default",
  attachedChannelIds = [] as number[],
  onClose = () => {},
} = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <AutoMatchMembersDialog
              route={route()}
              group={group}
              attachedChannelIds={attachedChannelIds}
              onClose={onClose}
            />
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("auto-match members dialog", () => {
  beforeEach(() => {
    localStorage.clear(); sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => {
    cleanup(); clients.splice(0).forEach((client) => client.clear()); vi.unstubAllGlobals();
  });

  it("previews every serving channel, pre-ticked, and attaches only on confirm", async () => {
    const backend = mockBackend();
    const onClose = vi.fn();
    renderDialog({ onClose });

    // Opening is a read: nothing is written until the operator confirms.
    expect(await screen.findByText("serving")).toBeInTheDocument();
    expect(screen.getByText("also-serving")).toBeInTheDocument();
    expect(screen.getByText("2 available, 2 selected")).toBeInTheDocument();
    expect(backend.attach).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Attach 2 channel(s)" }));
    await waitFor(() => expect(backend.attach).toHaveBeenCalledOnce());
    expect(backend.attach.mock.calls[0]?.[0]).toEqual({
      channel_ids: [11, 12],
      group_name: "default",
      match: "exact",
    });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("sends only the kept channels and targets the group being viewed", async () => {
    const backend = mockBackend();
    renderDialog({ group: "blue" });

    await screen.findByText("also-serving");
    fireEvent.click(screen.getByRole("checkbox", { name: "also-serving" }));
    expect(screen.getByText("2 available, 1 selected")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Attach 1 channel(s)" }));
    await waitFor(() => expect(backend.attach).toHaveBeenCalledOnce());
    expect(backend.attach.mock.calls[0]?.[0]).toEqual({
      channel_ids: [11],
      group_name: "blue",
      match: "exact",
    });
  });

  // A route named after the base model usually needs the channels that list a
  // -variant instead: they serve the model in practice while the bare name
  // never appears in their list. The dialog must ask for that scope AND show
  // which name the member will forward as, since the two differ.
  it("widens to the related scope and names the matched variant", async () => {
    const backend = mockBackend({
      items: [
        { channel_id: 11, channel_name: "serving", source: "models_csv" },
        {
          channel_id: 13,
          channel_name: "variant-only",
          source: "models_csv",
          model: "deepseek-v4-flash-free",
        },
      ],
    });
    renderDialog();
    await screen.findByText("serving");

    fireEvent.click(screen.getByRole("radio", { name: /Related:/ }));
    expect(await screen.findByText("matched deepseek-v4-flash-free")).toBeInTheDocument();
    // The scope reaches the server on the preview request, not only on attach.
    await waitFor(() =>
      expect(backend.previews.at(-1)?.searchParams.get("match")).toBe("related"),
    );

    fireEvent.click(screen.getByRole("button", { name: "Attach 2 channel(s)" }));
    await waitFor(() => expect(backend.attach).toHaveBeenCalledOnce());
    expect(backend.attach.mock.calls[0]?.[0]).toMatchObject({
      channel_ids: [11, 13],
      match: "related",
    });
  });

  it("does not offer a channel the group already has, and never sends an empty list", async () => {
    const backend = mockBackend();
    renderDialog({ attachedChannelIds: [11] });

    // `serving` is already a member here, so it is reported rather than
    // offered: re-attaching it is a server-side no-op.
    expect(
      await screen.findByText("1 channel(s) are already in this group and will not be added again"),
    ).toBeInTheDocument();
    expect(screen.getByText("1 available, 1 selected")).toBeInTheDocument();

    // Unticking the last candidate must disable the action. An empty
    // channel_ids list means "every current match" on the wire, so sending it
    // would attach the opposite of what the operator just chose.
    fireEvent.click(screen.getByRole("checkbox", { name: "also-serving" }));
    const confirm = screen.getByRole("button", { name: "Attach 0 channel(s)" });
    expect(confirm).toBeDisabled();
    fireEvent.click(confirm);
    expect(backend.attach).not.toHaveBeenCalled();
  });

  it("says so when there is nothing left to attach", async () => {
    mockBackend();
    renderDialog({ attachedChannelIds: [11, 12] });
    expect(
      await screen.findByText("Every channel serving this model is already in this group."),
    ).toBeInTheDocument();
  });

  it("surfaces a match lookup failure instead of an empty list", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path === "/admin/discovery/model-channels") {
        return json({ error: "discovery unavailable" }, 503);
      }
      return json({});
    }));
    renderDialog();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByText(/No enabled channel serves this model/)).not.toBeInTheDocument();
  });
});
