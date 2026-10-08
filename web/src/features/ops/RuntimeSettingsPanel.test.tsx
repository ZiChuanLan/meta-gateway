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

/**
 * Only the keys a test needs are present: everything else renders empty, and an
 * absent key is not what these tests are about. `env_bootstrap` is the
 * deployment layer every "changed" badge is measured against.
 */
const payload = (
  editable: Record<string, unknown> = {},
  bootstrap: Record<string, unknown> = {},
) => ({
  source: "admin_override",
  has_override: true,
  note: "",
  updated_at: "2026-10-06T00:00:00Z",
  editable: { cross_channel_failover_enabled: true, retry_times: 2, ...editable },
  env_bootstrap: { cross_channel_failover_enabled: true, retry_times: 2, ...bootstrap },
});

/**
 * A fake runtime-settings endpoint that remembers what was PUT: instant save
 * means the panel re-reads after every write, and a mock that always answered
 * with the pre-write values would look like the save was thrown away.
 */
function runtimeServer(read: () => Record<string, unknown>) {
  const puts: Array<Record<string, unknown>> = [];
  let stored: Record<string, unknown> | null = null;
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).split("?")[0];
    if (path !== "/admin/runtime-settings") return new Response(JSON.stringify({}));
    const base = read();
    if (init?.method === "PUT") {
      const body = JSON.parse(String(init.body)) as Record<string, unknown>;
      puts.push(body);
      stored = body;
      return new Response(JSON.stringify({ ...base, editable: body }));
    }
    return new Response(JSON.stringify({ ...base, editable: stored ?? base.editable }));
  });
  return { fetcher, puts };
}

/** The navigation owns sections: a section is one click away, always. */
async function openSection(name: string) {
  await screen.findByRole("tablist");
  const pill = screen.queryByRole("button", { name });
  if (pill) {
    fireEvent.click(pill);
    return;
  }
  for (const cat of [/Forwarding & routing/, /Health & automation/, /Data & operations/]) {
    const tab = screen.queryByRole("tab", { name: cat });
    if (tab) {
      fireEvent.click(tab);
      const target = screen.queryByRole("button", { name });
      if (target) {
        fireEvent.click(target);
        return;
      }
    }
  }
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

// The page used to be nineteen peer cards behind a chip index; the sidebar
// states which three groups they fall into and shows one section at a time.
it("groups the sections semantically into category tabs with high information density", async () => {
  vi.stubGlobal("fetch", runtimeServer(() => payload()).fetcher);
  mount(<RuntimeSettingsPanel />);

  expect(await screen.findByText("Forwarding & routing")).toBeInTheDocument();
  expect(screen.getByText("Health & automation")).toBeInTheDocument();
  expect(screen.getByText("Data & operations")).toBeInTheDocument();

  // In the active category, sections are visible together in the dense grid!
  expect(screen.getByRole("heading", { name: "Relay failover" })).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "Routing" })).toBeInTheDocument();
  expect(screen.getByText("Per-channel concurrency ceiling")).toBeInTheDocument();

  // Switch to Health & automation tab
  fireEvent.click(screen.getByRole("tab", { name: /Health & automation/ }));
  expect(
    await screen.findByRole("heading", { name: /Failure cooldown|Cooldown/ }),
  ).toBeInTheDocument();
  expect(screen.queryByRole("heading", { name: "Relay failover" })).toBeNull();
});

// I04's other half: a background refetch used to replace the draft whenever the
// query data changed, so another session's save silently erased what was being
// typed. The page must say the server moved and let the operator choose.
it("reports a changed server snapshot instead of overwriting the draft", async () => {
  let current = payload();
  vi.stubGlobal("fetch", runtimeServer(() => current).fetcher);
  const qc = mount(<RuntimeSettingsPanel />);
  const rounds = (await screen.findByLabelText(/Retry rounds/)) as HTMLInputElement;
  expect(rounds.value).toBe("2");

  fireEvent.change(rounds, { target: { value: "40" } });
  expect(rounds.value).toBe("40");

  current = payload({ retry_times: 9 });
  await qc.invalidateQueries({ queryKey: ["runtime-settings"] });

  expect(await screen.findByText(/The server's runtime parameters changed/)).toBeInTheDocument();
  // The edit is still there — the notice is the point, not a silent reset.
  expect(rounds.value).toBe("40");

  fireEvent.click(screen.getByRole("button", { name: "Reload server settings" }));
  await waitFor(() => expect(rounds.value).toBe("9"));
  expect(screen.queryByText(/The server's runtime parameters changed/)).toBeNull();
});

it("keeps the local draft when the operator chooses their side", async () => {
  let current = payload();
  vi.stubGlobal("fetch", runtimeServer(() => current).fetcher);
  const qc = mount(<RuntimeSettingsPanel />);
  const rounds = (await screen.findByLabelText(/Retry rounds/)) as HTMLInputElement;
  fireEvent.change(rounds, { target: { value: "40" } });

  current = payload({ retry_times: 9 });
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

// There is no Save button any more, so the only moment that may write is the
// moment the operator leaves the field — never a keystroke, because "300" on
// its way to "3" is a valid-looking timeout that must never reach production.
it("writes the draft when a field is left, and not while it is being typed", async () => {
  const server = runtimeServer(() => payload());
  vi.stubGlobal("fetch", server.fetcher);
  mount(<RuntimeSettingsPanel />);
  const rounds = (await screen.findByLabelText(/Retry rounds/)) as HTMLInputElement;

  fireEvent.change(rounds, { target: { value: "3" } });
  expect(server.puts).toHaveLength(0);
  fireEvent.change(rounds, { target: { value: "30" } });
  expect(server.puts).toHaveLength(0);

  fireEvent.focusOut(rounds);
  await waitFor(() => expect(server.puts).toHaveLength(1));
  // The whole draft goes up, not just the touched field: the endpoint replaces
  // the override as one unit.
  expect(server.puts[0]!.retry_times).toBe(30);
  expect(server.puts[0]!.cross_channel_failover_enabled).toBe(true);

  expect(await screen.findByText(/Saved and applied without restart/)).toBeInTheDocument();
});

it("saves a checkbox on the change itself, with no blur to wait for", async () => {
  const server = runtimeServer(() => payload());
  vi.stubGlobal("fetch", server.fetcher);
  mount(<RuntimeSettingsPanel />);
  await openSection("Routing");

  fireEvent.click(screen.getByRole("checkbox", { name: /Latency-aware routing/ }));
  await waitFor(() => expect(server.puts).toHaveLength(1));
  expect(server.puts[0]!.routing_latency_aware).toBe(true);
});

// With instant save the operator needs to see, without a Save button to compare
// against, which values override the deployment and which are just the default.
it("marks the fields that override the deployment default, and their section", async () => {
  vi.stubGlobal(
    "fetch",
    runtimeServer(() => payload({ retry_times: 9 }, { retry_times: 2 })).fetcher,
  );
  mount(<RuntimeSettingsPanel />);

  // The section dot is what makes the sidebar useful: a change made three
  // sections ago must still be visible from here.
  const relay = await screen.findByRole("button", { name: "Relay failover" });
  expect(relay.querySelector(".runtime-nav-dot")).not.toBeNull();
  expect(
    screen.getByRole("button", { name: "Routing" }).querySelector(".runtime-nav-dot"),
  ).toBeNull();

  const changedRow = screen.getByLabelText(/Retry rounds/).closest("label")!;
  expect(changedRow.textContent).toContain("Changed");
  const defaultRow = screen.getByLabelText(/Same-key re-sends/).closest("label")!;
  expect(defaultRow.textContent).toContain("Default");
});

it("restores one section to the deployment defaults without touching the rest", async () => {
  const server = runtimeServer(() =>
    payload({ retry_times: 9, sticky_enabled: false }, { retry_times: 2, sticky_enabled: true }),
  );
  vi.stubGlobal("fetch", server.fetcher);
  mount(<RuntimeSettingsPanel />);

  const restoreButtons = await screen.findAllByRole("button", { name: "Restore this section" });
  fireEvent.click(restoreButtons[0]!);
  await waitFor(() => expect(server.puts).toHaveLength(1));
  expect(server.puts[0]!.retry_times).toBe(2);
  // Sticky sessions are another section's business and stay as the operator
  // left them.
  expect(server.puts[0]!.sticky_enabled).toBe(false);
});

it("shows the environment layer read-only, folded into the service section", async () => {
  vi.stubGlobal(
    "fetch",
    runtimeServer(() => ({
      ...payload(),
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
    })).fetcher,
  );
  mount(<RuntimeSettingsPanel />);
  await openSection("Service & network");

  // Folded until asked for: the variable list is a fact about the deployment,
  // not a knob, and it should not out-shout the settings above it.
  const toggle = await screen.findByRole("button", { name: /Deployment parameters \(env\)/ });
  expect(toggle).toHaveAttribute("aria-expanded", "false");
  expect(screen.queryByLabelText("Filter variables")).toBeNull();
  fireEvent.click(toggle);

  // Only what the container actually set is listed until asked otherwise: a
  // value that fell back to a code default is the same on every deployment.
  expect(await screen.findByText("OUTBOUND_HEADER_TIMEOUT_SECONDS")).toBeInTheDocument();
  expect(screen.getByText("MASTER_KEY")).toBeInTheDocument();
  expect(screen.queryByText("OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS")).toBeNull();
  expect(screen.getAllByText("env", { selector: ".runtime-param-source" })).toHaveLength(2);

  fireEvent.click(screen.getByRole("button", { name: "Show all 1" }));
  expect(screen.getByText("OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS")).toBeInTheDocument();
  expect(screen.getByText("5m0s")).toBeInTheDocument();
  expect(screen.getAllByText("default", { selector: ".runtime-param-source" })).toHaveLength(1);

  // Filtering is presentation only: the draft stays clean, so the page never
  // asks about unsaved changes.
  fireEvent.change(screen.getByLabelText("Filter variables"), { target: { value: "image" } });
  expect(screen.queryByText("OUTBOUND_HEADER_TIMEOUT_SECONDS")).toBeNull();
  expect(screen.getByText("OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS")).toBeInTheDocument();
  expect(screen.queryByText("Unsaved changes")).toBeNull();
});
