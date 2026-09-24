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
  /** Replaces the /admin/routes/overview payload (member shapes matter here). */
  overview?: unknown[];
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
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), "http://localhost").pathname;
    if (path === "/admin/routes/overview")
      return json(options.overview ?? [
        { route: { id: 1, model_pattern: model.model, enabled: true }, members: [{ channel: { id: 7, name: "image-up" }, member: { id: 71, channel_id: 7, priority: 9, weight: 1 } }] },
        { route: { id: 2, model_pattern: chatModel.model, enabled: true }, members: [{ channel: { id: 8, name: "chat-up" }, member: { id: 81, channel_id: 8, priority: 5, weight: 2 } }] },
        { route: { id: 3, model_pattern: "disabled-image", enabled: false }, members: [] },
      ]);
    if (path === "/admin/model-capabilities/resolve") return resolve();
    if (path === "/admin/model-capabilities") return json({ items: [model, chatModel] });
    if (path === "/admin/model-capabilities/catalog") return json({
      state: null, sources: ["litellm", "models.dev"], scheduled: true, prices_enabled: true,
    });
    if (path === "/admin/try/image") return image(JSON.parse(String(init?.body)) as Record<string, unknown>);
    if (path === "/admin/try/chat") return chat(JSON.parse(String(init?.body)) as Record<string, unknown>);
    return json({});
  }));
  return { resolve, image, chat };
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
    // Opening the picker is what materializes the options, and the label
    // carries the connection serving the model. A disabled route is not a
    // candidate even though it is present in the overview payload.
    fireEvent.click(screen.getByRole("button", { name: "Model" }));
    expect(within(screen.getByRole("listbox", { name: "Model" })).getAllByRole("option")
      .map((option) => option.textContent)).toEqual(["gpt-image-2 · image-up"]);
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
    expect(screen.getByRole("button", { name: "Model" })).toBeDisabled();
    fireEvent.click(screen.getByText("Playground"));
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
    const picker = await screen.findByRole("button", { name: "Model" });
    // The trigger reports the selected model by name alone: the chat picker is
    // read while choosing what to think with, and the serving connection is
    // noise there (it is also a guess — the primary member is not necessarily
    // the one a given request lands on). The upstream picker below is where a
    // specific connection is chosen deliberately.
    expect(picker).toHaveTextContent("gpt-5.1");
    expect(picker).not.toHaveTextContent("chat-up");
    // The image model is routed but answers on /v1/images/*, so it is not a
    // candidate for a chat turn — opening the list proves it is not offered.
    fireEvent.click(picker);
    expect(within(screen.getByRole("listbox", { name: "Model" })).getAllByRole("option")
      .map((option) => option.textContent)).toEqual(["gpt-5.1"]);
    // Close the list again — it portals to <body>, so leaving it up would keep
    // its option buttons in every later role query.
    fireEvent.click(picker);
    expect(screen.queryByRole("listbox", { name: "Model" })).not.toBeInTheDocument();
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

  it("lists one row per member with its 原模型, and pins that member", async () => {
    // A shared alias: ONE channel reaches two differently named upstream models.
    // Both rows used to print "sensenova · p0/w100" and both sent the same
    // channel id, so the second upstream was neither visible nor reachable.
    const backend = mockBackend({
      overview: [
        {
          route: { id: 1, model_pattern: "alias", enabled: true },
          members: [
            { channel: { id: 7, name: "sensenova" }, member: { id: 41, channel_id: 7, priority: 0, weight: 100 } },
            { channel: { id: 7, name: "sensenova" }, member: { id: 42, channel_id: 7, priority: 0, weight: 100, mapping_json: '{"real":"SenseNova-V6"}' } },
          ],
        },
      ],
      resolve: () => json({ items: { alias: chatCapability("alias") } }),
      chat: () => streamedReply("pong"),
    });
    renderWorkbench();
    fireEvent.click(await screen.findByText("Playground"));
    const picker = await screen.findByRole("combobox", { name: "Upstream connection" });
    expect(within(picker).getAllByRole("option").map((option) => option.textContent)).toEqual([
      "Auto (gateway routing)",
      "sensenova · p0/w100",
      "sensenova · origin SenseNova-V6 · p0/w100",
    ]);

    // Choosing the second row pins the MEMBER: both rows share channel 7, so a
    // channel pin could only ever have reached the first of them.
    fireEvent.change(picker, { target: { value: "42" } });
    const composer = screen.getByPlaceholderText("Type a message — ⌘/Ctrl + Enter to send…");
    fireEvent.change(composer, { target: { value: "ping" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(backend.chat).toHaveBeenCalledOnce());
    expect(backend.chat.mock.calls[0]?.[0]).toMatchObject({ model: "alias", member_id: 42 });
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
