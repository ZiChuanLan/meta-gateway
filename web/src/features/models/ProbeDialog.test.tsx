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

// The schedule's scope and the run's scope are the same decision, so the dialog
// can write the schedule from the pick lists — but only the plan and the scope:
// everything else in the runtime settings has to survive the write.
it("sets the scheduled scope from the current selection and keeps the rest of the settings", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  localStorage.setItem("meta-gateway.admin-token", "test-token");
  const stored = {
    retry_times: 3,
    probe_cron: "0 3 * * *",
    probe_channels: [] as number[],
    probe_models: [] as string[],
  };
  const puts: Record<string, unknown>[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/admin/runtime-settings") {
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        puts.push(body);
        Object.assign(stored, body);
        return new Response(JSON.stringify({ editable: stored }));
      }
      return new Response(JSON.stringify({ editable: stored }));
    }
    const body =
      path === "/admin/channels"
        ? [
            { id: 1, name: "Upstream A" },
            { id: 2, name: "Upstream B" },
          ]
        : path === "/admin/routes/overview"
          ? [
              {
                route: { id: 1, enabled: true, model_pattern: "model" },
                members: [
                  { member: { enabled: true, channel_id: 1 } },
                  { member: { enabled: true, channel_id: 2 } },
                ],
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

  // The plan is the runtime setting, shown where the scope is chosen.
  expect(await screen.findByText("Scheduled scope: all channels · all models")).toBeInTheDocument();
  expect(await screen.findByText("The schedule already covers this selection")).toBeInTheDocument();

  // Narrow the run to one channel: the schedule no longer covers it.
  fireEvent.click(screen.getByRole("checkbox", { name: /Upstream A/ }));
  const useSelection = await screen.findByRole("button", {
    name: "Use this selection for the schedule",
  });
  fireEvent.click(useSelection);

  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toMatchObject({ probe_channels: [2], probe_models: [], probe_cron: "0 3 * * *" });
  expect(puts[0]!.retry_times).toBe(3);
  expect(await screen.findByText("Scheduled scope: 1 channels · all models")).toBeInTheDocument();
});
