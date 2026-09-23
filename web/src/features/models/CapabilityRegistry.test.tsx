import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ModelCapability } from "../../api/types";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { ToastProvider } from "../../toast";
import { CapabilityRegistryDialog } from "./CapabilityRegistry";

const clients: QueryClient[] = [];

function capability(model: string, overrides: Partial<ModelCapability> = {}): ModelCapability {
  return {
    model, kind: "chat", provider: "openai", endpoints: ["/v1/chat/completions"],
    input_formats: ["json"], input_modalities: ["text"], output_modalities: ["text"],
    max_input_images: 0, supports_stream: true, supports_tools: true,
    supports_json_mode: true, async_task: false, size_options: "",
    source: "builtin", notes: "", updated_at: "",
    ...overrides,
  };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function mockBackend(options: { sources?: string[] } = {}) {
  const items = [
    capability("gpt-5.1"),
    capability("gpt-image-2", { kind: "image_gen", endpoints: ["/v1/images/generations"], max_input_images: 2 }),
  ];
  // The catalog sync is a two-step contract — preview, then apply — so the mock
  // keeps both distinct and lets a test assert that opening the dialog did not
  // write anything.
  const catalogPreview = vi.fn(() => json({
    items: [
      {
        model: "gpt-5.1", found: true, sources: ["litellm", "models.dev"],
        capability_action: "refresh", capability_source: "catalog", capability_kind: "chat",
        capability_changes: [{ field: "endpoints", from: "/v1/chat/completions", to: "/v1/chat/completions,/v1/batch" }],
        metadata_action: "fill",
        metadata_changes: [{ field: "context_window", from: "0", to: "400000" }],
        price_action: "fill",
        price_changes: [{ field: "price_completion_per_1k", from: "0", to: "0.01" }],
      },
      {
        model: "gpt-image-2", found: true, sources: ["models.dev"],
        capability_action: "unchanged", capability_source: "catalog", capability_kind: "image_gen",
        metadata_action: "unchanged", price_action: "unchanged",
      },
    ],
    requested: 2, matched: 2, missing: 0,
    sources: ["litellm", "models.dev"], prices_enabled: true, fetched: true,
  }));
  const catalogSync = vi.fn(() => json({
    state: {
      synced_at: "2026-09-15T12:00:00Z", requested: 2, matched: 2,
      capabilities: 1, metadata: 1, prices: 1, skipped_manual: 0, missing: 0,
      sources: ["litellm", "models.dev"],
    },
    sources: ["litellm", "models.dev"],
  }));
  const autoTag = vi.fn(() => json({ tagged: 0 }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), "http://localhost").pathname;
    if (path === "/admin/model-capabilities") return json({ items });
    if (path === "/admin/routes/overview") return json([]);
    if (path === "/admin/model-capabilities/catalog") {
      return json({
        state: null,
        sources: options.sources ?? ["litellm", "models.dev"],
        scheduled: true,
        prices_enabled: true,
      });
    }
    if (path === "/admin/model-capabilities/catalog/preview") return catalogPreview();
    if (path === "/admin/model-capabilities/catalog/sync") return catalogSync();
    if (path === "/admin/model-capabilities/auto-tag") return autoTag();
    return json({});
  }));
  return { catalogPreview, catalogSync, autoTag };
}

function renderRegistry(onClose = () => {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <CapabilityRegistryDialog onClose={onClose} />
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("capability registry dialog", () => {
  beforeEach(() => {
    localStorage.clear(); sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => {
    cleanup(); clients.splice(0).forEach((client) => client.clear()); vi.unstubAllGlobals();
  });

  it("lists the persisted rows with the endpoints routing will plan against", async () => {
    mockBackend();
    renderRegistry();
    expect(await screen.findByText("gpt-5.1")).toBeInTheDocument();
    // Both the row and its edit affordance come from the same payload, so a
    // second model proves the list is a list and not a single detail view.
    expect(screen.getByText("gpt-image-2")).toBeInTheDocument();
    expect(screen.getByText("/v1/images/generations")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Edit" })).toHaveLength(2);
  });

  it("filters the registry without refetching it", async () => {
    const backend = mockBackend();
    renderRegistry();
    await screen.findByText("gpt-5.1");
    const fetches = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.length;
    fireEvent.change(screen.getByRole("textbox", { name: "Filter models" }), { target: { value: "image" } });
    expect(screen.queryByText("gpt-5.1")).not.toBeInTheDocument();
    expect(screen.getByText("gpt-image-2")).toBeInTheDocument();
    expect((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.length).toBe(fetches);
    expect(backend.catalogSync).not.toHaveBeenCalled();
  });

  it("previews the plan and only writes once the operator confirms", async () => {
    const backend = mockBackend();
    renderRegistry();

    const sync = await screen.findByRole("button", { name: "Sync from catalogs" });
    // Enabled only once the server has reported which indexes are wired in.
    await waitFor(() => expect(sync).toBeEnabled());
    fireEvent.click(sync);

    // The dialog opens on a dry run: the plan is fetched, nothing is written.
    expect(
      await screen.findByText("Sync from the public model indexes"),
    ).toBeInTheDocument();
    expect(backend.catalogPreview).toHaveBeenCalledOnce();
    expect(backend.catalogSync).not.toHaveBeenCalled();
    // Each planned write is spelled out with its before and after value.
    expect(await screen.findByText("/v1/chat/completions,/v1/batch")).toBeInTheDocument();
    expect(screen.getByText("0.01")).toBeInTheDocument();

    fireEvent.click(await screen.findByRole("button", { name: "Apply 1 change(s)" }));
    await waitFor(() => expect(backend.catalogSync).toHaveBeenCalledOnce());
  });

  it("disables the catalog action when the gateway has no index configured", async () => {
    mockBackend({ sources: [] });
    // The console can outrun the server it talks to, so an empty source list
    // must disable the action rather than offer a sync that cannot run.
    renderRegistry();
    const sync = await screen.findByRole("button", { name: "Sync from catalogs" });
    await waitFor(() => expect(sync).toBeDisabled());
    // Nothing is offered, and no plan is fetched behind the scenes.
    expect(screen.queryByText("Sync from the public model indexes")).not.toBeInTheDocument();
  });

  it("auto-tags the routed models that are already on the screen", async () => {
    const backend = mockBackend();
    renderRegistry();
    await screen.findByText("gpt-5.1");
    fireEvent.click(screen.getByRole("button", { name: "Auto-tag with built-in rules" }));
    await waitFor(() => expect(backend.autoTag).toHaveBeenCalledOnce());
  });
});
