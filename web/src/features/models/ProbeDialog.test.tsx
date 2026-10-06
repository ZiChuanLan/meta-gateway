import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { ToastProvider } from "../../toast";
import { SessionProvider } from "../../session";
import { ProbeDialog } from "./ProbeDialog";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});
it("does not treat cleared selection as permission to probe everything", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  localStorage.setItem("meta-gateway.admin-token", "test-token");
  const fetcher = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
    const path = String(input);
    const body =
      path === "/admin/channels"
        ? [{ id: 1, name: "Upstream" }]
        : path === "/admin/routes/overview"
          ? [
              {
                route: { id: 1, enabled: true, model_pattern: "model" },
                members: [{ member: { enabled: true, channel_id: 1 } }],
              },
            ]
          : [];
    return new Response(JSON.stringify(body));
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <ProbeDialog onClose={() => {}} />
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.getByRole("button", { name: "Start probing" })).toBeEnabled());
  expect(screen.getByText("No probe results yet")).toBeInTheDocument();
  fireEvent.click(screen.getAllByRole("button", { name: /^Clear$/ })[0]!);
  expect(screen.getByRole("button", { name: "Start probing" })).toBeDisabled();
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
});
