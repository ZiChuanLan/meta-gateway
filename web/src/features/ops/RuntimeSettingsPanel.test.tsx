import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { ToastProvider } from "../../toast";
import { RuntimeSettingsPanel } from "./RuntimeSettingsPanel";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

function mount(
  children: ReactNode,
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
  localStorage.setItem("meta-gateway.locale", "en");
  localStorage.setItem("meta-gateway.admin-token", "test-token");
  render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <MemoryRouter>{children}</MemoryRouter>
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
  return client;
}

/** Only the keys the routing card needs are present: everything else renders
 *  empty, and an absent key is not what this test is about. */
const settings = (editable: Record<string, unknown>) => ({
  source: "admin_override",
  has_override: true,
  note: "",
  updated_at: "2026-10-06T00:00:00Z",
  editable: { cross_channel_failover_enabled: true, retry_times: 2, ...editable },
});

function runtimeFetcher(payload: () => unknown) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input).split("?")[0];
    if (path === "/admin/runtime-settings") return new Response(JSON.stringify(payload()));
    return new Response(JSON.stringify({}));
  });
}

async function openRoutingCard() {
  // The nav has a "Routing" button too; only the group toggle carries
  // aria-expanded, so the filter picks the collapsible header.
  fireEvent.click(await screen.findByRole("button", { name: /Routing/, expanded: false }));
  return screen.findByLabelText(/Retry rounds/) as Promise<HTMLInputElement>;
}

it("shows an initial load failure with a working retry instead of endless loading", async () => {
  const fetcher = vi.fn(
    async (_input: RequestInfo | URL) =>
      new Response('{"error":"runtime_read_failed"}', { status: 500 }),
  );
  vi.stubGlobal("fetch", fetcher);
  mount(<RuntimeSettingsPanel />);
  const retry = await screen.findByRole("button", { name: "Retry" });
  const reads = () =>
    fetcher.mock.calls.filter(([path]) => String(path).includes("/runtime-settings")).length;
  const before = reads();
  expect(before).toBeGreaterThan(0);
  fireEvent.click(retry);
  await waitFor(() => expect(reads()).toBeGreaterThan(before));
});

// I04's other half: a background refetch used to replace the draft whenever the
// query data changed, so another session's save silently erased what was being
// typed. The page must say the server moved and let the operator choose.
it("reports a changed server snapshot instead of overwriting the draft", async () => {
  let payload = settings({});
  vi.stubGlobal(
    "fetch",
    runtimeFetcher(() => payload),
  );
  const qc = mount(<RuntimeSettingsPanel />);
  const rounds = await openRoutingCard();
  expect(rounds.value).toBe("2");

  fireEvent.change(rounds, { target: { value: "40" } });
  expect(rounds.value).toBe("40");

  payload = settings({ retry_times: 9 });
  await qc.invalidateQueries({ queryKey: ["runtime-settings"] });

  expect(await screen.findByText(/The server's runtime parameters changed/)).toBeInTheDocument();
  // The edit is still there — the notice is the point, not a silent reset.
  expect(rounds.value).toBe("40");

  fireEvent.click(screen.getByRole("button", { name: "Reload server settings" }));
  await waitFor(() => expect(rounds.value).toBe("9"));
  expect(screen.queryByText(/The server's runtime parameters changed/)).toBeNull();
});

it("keeps the local draft when the operator chooses their side", async () => {
  let payload = settings({});
  vi.stubGlobal(
    "fetch",
    runtimeFetcher(() => payload),
  );
  const qc = mount(<RuntimeSettingsPanel />);
  const rounds = await openRoutingCard();
  fireEvent.change(rounds, { target: { value: "40" } });

  payload = settings({ retry_times: 9 });
  await qc.invalidateQueries({ queryKey: ["runtime-settings"] });
  await screen.findByText(/The server's runtime parameters changed/);

  fireEvent.click(screen.getByRole("button", { name: "Keep my changes" }));
  await waitFor(() =>
    expect(screen.queryByText(/The server's runtime parameters changed/)).toBeNull(),
  );
  expect(rounds.value).toBe("40");
  // The unsaved marker stays: the draft is still uncommitted.
  expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
});

it("shows the environment layer read-only, with the source of each value", async () => {
  vi.stubGlobal(
    "fetch",
    runtimeFetcher(() => ({
      ...settings({
        // The ops group renders the check-in card too, which reads these.
        checkin_cron: "0 8 * * *",
        checkin_enabled: false,
        update_check_enabled: false,
      }),
      deployment_parameters: [
        {
          key: "OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS",
          kind: "duration",
          value: "5m0s",
          from_env: false,
          secret: false,
        },
        {
          key: "OUTBOUND_HEADER_TIMEOUT_SECONDS",
          kind: "duration",
          value: "1m0s",
          from_env: true,
          secret: false,
        },
        { key: "MASTER_KEY", kind: "string", value: "••••••", from_env: true, secret: true },
      ],
    })),
  );
  mount(<RuntimeSettingsPanel />);
  fireEvent.click(await screen.findByRole("button", { name: /Alerts & Ops/, expanded: false }));

  // Both the value and whether it came from the environment are shown: a
  // variable compose never passed is how a request ends up dying at a ceiling
  // nobody remembers configuring.
  expect(await screen.findByText("OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS")).toBeInTheDocument();
  expect(screen.getByText("5m0s")).toBeInTheDocument();
  expect(screen.getAllByText("env", { selector: ".runtime-param-source" })).toHaveLength(2);
  expect(screen.getAllByText("default", { selector: ".runtime-param-source" })).toHaveLength(1);

  // Filtering is presentation only: the draft stays clean, so the page never
  // asks about unsaved changes.
  fireEvent.change(screen.getByLabelText("Filter variables"), { target: { value: "image" } });
  expect(screen.queryByText("OUTBOUND_HEADER_TIMEOUT_SECONDS")).toBeNull();
  expect(screen.getByText("OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS")).toBeInTheDocument();
  expect(screen.queryByText("Unsaved changes")).toBeNull();
});
