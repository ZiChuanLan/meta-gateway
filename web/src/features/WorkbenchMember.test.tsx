import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../i18n";
import type { ConsoleRole } from "../session";
import { ToastProvider } from "../toast";
import Workbench from "./Workbench";

// A member, not staff: the workbench then runs the member runner, which spends
// one of their own tokens against the real /v1 instead of the admin probe.
const identity = vi.hoisted(() => ({ role: "member" as ConsoleRole | null }));
vi.mock("../session", async (load) => {
  const actual = await load<typeof import("../session")>();
  return {
    ...actual,
    useSession: () => ({
      role: identity.role,
      client: null,
      user: { id: 7, username: "lin", role: "member" },
    }),
  };
});

const clients: QueryClient[] = [];

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** The member's catalogue row: the same shape /me/model-catalog returns. */
function catalogueRow(overrides: Record<string, unknown> = {}) {
  return {
    name: "gpt-5.1",
    vendor: "openai",
    kind: "chat",
    context_window: 128000,
    input_modalities: "text",
    output_modalities: "text",
    endpoints: "/v1/chat/completions",
    candidates: 1,
    ...overrides,
  };
}

function streamedReply(...deltas: string[]) {
  const frames = [
    ...deltas.map((content) => ({ data: JSON.stringify({ choices: [{ delta: { content } }] }) })),
  ];
  const text = frames.map((frame) => `data: ${frame.data}\n\n`).join("") + "data: [DONE]\n\n";
  return new Response(text, { status: 200, headers: { "Content-Type": "text/event-stream" } });
}

function mockMemberBackend(
  options: {
    catalogue?: unknown;
    keys?: unknown;
    reveal?: () => Promise<Response> | Response;
    chat?: (request: Record<string, unknown>, init?: RequestInit) => Response;
  } = {},
) {
  const chat = vi.fn(options.chat ?? (() => streamedReply("po", "ng")));
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), "http://localhost").pathname;
    const method = init?.method ?? "GET";
    if (path === "/me/model-catalog") return json(options.catalogue ?? [catalogueRow()]);
    if (path === "/me/keys")
      return json(
        options.keys ?? [
          {
            id: 5,
            name: "default-key",
            enabled: true,
            hint: "mg-0ab…",
            models: "",
            expires_at: "",
            allowed_ips: "",
            plan_id: 0,
            created_at: "",
          },
        ],
      );
    if (path === "/me/keys/5/reveal" && method === "POST")
      return options.reveal ? options.reveal() : json({ token: "mg-member-token" });
    if (path === "/v1/chat/completions" && method === "POST") {
      return chat(JSON.parse(String(init?.body)) as Record<string, unknown>, init);
    }
    return json({});
  });
  vi.stubGlobal("fetch", fetchMock);
  return { chat, fetchMock };
}

function renderWorkbench() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ToastProvider>
          <MemoryRouter>
            <Workbench />
          </MemoryRouter>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("member workbench", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    // No deployment token: a member signs in with a cookie session.
    localStorage.setItem("meta-gateway.team-console", "1");
  });
  afterEach(() => {
    cleanup();
    clients.splice(0).forEach((client) => client.clear());
    vi.unstubAllGlobals();
  });

  // The whole point of the runner: a member gets the console's console-quality
  // workbench (tabs, threaded turns, streaming) while the call underneath is
  // their own, so quota and billing see it.
  it("runs the same workbench against the member's own token and the real /v1", async () => {
    const backend = mockMemberBackend();
    renderWorkbench();
    fireEvent.click(await screen.findByText("Playground"));
    const picker = await screen.findByRole("button", { name: "Model" });
    expect(picker).toHaveTextContent("gpt-5.1");
    // The connection picker is the member's token, labelled as such.
    const connections = screen.getByRole("combobox", { name: "Which token" });
    expect(
      within(connections)
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual(["Auto (first usable token)", "default-key"]);

    const composer = screen.getByPlaceholderText("Type a message — ⌘/Ctrl + Enter to send…");
    fireEvent.change(composer, { target: { value: "ping" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(backend.chat).toHaveBeenCalledOnce());
    expect(backend.chat.mock.calls[0]?.[0]).toMatchObject({
      model: "gpt-5.1",
      stream: true,
      messages: [{ role: "user", content: "ping" }],
    });
    // The probe spent the member's token against the real relay endpoint —
    // never an admin probe path.
    const init = backend.chat.mock.calls[0]?.[1];
    const headers = new Headers(init?.headers);
    expect(headers.get("Authorization")).toBe("Bearer mg-member-token");
    expect(
      backend.fetchMock.mock.calls.some(([path]) => String(path).includes("/admin/try/")),
    ).toBe(false);
    expect(await screen.findByText("pong")).toBeInTheDocument();
  });

  it("shows a refusal from the relay as the turn's error, not an empty answer", async () => {
    const backend = mockMemberBackend({
      chat: () => json({ error: { message: "insufficient quota for this model" } }, 403),
    });
    renderWorkbench();
    fireEvent.click(await screen.findByText("Playground"));
    const composer = await screen.findByPlaceholderText("Type a message — ⌘/Ctrl + Enter to send…");
    fireEvent.change(composer, { target: { value: "ping" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(backend.chat).toHaveBeenCalledOnce());
    expect(await screen.findByRole("alert")).toHaveTextContent("insufficient quota");
  });

  it("offers the image tab to a member whose catalogue has an image model", async () => {
    const backend = mockMemberBackend({
      catalogue: [
        catalogueRow(),
        catalogueRow({
          name: "gpt-image-2",
          kind: "image_gen",
          endpoints: "/v1/images/generations",
          output_modalities: "image",
        }),
      ],
    });
    renderWorkbench();
    const picker = await screen.findByRole("button", { name: "Model" });
    fireEvent.click(picker);
    expect(
      within(screen.getByRole("listbox", { name: "Model" }))
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual(["gpt-image-2"]);
    expect(screen.getByRole("button", { name: "Generate image" })).toBeInTheDocument();
    void backend;
  });
});
