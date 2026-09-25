import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { ToastProvider } from "../../toast";
import { CheckinsPanel } from "./CheckinsPanel";

const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** Only the fields the panel reads; the rest of the document is irrelevant here. */
const settings = (overrides: Record<string, unknown>) => ({
  source: "environment",
  has_override: false,
  note: "",
  editable: {
    retry_times: 2,
    cross_channel_failover_enabled: true,
    cooldown_seconds: 30,
    fault_protection_enabled: true,
    checkin_enabled: false,
    checkin_cron: "0 8 * * *",
  },
  env_bootstrap: {},
  server_http_addr: ":4100",
  data_dir: "/data",
  backup_dir: "/data/backups",
  plugins_dir: "/data/plugins",
  metrics_token_masked: "",
  ...overrides,
});

function setup(payload: Record<string, unknown>) {
  const calls: { path: string; body: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path === "/admin/runtime-settings") {
        if (init?.method === "PUT") {
          const body = JSON.parse(String(init.body)) as Record<string, unknown>;
          calls.push({ path, body });
          return response(settings({ source: "admin_override", has_override: true, editable: { ...payload.editable as object, ...body } }));
        }
        return response(payload);
      }
      if (path === "/admin/checkin/logs") return response([]);
      if (path === "/admin/sites") return response([]);
      return response({ error: `unexpected ${path}` }, 500);
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <CheckinsPanel />
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { calls };
}

describe("scheduled check-in panel", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "zh-CN");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  // The schedule is only durable when the console owns it: an environment-fed
  // schedule reverts to the compose default the moment the container is
  // recreated, which reads as "the update deleted my setting".
  it("says when the schedule still comes from the environment", async () => {
    setup(settings({}));
    expect(await screen.findByText(/定时设置来源：环境变量/)).toBeInTheDocument();
    expect(screen.getByText(/当前跟随环境变量/)).toBeInTheDocument();
  });

  it("drops the environment warning once the console saved an override", async () => {
    setup(
      settings({
        source: "admin_override",
        has_override: true,
        editable: {
          retry_times: 2,
          cross_channel_failover_enabled: true,
          cooldown_seconds: 30,
          fault_protection_enabled: true,
          checkin_enabled: true,
          checkin_cron: "0 9 * * *",
        },
      }),
    );
    expect(await screen.findByText("定时设置来源：管理端覆盖")).toBeInTheDocument();
    expect(screen.queryByText(/当前跟随环境变量/)).not.toBeInTheDocument();
  });

  it("saves the schedule as a console override", async () => {
    const { calls } = setup(settings({}));
    // Wait for the settings document: the checkbox stays disabled until it arrives.
    await screen.findByText(/定时设置来源：环境变量/);
    // Turning the schedule on is what pins it: the save carries the flag and
    // the cron in one document, so a rebuild keeps running at that time.
    fireEvent.click(screen.getByLabelText("启用定时签到"));
    fireEvent.click(screen.getByRole("button", { name: "保存计划" }));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]?.body).toMatchObject({ checkin_enabled: true, checkin_cron: "0 8 * * *" });
  });
});
