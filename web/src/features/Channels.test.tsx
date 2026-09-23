import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { ToastProvider } from "../toast";
import { Channels, capabilityFlags, channelReadiness } from "./Channels";
import { isCustomChannelType } from "./channels/helpers";
import type { ChannelOverview } from "../api/types";

function renderChannels() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <MemoryRouter initialEntries={["/"]}>
              <Routes>
                <Route path="/" element={<Channels />} />
              </Routes>
            </MemoryRouter>
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("Channels two-phase create", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("saves the connection before verify and retries without re-creating", async () => {
    const createConnection = vi.fn(async () =>
      jsonResponse({
        channel: {
          id: 21,
          name: "api.example.com",
          site_id: 1,
          credential_id: 11,
          base_url: "",
          models_csv: "",
          group_name: "default",
          priority: 0,
          weight: 100,
          status: "enabled",
          type_hint: "openai-compatible",
          created_at: "",
          updated_at: "",
        },
        credential_id: 11,
      }),
    );
    let refreshAttempts = 0;
    const refreshChannel = vi.fn(async () => {
      refreshAttempts += 1;
      if (refreshAttempts === 1) {
        return jsonResponse({ error: "upstream unauthorized" }, 502);
      }
      return jsonResponse({
        channel_id: 21,
        models: [{ id: "gpt-test" }],
        created_routes: 1,
        latency_ms: 12,
      });
    });

    const overviews: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input).split("?")[0];
        const method = (init?.method ?? "GET").toUpperCase();
        if (path === "/admin/channels/overview" && method === "GET") {
          return jsonResponse(overviews);
        }
        if (path === "/admin/sites" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/connections" && method === "POST") {
          const response = await createConnection();
          const payload = await response.clone().json();
          const channel = payload.channel;
          overviews.push({
            channel,
            credential_kind: "api_key",
            checkin_enabled: false,
            has_user_credential: false,
            has_platform_user_id: false,
            has_api_key: true,
            site_usable: true,
            credential_usable: true,
            model_count: 0,
            discovered_model_count: 0,
            cooling_member_count: 0,
            failure_count: 0,
            checkin_supported: true,
            account_supported: true,
            last_error: "",
            last_checked_at: null,
            last_latency_ms: 0,
          });
          return response;
        }
        if (
          path === "/admin/discovery/channels/21/refresh" &&
          method === "POST"
        ) {
          const response = await refreshChannel();
          if (response.ok) {
            const current = overviews[0] as {
              model_count: number;
              channel: { id: number };
            };
            current.model_count = 1;
          }
          return response;
        }
        return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
      }),
    );

    renderChannels();
    expect(
      await screen.findByRole("heading", { name: "Connections" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Add connection" }));
    fireEvent.change(screen.getByPlaceholderText("https://api.example.com"), {
      target: { value: "https://api.example.com" },
    });
    const secretInput = document.querySelector(
      'input[type="password"]',
    ) as HTMLInputElement;
    fireEvent.change(secretInput, { target: { value: "sk-test" } });
    fireEvent.click(screen.getByRole("button", { name: "Save & verify" }));

    await waitFor(() => expect(createConnection).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(refreshChannel).toHaveBeenCalledTimes(1));
    expect(
      await screen.findByText(/was saved, but model sync failed/i),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Retry verify" }));
    await waitFor(() => expect(refreshChannel).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(
        screen.getByText(/saved and fetched 1 models/i),
      ).toBeInTheDocument(),
    );
    expect(createConnection).toHaveBeenCalledTimes(1);
  });
});

describe("capabilityFlags", () => {
  it("marks access-token-only connections as check-in ready and missing API key", () => {
    const flags = capabilityFlags({
      channel: {
        id: 1,
        name: "demo",
        base_url: "",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "access_token",
      checkin_enabled: true,
      has_user_credential: true,
      has_platform_user_id: true,
      has_api_key: false,
      last_probe_ok: true,
      site_usable: true,
      credential_usable: true,
      model_count: 0,
      discovered_model_count: 0,
      last_latency_ms: 0,
      route_count: 0,
      enabled_member_count: 0,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    });
    expect(flags.checkinCapable).toBe(true);
    expect(flags.checkinOff).toBe(false);
    expect(flags.checkinScheduled).toBe(true);
    expect(flags.missingAPIKey).toBe(true);
    expect(flags.modelsReady).toBe(false);
    expect(
      channelReadiness({
        channel: {
          id: 1,
          name: "demo",
          base_url: "",
          models_csv: "",
          group_name: "default",
          priority: 0,
          weight: 100,
          status: "enabled",
          created_at: "",
          updated_at: "",
        },
        checkin_enabled: false,
        has_user_credential: true,
        has_platform_user_id: false,
        has_api_key: false,
        site_usable: true,
        credential_usable: true,
        model_count: 0,
        discovered_model_count: 0,
        last_latency_ms: 0,
        route_count: 0,
        enabled_member_count: 0,
        cooling_member_count: 0,
        failure_count: 0,
        checkin_supported: true,
        account_supported: true,
      }),
    ).toBe("missing_key");
  });

  it("flags missing API key regardless of access token state", () => {
    const base: ChannelOverview = {
      channel: {
        id: 9,
        name: "demo",
        base_url: "",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "access_token",
      checkin_enabled: true,
      has_user_credential: true,
      has_platform_user_id: true,
      has_api_key: false,
      site_usable: true,
      credential_usable: true,
      model_count: 0,
      discovered_model_count: 0,
      last_latency_ms: 0,
      route_count: 0,
      enabled_member_count: 0,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    };
    // Missing API key is independent of token probe state.
    expect(capabilityFlags({ ...base }).missingAPIKey).toBe(true);
    expect(
      capabilityFlags({ ...base, last_probe_ok: false }).missingAPIKey,
    ).toBe(true);
    expect(
      capabilityFlags({ ...base, last_probe_ok: true }).missingAPIKey,
    ).toBe(true);
    // Without a user credential there is no token state at all.
    expect(
      capabilityFlags({ ...base, has_user_credential: false }).missingAPIKey,
    ).toBe(true);
  });

  it("flags access token problems only when a token exists and its probe failed", () => {
    const base: ChannelOverview = {
      channel: {
        id: 10,
        name: "demo",
        base_url: "",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "access_token",
      checkin_enabled: true,
      has_user_credential: true,
      has_platform_user_id: true,
      has_api_key: true,
      site_usable: true,
      credential_usable: true,
      model_count: 3,
      discovered_model_count: 5,
      last_latency_ms: 0,
      route_count: 1,
      enabled_member_count: 1,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    };
    // Never probed → no verdict.
    expect(capabilityFlags({ ...base }).tokenProblem).toBe(false);
    // Account probe passed → token fine.
    expect(
      capabilityFlags({ ...base, last_account_probe_ok: true }).tokenProblem,
    ).toBe(false);
    // Account probe failed → token is the problem.
    expect(
      capabilityFlags({
        ...base,
        last_account_probe_at: "2026-08-02T00:00:00Z",
        last_account_probe_ok: false,
      }).tokenProblem,
    ).toBe(true);
    // Account probe failure without a timestamp is "never checked", not "failed".
    expect(
      capabilityFlags({ ...base, last_account_probe_ok: false }).tokenProblem,
    ).toBe(false);
    // A failed business probe (api_key chain) is not a token problem.
    expect(
      capabilityFlags({
        ...base,
        last_probe_at: "2026-08-02T00:00:00Z",
        last_probe_ok: false,
      }).tokenProblem,
    ).toBe(false);
    // No token stored → nothing to flag, even with a failed account probe.
    expect(
      capabilityFlags({
        ...base,
        has_user_credential: false,
        last_account_probe_at: "2026-08-02T00:00:00Z",
        last_account_probe_ok: false,
      }).tokenProblem,
    ).toBe(false);
  });

  it("marks check-in as needing user id when token exists without platform_user_id", () => {
    const flags = capabilityFlags({
      channel: {
        id: 3,
        name: "demo",
        base_url: "",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "access_token",
      checkin_enabled: true,
      has_user_credential: true,
      has_platform_user_id: false,
      has_api_key: false,
      site_usable: true,
      credential_usable: true,
      model_count: 0,
      discovered_model_count: 0,
      last_latency_ms: 0,
      route_count: 0,
      enabled_member_count: 0,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    });
    expect(flags.checkinCapable).toBe(false);
    expect(flags.checkinNeedsUserID).toBe(true);
    expect(flags.checkinScheduled).toBe(false);
  });

  it("shows check-in schedule off when user token exists but checkin_enabled is false", () => {
    const flags = capabilityFlags({
      channel: {
        id: 2,
        name: "demo",
        base_url: "",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "api_key",
      checkin_enabled: false,
      has_user_credential: true,
      has_platform_user_id: true,
      has_api_key: true,
      site_usable: true,
      credential_usable: true,
      model_count: 3,
      discovered_model_count: 5,
      last_latency_ms: 10,
      route_count: 1,
      enabled_member_count: 1,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    });
    expect(flags.checkinCapable).toBe(true);
    expect(flags.checkinScheduled).toBe(false);
    expect(flags.checkinOff).toBe(true);
    expect(flags.missingAPIKey).toBe(false);
    expect(flags.noUserToken).toBe(false);
  });
});

describe("Channels create-key double-submit guard", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("creates exactly one upstream key despite rapid repeated clicks", async () => {
    const overview = {
      channel: {
        id: 7,
        name: "newapi-demo",
        base_url: "https://api.example.com",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "access_token",
      checkin_enabled: false,
      has_user_credential: true,
      has_platform_user_id: true,
      has_api_key: false,
      site_usable: true,
      credential_usable: true,
      model_count: 0,
      discovered_model_count: 0,
      last_probe_at: "2026-08-02T00:00:00Z",
      last_probe_ok: true,
      last_latency_ms: 5,
      route_count: 0,
      enabled_member_count: 0,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    };
    let createKeyCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input).split("?")[0] ?? "";
        const method = (init?.method ?? "GET").toUpperCase();
        if (path === "/admin/channels/overview" && method === "GET") {
          return jsonResponse([overview]);
        }
        if (path === "/admin/sites" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/plugins/status" && method === "GET") {
          return jsonResponse([]);
        }
        if (
          /\/admin\/channels\/7\/account\/token-groups$/.test(path) &&
          method === "GET"
        ) {
          return jsonResponse({ groups: ["default"] });
        }
        if (
          /\/admin\/channels\/7\/account\/create-key$/.test(path) &&
          method === "POST"
        ) {
          createKeyCalls += 1;
          return jsonResponse({
            credential_id: 100 + createKeyCalls,
            name: "gateway-default",
            group: "default",
            category: "created",
            message: "ok",
          });
        }
        if (path === "/admin/channels" && method === "GET") {
          return jsonResponse([]);
        }
        return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
      }),
    );

    renderChannels();
    await waitFor(async () => {
      const trigger = screen.getByRole("button", {
        name: /more actions/i,
      });
      trigger.click();
      await new Promise((resolve) => setTimeout(resolve, 0));
      const createItem = screen.getByRole("menuitem", {
        name: /create api key/i,
      });
      createItem.click();
    });

    const dialog = await screen.findByRole("dialog");
    const confirm = await within(dialog).findByRole("button", {
      name: /^create$/i,
    });
    // Rapid-fire the confirm button before React can re-render the disabled state.
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    fireEvent.click(confirm);

    await waitFor(() => expect(createKeyCalls).toBe(1));
    expect(createKeyCalls).toBe(1);
  });

  it("keeps upstream key creation available when the site already has keys", async () => {
    // A group-scoped upstream (New API) typically needs one key per
    // group; existing keys must not hide the create entry.
    const overview = {
      channel: {
        id: 7,
        name: "newapi-demo",
        base_url: "https://api.example.com",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "access_token",
      checkin_enabled: false,
      has_user_credential: true,
      has_platform_user_id: true,
      has_api_key: true,
      site_usable: true,
      credential_usable: true,
      model_count: 0,
      discovered_model_count: 0,
      last_probe_at: "2026-08-02T00:00:00Z",
      last_probe_ok: true,
      last_latency_ms: 5,
      route_count: 0,
      enabled_member_count: 0,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input).split("?")[0] ?? "";
        const method = (init?.method ?? "GET").toUpperCase();
        if (path === "/admin/channels/overview" && method === "GET") {
          return jsonResponse([overview]);
        }
        if (path === "/admin/sites" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/plugins/status" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/channels" && method === "GET") {
          return jsonResponse([]);
        }
        return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
      }),
    );

    renderChannels();
    await waitFor(async () => {
      const trigger = screen.getByRole("button", {
        name: /more actions/i,
      });
      trigger.click();
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(
        screen.getByRole("menuitem", { name: /create api key/i }),
      ).toBeInTheDocument();
    });
  });
});

describe("Channels edit dialog sync mode", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("seeds the picker from the channel's saved sync mode", async () => {
    // Regression: the edit drawer once opened on the default mode instead of
    // the stored per-channel override.
    const overview = {
      channel: {
        id: 7,
        name: "manual-channel",
        base_url: "https://api.example.com",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        model_sync_mode: "manual",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "access_token",
      checkin_enabled: false,
      has_user_credential: true,
      has_platform_user_id: true,
      has_api_key: true,
      site_usable: true,
      credential_usable: true,
      model_count: 0,
      discovered_model_count: 0,
      last_probe_at: "2026-08-02T00:00:00Z",
      last_probe_ok: true,
      last_latency_ms: 5,
      route_count: 0,
      enabled_member_count: 0,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input).split("?")[0] ?? "";
        const method = (init?.method ?? "GET").toUpperCase();
        if (path === "/admin/channels/overview" && method === "GET") {
          return jsonResponse([overview]);
        }
        if (path === "/admin/sites" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/plugins/status" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/channels" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/routes/overview" && method === "GET") {
          return jsonResponse([]);
        }
        if (path.startsWith("/admin/discovery/models")) {
          return jsonResponse([]);
        }
        return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
      }),
    );

    renderChannels();
    expect(
      await screen.findByRole("heading", { name: "Connections" }),
    ).toBeInTheDocument();

    await waitFor(async () => {
      const trigger = screen.getByRole("button", { name: /more actions/i });
      trigger.click();
      await new Promise((resolve) => setTimeout(resolve, 0));
      screen.getByRole("menuitem", { name: /^edit$/i }).click();
    });

    const dialog = await screen.findByRole("dialog");
    const manual = await within(dialog).findByRole("button", {
      name: "Pick on demand",
    });
    expect(manual).toHaveAttribute("aria-pressed", "true");
    expect(
      within(dialog).getByRole("button", { name: "Auto sync" }),
    ).toHaveAttribute("aria-pressed", "false");
  });

  it("sends model_sync_mode only when the operator changed it in the dialog", async () => {
    // Regression: the model-management drawer persists the sync mode with
    // an immediate PATCH; saving the still-open edit dialog used to stomp
    // that choice with the dialog's stale seeded value.
    const overview = {
      channel: {
        id: 7,
        name: "manual-channel",
        base_url: "https://api.example.com",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        model_sync_mode: "manual",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "access_token",
      checkin_enabled: false,
      has_user_credential: true,
      has_platform_user_id: true,
      has_api_key: true,
      site_usable: true,
      credential_usable: true,
      model_count: 0,
      discovered_model_count: 0,
      last_probe_at: "2026-08-02T00:00:00Z",
      last_probe_ok: true,
      last_latency_ms: 5,
      route_count: 0,
      enabled_member_count: 0,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    };
    const putBodies: Record<string, unknown>[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input).split("?")[0] ?? "";
        const method = (init?.method ?? "GET").toUpperCase();
        if (path === "/admin/channels/7" && method === "PUT") {
          putBodies.push(JSON.parse(String(init?.body ?? "{}")));
          // Fail the PUT so the dialog stays open for the second save.
          return jsonResponse({ error: "keep open" }, 500);
        }
        if (path === "/admin/channels/overview" && method === "GET") {
          return jsonResponse([overview]);
        }
        if (path === "/admin/sites" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/plugins/status" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/channels" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/routes/overview" && method === "GET") {
          return jsonResponse([]);
        }
        if (path.startsWith("/admin/discovery/models")) {
          return jsonResponse([]);
        }
        return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
      }),
    );

    renderChannels();
    expect(
      await screen.findByRole("heading", { name: "Connections" }),
    ).toBeInTheDocument();

    await waitFor(async () => {
      const trigger = screen.getByRole("button", { name: /more actions/i });
      trigger.click();
      await new Promise((resolve) => setTimeout(resolve, 0));
      screen.getByRole("menuitem", { name: /^edit$/i }).click();
    });

    const dialog = await screen.findByRole("dialog");

    // Save untouched: the drawer's value must survive (field omitted).
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(putBodies.length).toBe(1));
    expect(putBodies[0]).not.toHaveProperty("model_sync_mode");

    // Change the picker explicitly: now the save carries it.
    fireEvent.click(within(dialog).getByRole("button", { name: "Auto sync" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(putBodies.length).toBe(2));
    expect(putBodies[1]).toHaveProperty("model_sync_mode", "auto");
  });
});

describe("Channels edit dialog user id", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  /** Boots the connections page with one New-API channel and returns fetchers. */
  async function openEditDialog(options: {
    /** platform_user_id already stored on the user credential, if any. */
    storedUserID?: number;
    /** Whether a user credential row exists at all. */
    withUserCredential?: boolean;
    checkinEnabled?: boolean;
  }) {
    const withUserCredential = options.withUserCredential ?? true;
    const overview = {
      channel: {
        id: 7,
        name: "new-api-channel",
        site_id: 3,
        base_url: "https://api.example.com",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        model_sync_mode: "manual",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "access_token",
      checkin_enabled: options.checkinEnabled ?? false,
      has_user_credential: withUserCredential,
      has_platform_user_id: options.storedUserID != null,
      has_api_key: true,
      site_usable: true,
      credential_usable: true,
      model_count: 0,
      discovered_model_count: 0,
      last_probe_at: "2026-08-02T00:00:00Z",
      last_probe_ok: true,
      last_latency_ms: 5,
      route_count: 0,
      enabled_member_count: 0,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
    };
    const credentialBodies: Record<string, unknown>[] = [];
    const credentialPuts: { path: string; body: Record<string, unknown> }[] =
      [];
    const channelPuts: Record<string, unknown>[] = [];
    const seen: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input).split("?")[0] ?? "";
        const method = (init?.method ?? "GET").toUpperCase();
        if (path === "/admin/channels/overview" && method === "GET") {
          return jsonResponse([overview]);
        }
        if (path === "/admin/sites" && method === "GET") {
          return jsonResponse([
            {
              id: 3,
              name: "new-api-channel",
              base_url: "https://api.example.com",
              platform: "new-api",
              status: "enabled",
              created_at: "",
              updated_at: "",
            },
          ]);
        }
        if (path === "/admin/sites/3/credentials" && method === "GET") {
          return jsonResponse(
            withUserCredential
              ? [
                  {
                    id: 42,
                    site_id: 3,
                    kind: "access_token",
                    auth_mode: "access_token",
                    has_secret: true,
                    has_cookie: false,
                    status: "enabled",
                    checkin_enabled: options.checkinEnabled ?? false,
                    meta_json:
                      options.storedUserID != null
                        ? JSON.stringify({
                            platform_user_id: options.storedUserID,
                          })
                        : undefined,
                  },
                ]
              : [],
          );
        }
        if (path === "/admin/plugins/status" && method === "GET") {
          return jsonResponse([
            {
              id: "checkin",
              name: "Check-in",
              version: "1",
              kind: "addon",
              installed: true,
              enabled: true,
              can_toggle: true,
            },
          ]);
        }
        if (path === "/admin/channels" && method === "GET") {
          return jsonResponse([]);
        }
        if (path === "/admin/routes/overview" && method === "GET") {
          return jsonResponse([]);
        }
        if (path.startsWith("/admin/discovery/models")) {
          return jsonResponse([]);
        }
        if (path === "/admin/sites/3/credentials" && method === "POST") {
          credentialBodies.push(JSON.parse(String(init?.body ?? "{}")));
          return jsonResponse({ id: 43 }, 201);
        }
        if (path.startsWith("/admin/credentials/") && method === "PUT") {
          credentialPuts.push({
            path,
            body: JSON.parse(String(init?.body ?? "{}")),
          });
          return jsonResponse({ id: 42 });
        }
        if (path === "/admin/channels/7" && method === "PUT") {
          channelPuts.push(JSON.parse(String(init?.body ?? "{}")));
          return jsonResponse({ id: 7 });
        }
        seen.push(`${method} ${path}`);
        return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
      }),
    );

    renderChannels();
    expect(
      await screen.findByRole("heading", { name: "Connections" }),
    ).toBeInTheDocument();

    await waitFor(async () => {
      const trigger = screen.getByRole("button", { name: /more actions/i });
      trigger.click();
      await new Promise((resolve) => setTimeout(resolve, 0));
      screen.getByRole("menuitem", { name: /^edit$/i }).click();
    });
    return {
      dialog: await screen.findByRole("dialog"),
      credentialBodies,
      credentialPuts,
      channelPuts,
      seen,
    };
  }

  it("seeds the stored platform user id and writes a changed one to credential meta", async () => {
    // The dead end this feature closes: the row badge says "Needs user id"
    // but there was nowhere to type it.
    const { dialog, credentialPuts } = await openEditDialog({
      storedUserID: 1544,
    });

    const field = within(dialog).getByLabelText("User ID");
    expect(field).toHaveValue("1544");

    fireEvent.change(field, { target: { value: "2001" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(credentialPuts.length).toBe(1));
    expect(credentialPuts[0]?.path).toBe("/admin/credentials/42");
    expect(credentialPuts[0]?.body).toMatchObject({
      meta_json: JSON.stringify({ platform_user_id: 2001 }),
    });
  });

  it("keeps stored meta untouched when the user id was not edited", async () => {
    const { dialog, credentialPuts, channelPuts } = await openEditDialog({
      storedUserID: 1544,
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    // The channel update completes, and no credential write happened: an
    // untouched dialog must never rewrite (or drop) the stored user id.
    await waitFor(() => expect(channelPuts.length).toBe(1));
    expect(credentialPuts).toHaveLength(0);
    expect(channelPuts[0]).not.toHaveProperty("meta_json");
  });

  it("rejects a non-numeric user id instead of silently dropping it", async () => {
    const { dialog, credentialPuts } = await openEditDialog({
      storedUserID: 1544,
    });
    const field = within(dialog).getByLabelText("User ID");
    fireEvent.change(field, { target: { value: "abc" } });

    expect(
      within(dialog).getByText("User ID must be digits only."),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    expect(credentialPuts).toHaveLength(0);
  });

  it("opens the credential section when a user id is missing", async () => {
    // The row badge says "Needs user id"; the field that fixes it must not be
    // hidden behind a collapsed section.
    const { dialog } = await openEditDialog({ withUserCredential: true });
    expect(within(dialog).getByLabelText("User ID")).toBeInTheDocument();
  });

  it("tells the operator a typed id needs a user credential to be saved on", async () => {
    const { dialog } = await openEditDialog({
      withUserCredential: false,
    });

    // Nothing stored yet, so the credential fields stay collapsed (the row
    // badge for this state points at the token, not at the user id).
    fireEvent.click(
      within(dialog).getByRole("button", { name: /show advanced/i }),
    );

    const field = within(dialog).getByLabelText("User ID");
    fireEvent.change(field, { target: { value: "1544" } });

    expect(
      within(dialog).getByText(
        /No user credential is set\. Fill in the User Access Token/,
      ),
    ).toBeInTheDocument();
  });
});

describe("Channels cooldown freshness", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("clears a cooling verdict without a page switch", async () => {
    // Regression: the page carried no refetch interval of its own, so a channel
    // leaving cooldown kept rendering "Degraded" (with the "route members are
    // cooling down" tooltip) until the operator navigated away and back, which
    // remounted the query observer and forced the refetch.
    vi.useFakeTimers();
    let cooling = true;
    const overview = () => ({
      channel: {
        id: 5,
        name: "cooling-channel",
        base_url: "https://api.example.com",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        created_at: "",
        updated_at: "",
      },
      credential_kind: "api_key",
      checkin_enabled: false,
      has_user_credential: false,
      has_platform_user_id: false,
      has_api_key: true,
      site_usable: true,
      credential_usable: true,
      model_count: 1,
      discovered_model_count: 1,
      last_probe_at: "2026-08-02T00:00:00Z",
      last_probe_ok: true,
      last_latency_ms: 5,
      route_count: 1,
      enabled_member_count: 1,
      cooling_member_count: cooling ? 1 : 0,
      failure_count: 0,
      checkin_supported: true,
      account_supported: true,
      health_state: cooling ? "degraded" : "healthy",
      health_reason: cooling ? "route_cooling" : "probe_ok",
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input).split("?")[0] ?? "";
        switch (path) {
          case "/admin/channels/overview":
            return jsonResponse([overview()]);
          case "/admin/sites":
          case "/admin/channels":
          case "/admin/routes/overview":
          case "/admin/plugins/status":
            return jsonResponse([]);
          default:
            return jsonResponse({ error: `unexpected ${path}` }, 500);
        }
      }),
    );

    renderChannels();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(screen.getByTestId("channel-health-badge")).toHaveTextContent(
      "Degraded",
    );

    // The backend now reports the cooldown as over; the page has to notice on
    // its own poll rather than on the next mount.
    cooling = false;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_100);
    });
    expect(screen.getByTestId("channel-health-badge")).toHaveTextContent(
      "Healthy",
    );
  });
});

describe("isCustomChannelType", () => {
  it("treats the Custom type and hand-typed ids as custom, named providers as not", () => {
    expect(isCustomChannelType("custom")).toBe(true);
    expect(isCustomChannelType("  Custom ")).toBe(true);
    expect(isCustomChannelType("my-bespoke-relay")).toBe(true);
    expect(isCustomChannelType("openai-compatible")).toBe(false);
    expect(isCustomChannelType("typesafe")).toBe(false);
    // An unset type is not an assertion of bespoke wiring.
    expect(isCustomChannelType("")).toBe(false);
  });
});

describe("Channels edit dialog endpoint mapping visibility", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  function overviewFor(channel: Record<string, unknown>) {
    return {
      channel: {
        id: 11,
        name: "probe-channel",
        base_url: "https://api.example.com",
        models_csv: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        created_at: "",
        updated_at: "",
        ...channel,
      },
      credential_kind: "api_key",
      checkin_enabled: false,
      has_user_credential: false,
      has_platform_user_id: false,
      has_api_key: true,
      site_usable: true,
      credential_usable: true,
      model_count: 0,
      discovered_model_count: 0,
      last_probe_at: "2026-08-02T00:00:00Z",
      last_probe_ok: true,
      last_latency_ms: 5,
      route_count: 0,
      enabled_member_count: 0,
      cooling_member_count: 0,
      failure_count: 0,
      checkin_supported: false,
      account_supported: false,
    };
  }

  async function openEditAdvanced(
    channel: Record<string, unknown>,
    overviewPatch: Record<string, unknown> = {},
  ) {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input).split("?")[0] ?? "";
        switch (path) {
          case "/admin/channels/overview":
            return jsonResponse([
              { ...overviewFor(channel), ...overviewPatch },
            ]);
          case "/admin/sites":
          case "/admin/channels":
          case "/admin/routes/overview":
          case "/admin/plugins/status":
            return jsonResponse([]);
          default:
            return jsonResponse({ error: `unexpected ${path}` }, 500);
        }
      }),
    );

    renderChannels();
    await screen.findByRole("heading", { name: "Connections" });

    await waitFor(async () => {
      screen.getByRole("button", { name: /more actions/i }).click();
      await new Promise((resolve) => setTimeout(resolve, 0));
      screen.getByRole("menuitem", { name: /^edit$/i }).click();
    });

    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Show advanced" }));
    return dialog;
  }

  it("stays out of the way for an ordinary provider with nothing mapped", async () => {
    // Regression: the panel used to render for every channel, so a plain
    // OpenAI-compatible row opened onto four JSON editors.
    const dialog = await openEditAdvanced({ type_hint: "openai-compatible" });
    // Proves the advanced block did open — the absence below is the gate, not a
    // missing click.
    expect(
      within(dialog).getByText("Payload rules (body rewrite)"),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("heading", {
        name: "Custom endpoint and field mapping",
      }),
    ).toBeNull();
  });

  it("opens for a hand-wired Custom type", async () => {
    const dialog = await openEditAdvanced({ type_hint: "custom" });
    expect(
      within(dialog).getByRole("heading", {
        name: "Custom endpoint and field mapping",
      }),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText("Not configured (plain passthrough)"),
    ).toBeInTheDocument();
  });

  it("keeps a mapping that already exists on the row visible and inspectable", async () => {
    // A provider profile applied at save (TypeSafe) or an endpoint split out of
    // a pasted URL leaves a mapping on a non-custom type. Hiding it would leave
    // the operator unable to read or clear it.
    const dialog = await openEditAdvanced({
      type_hint: "typesafe",
      upstream_request_map: '[{"from":"messages.0.content","to":"state"}]',
    });
    expect(
      within(dialog).getByRole("heading", {
        name: "Custom endpoint and field mapping",
      }),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText("Configured: Request field map"),
    ).toBeInTheDocument();
  });

  it("hides the check-in block entirely for a provider with no check-in API", async () => {
    // It used to render for every channel — including site families that have no
    // check-in endpoint — where it showed a permanent "does not expose a check-in
    // API" note plus a Logs link in the middle of the form.
    const dialog = await openEditAdvanced({ type_hint: "openai-compatible" });
    // Proves the advanced block really did open, so the absence below is the
    // gate and not a missed click.
    expect(
      within(dialog).getByText("Payload rules (body rewrite)"),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("heading", { name: "Check-in" }),
    ).toBeNull();
  });

  it("offers scheduled check-in beside the credential for a supported provider", async () => {
    const dialog = await openEditAdvanced(
      { type_hint: "new-api" },
      { checkin_supported: true },
    );
    const advanced = dialog.querySelector(".advanced-fields");
    expect(advanced).not.toBeNull();
    expect(
      within(advanced as HTMLElement).getByRole("heading", { name: "Check-in" }),
    ).toBeInTheDocument();
  });
});
