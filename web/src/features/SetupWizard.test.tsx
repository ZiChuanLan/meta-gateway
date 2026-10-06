import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { SetupWizard } from "./SetupWizard";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});
it("offers personal/team setup and does not advance after saving sync defaults fails", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  localStorage.setItem("meta-gateway.admin-token", "test");
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path === "/admin/mode")
      return new Response(JSON.stringify({ mode: "personal", has_owner: false, role: "owner" }));
    return new Response(JSON.stringify({ error: "save failed" }), { status: 500 });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
        })
      }
    >
      <I18nProvider>
        <SessionProvider>
          <MemoryRouter>
            <SetupWizard />
          </MemoryRouter>
        </SessionProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByLabelText("Operating mode")).toHaveValue("personal");
  expect(screen.getByRole("heading", { name: "Create owner account" })).toBeInTheDocument();
  fireEvent.click(screen.getAllByRole("radio")[1]!);
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([path]) => String(path).includes("runtime"))).toBe(true),
  );
  expect(screen.getByRole("heading", { name: "Welcome to Meta Gateway" })).toBeInTheDocument();
});
