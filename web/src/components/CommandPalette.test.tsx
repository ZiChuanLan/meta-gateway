import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { Boxes } from "lucide-react";
import { ApiClient } from "../api/client";
import { I18nProvider } from "../i18n";
import type { ConsoleRole } from "../session";
import { CommandPalette } from "./CommandPalette";

const identity = vi.hoisted(() => ({ role: "member" as ConsoleRole | null }));
vi.mock("../session", async (load) => {
  const actual = await load<typeof import("../session")>();
  return {
    ...actual,
    useSession: () => ({
      role: identity.role,
      client: new ApiClient("test-token"),
    }),
  };
});

const originalScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollIntoView");
beforeEach(() => {
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: vi.fn(),
  });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
  if (originalScroll)
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", originalScroll);
  else Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView");
});

function mount(role: ConsoleRole | null) {
  identity.role = role;
  localStorage.setItem("meta-gateway.locale", "en");
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // Even cached staff results must not be visible to members.
  qc.setQueryData(["command-search", "Models"], {
    channels: [{ id: 9, name: "private-channel", url: "https://example.test" }],
    routes: [],
    credentials: [],
    logs: [],
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider>
        <MemoryRouter>
          <CommandPalette
            open
            onClose={() => {}}
            nav={[{ to: "/models", label: "Models", icon: Boxes }]}
          />
        </MemoryRouter>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

it("lets members search navigation without fetching or displaying administrator records", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  mount("member");
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "Models" } });
  // Wait past the component debounce, not just its initial navigation render.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 300));
  });
  expect(screen.getByRole("button", { name: /Models/ })).toBeInTheDocument();
  expect(screen.queryByText("private-channel")).not.toBeInTheDocument();
  expect(fetcher).not.toHaveBeenCalled();
});

it.each([null, "owner", "admin"] as const)("keeps gateway search for %s", async (role) => {
  const fetcher = vi.fn(
    async () =>
      new Response(
        JSON.stringify({
          channels: [],
          routes: [],
          credentials: [],
          logs: [],
        }),
      ),
  );
  vi.stubGlobal("fetch", fetcher);
  mount(role);
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "gateway" } });
  await waitFor(() => expect(fetcher).toHaveBeenCalled());
});
