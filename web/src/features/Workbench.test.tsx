import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import type { ModelCapability } from "../api/types";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { ToastProvider } from "../toast";
import Workbench from "./Workbench";

const imageData = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8AAAwAB/AF+msuWAAAAAElFTkSuQmCC";
const clients: QueryClient[] = [];

function capability(model = "gpt-image-2"): ModelCapability {
  const grok = model.startsWith("grok");
  return {
    model, kind: "image_edit", provider: grok ? "xai" : "openai",
    endpoints: grok ? ["/v1/images/edits"] : ["/v1/images/generations", "/v1/images/edits"],
    input_formats: grok ? ["json"] : ["json", "multipart"],
    input_modalities: ["text", "image"], output_modalities: ["image"],
    max_input_images: 2, supports_stream: false, supports_tools: false,
    supports_json_mode: false, async_task: false, size_options: "1024x1024",
    source: "builtin", notes: "", updated_at: "",
  };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

/** A routed chat model. The text tab lists models by the endpoint their
 *  capability resolves to, so an image registration must not appear there. */
function chatCapability(model = "gpt-5.1"): ModelCapability {
  return {
    model, kind: "chat", provider: "openai", endpoints: ["/v1/chat/completions"],
    input_formats: ["json"], input_modalities: ["text"], output_modalities: ["text"],
    max_input_images: 0, supports_stream: true, supports_tools: true,
    supports_json_mode: true, async_task: false, size_options: "",
    source: "builtin", notes: "", updated_at: "",
  };
}

function deferredResponse() {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((complete) => { resolve = complete; });
  return { promise, resolve };
}

/** Builds the SSE body the admin probe relays: meta first, then provider frames. */
function sse(frames: Array<{ event?: string; data: string }>) {
  const text = frames
    .map((frame) => `${frame.event ? `event: ${frame.event}\n` : ""}data: ${frame.data}\n\n`)
    .join("");
  return new Response(text, { status: 200, headers: { "Content-Type": "text/event-stream" } });
}

function streamedReply(...deltas: string[]) {
  return sse([
    { event: "meta", data: JSON.stringify({ status: 200, latency_ms: 7, model: "gpt-5.1", channel_name: "chat-up" }) },
    ...deltas.map((content) => ({ data: JSON.stringify({ choices: [{ delta: { content } }] }) })),
    { event: "done", data: "{}" },
  ]);
}

function mockBackend(options: {
  capability?: ModelCapability;
  resolve?: () => Promise<Response> | Response;
  image?: (request: Record<string, unknown>) => Promise<Response> | Response;
  chat?: (request: Record<string, unknown>) => Promise<Response> | Response;
} = {}) {
  const model = options.capability ?? capability();
  const chatModel = chatCapability();
  const resolve = vi.fn(options.resolve ?? (() => json({ items: {
    [model.model]: model,
    [chatModel.model]: chatModel,
  } })));
  const image = vi.fn(options.image ?? (() => json({
    status: 200, latency_ms: 12, model: model.model,
    plan: { endpoint: "images/edits", format: "json" },
    images: [{ data_url: imageData, revised_prompt: "purple cube" }],
  })));
  const chat = vi.fn(options.chat ?? ((request: Record<string, unknown>) =>
    request.stream
      ? streamedReply("po", "ng")
      : json({
          status: 200, latency_ms: 8, model: chatModel.model, channel_name: "chat-up",
          body: { id: "chatcmpl-test", choices: [{ message: { role: "assistant", content: "pong" } }] },
        })));
  // The catalog sync is a two-step contract — preview, then apply — so the mock
  // keeps both distinct and lets a test assert that opening the dialog did not
  // write anything.
  const catalogPreview = vi.fn((_request: Record<string, unknown>) => json({
    items: [
      {
        model: chatModel.model, found: true, sources: ["litellm", "models.dev"],
        capability_action: "refresh", capability_source: "catalog", capability_kind: "chat",
        capability_changes: [{ field: "endpoints", from: "/v1/chat/completions", to: "/v1/chat/completions,/v1/batch" }],
        metadata_action: "fill",
        metadata_changes: [{ field: "context_window", from: "0", to: "400000" }],
        price_action: "fill",
        price_changes: [{ field: "price_completion_per_1k", from: "0", to: "0.01" }],
      },
      {
        model: model.model, found: true, sources: ["models.dev"],
        capability_action: "unchanged", capability_source: "catalog", capability_kind: "image_gen",
        metadata_action: "unchanged", price_action: "unchanged",
      },
    ],
    requested: 2, matched: 2, missing: 0,
    sources: ["litellm", "models.dev"], prices_enabled: true, fetched: true,
  }));
  const catalogSync = vi.fn((_request: Record<string, unknown>) => json({
    state: {
      synced_at: "2026-09-15T12:00:00Z", requested: 2, matched: 2,
      capabilities: 1, metadata: 1, prices: 1, skipped_manual: 0, missing: 0,
      sources: ["litellm", "models.dev"],
    },
    sources: ["litellm", "models.dev"],
  }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), "http://localhost").pathname;
    if (path === "/admin/routes/overview") return json([
      { route: { id: 1, model_pattern: model.model, enabled: true }, members: [{ channel: { id: 7, name: "image-up" }, member: { priority: 9, weight: 1 } }] },
      { route: { id: 2, model_pattern: chatModel.model, enabled: true }, members: [{ channel: { id: 8, name: "chat-up" }, member: { priority: 5, weight: 2 } }] },
      { route: { id: 3, model_pattern: "disabled-image", enabled: false }, members: [] },
    ]);
    if (path === "/admin/model-capabilities/resolve") return resolve();
    if (path === "/admin/model-capabilities") return json({ items: [model, chatModel] });
    if (path === "/admin/model-capabilities/catalog/preview")
      return catalogPreview(JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>);
    if (path === "/admin/model-capabilities/catalog/sync")
      return catalogSync(JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>);
    if (path === "/admin/model-capabilities/catalog") return json({
      state: null, sources: ["litellm", "models.dev"], scheduled: true, prices_enabled: true,
    });
    if (path === "/admin/try/image") return image(JSON.parse(String(init?.body)) as Record<string, unknown>);
    if (path === "/admin/try/chat") return chat(JSON.parse(String(init?.body)) as Record<string, unknown>);
    return json({});
  }));
  return { resolve, image, chat, catalogPreview, catalogSync };
}

function renderWorkbench() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  clients.push(client);
  return render(<QueryClientProvider client={client}><I18nProvider><ToastProvider><SessionProvider><MemoryRouter>
    <Workbench />
  </MemoryRouter></SessionProvider></ToastProvider></I18nProvider></QueryClientProvider>);
}

describe("image workbench", () => {
  beforeEach(() => {
    localStorage.clear(); sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => {
    cleanup(); clients.splice(0).forEach((client) => client.clear()); vi.unstubAllGlobals();
  });

  it("waits for capability resolution before deciding there are no image models", async () => {
    const pending = deferredResponse();
    const backend = mockBackend({ resolve: () => pending.promise });
    renderWorkbench();
    await waitFor(() => expect(backend.resolve).toHaveBeenCalled());
    expect(screen.queryByText(/No image model detected/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Generate image" })).not.toBeInTheDocument();
    await act(async () => pending.resolve(json({ items: { "gpt-image-2": capability() } })));
    expect(await screen.findByRole("button", { name: "Generate image" })).toBeDisabled();
    expect(screen.queryByRole("option", { name: "disabled-image" })).not.toBeInTheDocument();
  });

  it("shows capability lookup failures as errors instead of an empty model list", async () => {
    mockBackend({ resolve: () => json({ error: "capability service unavailable" }, 503) });
    renderWorkbench();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByText(/No image model detected/)).not.toBeInTheDocument();
  });

  it("validates reference files before reading or submitting them", async () => {
    const backend = mockBackend({ capability: capability("grok-imagine-image-edit") });
    renderWorkbench();
    const submit = await screen.findByRole("button", { name: "Edit image" });
    fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "add a hat" } });
    expect(submit).toBeDisabled();
    expect(screen.getByText("Add a reference image before editing.")).toBeInTheDocument();
    const input = screen.getByLabelText("Add images");
    fireEvent.change(input, { target: { files: [new File(["notes"], "notes.txt", { type: "text/plain" })] } });
    expect(await screen.findByRole("alert")).toHaveTextContent("notes.txt is not an image file");
    const oversized = new File(["image"], "large.png", { type: "image/png" });
    Object.defineProperty(oversized, "size", { value: 21 * 1024 * 1024 });
    fireEvent.change(input, { target: { files: [oversized] } });
    expect(await screen.findByRole("alert")).toHaveTextContent("20 MB");
    expect(backend.image).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { files: [new File(["image"], "reference.png", { type: "image/png" })] } });
    expect(await screen.findByAltText("reference.png")).toBeInTheDocument();
    await waitFor(() => expect(submit).toBeEnabled());
    fireEvent.click(submit);
    await waitFor(() => expect(backend.image).toHaveBeenCalledOnce());
    expect(backend.image.mock.calls[0]?.[0]).toMatchObject({
      model: "grok-imagine-image-edit", mode: "auto", prompt: "add a hat",
      images: [{ name: "reference.png", data_url: expect.stringContaining("data:image/png;base64,") }],
    });
  });

  it("shows what the image upstream actually said instead of a bare status", async () => {
    // grok2api refuses by plane: the Web plane is rate limited, the Console
    // plane reports exhausted quota. Both surface as 429, so the status alone
    // hid the difference — the panel now relays the provider's own message.
    const backend = mockBackend({
      image: () => json({
        status: 429, latency_ms: 11644, model: "gpt-image-2",
        plan: { endpoint: "images/edits", format: "json" },
        images: [],
        body: {
          error: {
            code: "upstream_unavailable",
            message: "Grok Web 媒体上游返回 429: 8: Too many requests. Wait a moment and try again.",
          },
        },
      }),
    });
    renderWorkbench();
    fireEvent.change(await screen.findByRole("textbox", { name: "Prompt" }), { target: { value: "add a hat" } });
    fireEvent.click(screen.getByRole("button", { name: "Generate image" }));
    await waitFor(() => expect(backend.image).toHaveBeenCalledOnce());
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Too many requests");
    expect(alert).not.toHaveTextContent("Upstream returned HTTP 429");
  });

  it("keeps an earlier result reachable when a later attempt fails", async () => {
    let calls = 0;
    const backend = mockBackend({
      image: () => {
        calls += 1;
        return calls === 1
          ? json({
              status: 200, latency_ms: 12, model: "gpt-image-2",
              plan: { endpoint: "images/generations", format: "json" },
              images: [{ data_url: imageData, revised_prompt: "purple cube" }],
            })
          : json({
              status: 429, latency_ms: 1677, model: "gpt-image-2",
              plan: { endpoint: "images/generations", format: "json" },
              images: [],
              body: { error: { code: "upstream_quota_exhausted", message: "Upstream account quota is cooling down" } },
            });
      },
    });
    renderWorkbench();
    fireEvent.change(await screen.findByRole("textbox", { name: "Prompt" }), { target: { value: "a purple cube" } });
    const submit = screen.getByRole("button", { name: "Generate image" });
    fireEvent.click(submit);
    expect(await screen.findByAltText("purple cube")).toBeVisible();
    // Only one result and it is already on screen, so the strip stays out of the way.
    expect(screen.queryByText("This session")).not.toBeInTheDocument();

    fireEvent.click(submit);
    await waitFor(() => expect(backend.image).toHaveBeenCalledTimes(2));
    expect(await screen.findByRole("alert")).toHaveTextContent("cooling down");
    // The failed attempt blanks the panel, so the earlier image has to stay
    // reachable — with a single entry the strip used to hide itself here.
    const strip = (await screen.findByText("This session")).closest(".workbench-history");
    expect(strip).not.toBeNull();
    fireEvent.click(within(strip as HTMLElement).getByRole("button"));
    expect(await screen.findByAltText("purple cube")).toBeVisible();
  });

  it("keeps an in-flight result and the prompt when switching tabs", async () => {
    const pending = deferredResponse();
    const backend = mockBackend({ image: () => pending.promise });
    renderWorkbench();
    const submit = await screen.findByRole("button", { name: "Generate image" });
    fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "a purple cube" } });
    fireEvent.click(submit);
    await waitFor(() => expect(backend.image).toHaveBeenCalledOnce());
    expect(screen.getByRole("combobox", { name: "Model" })).toBeDisabled();
    fireEvent.click(screen.getByText("Capabilities"));
    await act(async () => pending.resolve(json({
      status: 200, latency_ms: 12, model: "gpt-image-2",
      plan: { endpoint: "images/generations", format: "json" },
      images: [{ data_url: imageData, revised_prompt: "purple cube" }],
    })));
    fireEvent.click(screen.getByText("Images"));
    expect(await screen.findByAltText("purple cube")).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Prompt" })).toHaveValue("a purple cube");
    expect(screen.getByRole("button", { name: "Generate image" })).toBeEnabled();
  });

  it("streams a multi-turn conversation from the playground over the routed chat models only", async () => {
    const backend = mockBackend();
    renderWorkbench();
    fireEvent.click(await screen.findByText("Playground"));
    const picker = await screen.findByRole("combobox", { name: "Model" });
    expect(picker).toHaveValue("gpt-5.1");
    // The image model is routed but answers on /v1/images/*, so it is not a
    // candidate for a chat turn.
    expect(within(picker).getAllByRole("option").map((option) => option.textContent))
      .toEqual(["gpt-5.1"]);
    // Members of the selected model become the pinnable upstreams.
    expect(within(screen.getByRole("combobox", { name: "Upstream connection" })).getAllByRole("option")
      .map((option) => option.textContent)).toEqual(["Auto (gateway routing)", "chat-up · p5/w2"]);

    const composer = screen.getByPlaceholderText("Type a message — ⌘/Ctrl + Enter to send…");
    fireEvent.change(composer, { target: { value: "ping" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(backend.chat).toHaveBeenCalledOnce());
    expect(backend.chat.mock.calls[0]?.[0]).toMatchObject({
      model: "gpt-5.1", stream: true, max_tokens: 4096, temperature: 0.7,
      messages: [{ role: "user", content: "ping" }],
    });
    // Deltas are concatenated into one answer, and the meta frame lands as chips.
    expect(await screen.findByText("pong")).toBeInTheDocument();
    expect(screen.getByText("chat-up")).toBeInTheDocument();
    expect(screen.getByText("200")).toBeInTheDocument();

    // The second turn replays the transcript rather than only the new message.
    fireEvent.change(composer, { target: { value: "again" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(backend.chat).toHaveBeenCalledTimes(2));
    expect(backend.chat.mock.calls[1]?.[0]).toMatchObject({
      messages: [
        { role: "user", content: "ping" },
        { role: "assistant", content: "pong" },
        { role: "user", content: "again" },
      ],
    });
  });

  it("surfaces an upstream refusal frame as a turn error instead of an empty answer", async () => {
    const backend = mockBackend({
      chat: () => sse([
        { event: "meta", data: JSON.stringify({ status: 429, latency_ms: 3, model: "gpt-5.1" }) },
        { event: "error", data: JSON.stringify({ error: { message: "rate limited upstream" } }) },
        { event: "done", data: "{}" },
      ]),
    });
    renderWorkbench();
    fireEvent.click(await screen.findByText("Playground"));
    const composer = await screen.findByPlaceholderText("Type a message — ⌘/Ctrl + Enter to send…");
    fireEvent.change(composer, { target: { value: "ping" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(backend.chat).toHaveBeenCalledOnce());
    expect(await screen.findByRole("alert")).toHaveTextContent("rate limited upstream");
  });

  it("keeps the reply when a channel ignores the stream and answers buffered", async () => {
    const backend = mockBackend({
      chat: () => sse([
        { event: "meta", data: JSON.stringify({ status: 200, latency_ms: 5, model: "gpt-5.1" }) },
        { event: "raw", data: JSON.stringify({ choices: [{ message: { role: "assistant", content: "buffered" } }] }) },
        { event: "done", data: "{}" },
      ]),
    });
    renderWorkbench();
    fireEvent.click(await screen.findByText("Playground"));
    const composer = await screen.findByPlaceholderText("Type a message — ⌘/Ctrl + Enter to send…");
    fireEvent.change(composer, { target: { value: "ping" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(backend.chat).toHaveBeenCalledOnce());
    expect(await screen.findByText("buffered")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("capability catalog sync", () => {
  beforeEach(() => {
    localStorage.clear(); sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => {
    cleanup(); clients.splice(0).forEach((client) => client.clear()); vi.unstubAllGlobals();
  });

  it("previews the plan and only writes once the operator confirms", async () => {
    const backend = mockBackend();
    renderWorkbench();
    fireEvent.click(await screen.findByText("Capabilities"));

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
    expect(backend.catalogSync.mock.calls[0]?.[0]).toEqual({
      capabilities: true, metadata: true, prices: true,
    });
  });

  it("hides the catalog action when the gateway has no index configured", async () => {
    mockBackend();
    // The console can outrun the server it talks to, so an empty source list
    // must disable the action rather than offer a sync that cannot run.
    const inner = globalThis.fetch as (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path === "/admin/model-capabilities/catalog") {
        return json({ state: null, sources: [], scheduled: false, prices_enabled: false });
      }
      return inner(input, init);
    }));
    renderWorkbench();
    fireEvent.click(await screen.findByText("Capabilities"));
    const sync = await screen.findByRole("button", { name: "Sync from catalogs" });
    await waitFor(() => expect(sync).toBeDisabled());
    // Nothing is offered, and no plan is fetched behind the scenes.
    expect(screen.queryByText("Sync from the public model indexes")).not.toBeInTheDocument();
  });
});
