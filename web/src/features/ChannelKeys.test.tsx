import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { ToastProvider } from "../toast";
import { ChannelKeysDrawer } from "./ChannelKeys";

const CHANNEL = {
  id: 5,
  site_id: 2,
  credential_id: 143,
  name: "随时跑路公益站",
  base_url: "https://up.example",
  models_csv: "",
  group_name: "default",
  priority: 0,
  weight: 100,
  status: "enabled",
  model_sync_mode: "auto",
  type_hint: "gemini",
};

// The channel listed three models; the gemini key only synced two of them.
const CHANNEL_MODELS = ["gemini-2.5-pro", "gemini-3.1-flash-lite", "deepseek-v4-flash"].map(
  (model_name, index) => ({
    id: index + 1,
    channel_id: 5,
    model_name,
    available: true,
    source: "probe",
    latency_ms: 0,
    checked_at: "2026-09-21T00:00:00Z",
  }),
);

const GEMINI_KEY = {
  id: 143,
  site_id: 2,
  kind: "api_key",
  has_secret: true,
  status: "enabled",
  checkin_enabled: false,
  models_csv: "",
  model_count: 2,
  models: ["gemini-2.5-pro", "gemini-3.1-flash-lite"],
  priority: 10,
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function stubEndpoints(keys: unknown[]) {
  const puts: { path: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const method = (init?.method ?? "GET").toUpperCase();
      if (method === "PUT") {
        puts.push({
          path,
          body: init?.body ? JSON.parse(String(init.body)) : null,
        });
        return jsonResponse({ ok: true });
      }
      if (path.includes("/admin/discovery/models")) return jsonResponse(CHANNEL_MODELS);
      if (path.includes("/credentials")) return jsonResponse(keys);
      return jsonResponse({ error: "unexpected" }, 500);
    }),
  );
  return puts;
}

/** Records the tier the drawer reports. The drawer only emits the intent --
 *  Channels.tsx owns the credential PUT -- so the component test pins the
 *  callback contract and the wire body is covered in api/client.test.ts. */
function renderDrawer(
  keys: unknown[] = [GEMINI_KEY],
  onAddApiKey: (secret: string, name?: string) => void = () => {},
) {
  const puts = stubEndpoints(keys);
  const priorityWrites: { id: number; priority: number }[] = [];
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <ChannelKeysDrawer
              channel={CHANNEL as never}
              apiKeys={keys as never}
              pending={false}
              onToggleKey={() => {}}
              onUpdateKeyModels={() => {}}
              onUpdateKeyPriority={(id, priority) => {
                priorityWrites.push({ id, priority });
              }}
              onDeleteKey={() => {}}
              onAddApiKey={onAddApiKey}
              onSyncKeys={() => {}}
              onClose={() => {}}
            />
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { puts, priorityWrites };
}

/** Candidate model names currently offered by the (single) open picker. */
function candidateModels(): string[] {
  return Array.from(document.querySelectorAll(".model-picker-name")).map(
    (row) => row.textContent?.trim() ?? "",
  );
}

async function expandAllowlist() {
  const toggle = await screen.findByRole("button", {
    name: "展开模型白名单",
  });
  fireEvent.click(toggle);
  await waitFor(() => expect(candidateModels().length).toBeGreaterThan(0));
}

describe("ChannelKeysDrawer model allowlist", () => {
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

  it("offers the key's own synced models, not the channel-wide union", async () => {
    renderDrawer();
    await expandAllowlist();
    const candidates = candidateModels();
    expect(candidates).toContain("gemini-2.5-pro");
    expect(candidates).toContain("gemini-3.1-flash-lite");
    expect(candidates).not.toContain("deepseek-v4-flash");
  });

  it("falls back to the channel list and says so while a key has no snapshot", async () => {
    renderDrawer([{ ...GEMINI_KEY, model_count: -1, models: undefined }]);
    await expandAllowlist();
    expect(candidateModels()).toContain("deepseek-v4-flash");
    expect(
      screen.getByText(
        "该 Key 还没有同步快照，先列出渠道上的全部模型；同步一次后这里只列它自己的模型。",
      ),
    ).toBeTruthy();
  });

  it("warns about selections the key never synced", async () => {
    renderDrawer([{ ...GEMINI_KEY, models_csv: "deepseek-v4-flash" }]);
    await expandAllowlist();
    expect(
      screen.getByText("有 1 个已选模型不在该 Key 的同步列表里，上游很可能直接 404。"),
    ).toBeTruthy();
  });

  it("shows the pool tier and reports a changed one upward", async () => {
    const { priorityWrites } = renderDrawer();
    const balanced = await screen.findByRole("button", { name: "均衡" });
    expect(balanced.getAttribute("aria-pressed")).toBe("false");
    expect((await screen.findByRole("button", { name: "优先" })).getAttribute("aria-pressed")).toBe(
      "true",
    );

    fireEvent.click(balanced);
    // The drawer reports the tier on its own; it must not reach for the
    // allowlist or the status while doing so.
    await waitFor(() => expect(priorityWrites).toEqual([{ id: 143, priority: 0 }]));
  });

  it("leaves the tier alone when the active segment is clicked again", async () => {
    const { priorityWrites } = renderDrawer();
    fireEvent.click(await screen.findByRole("button", { name: "优先" }));
    // Nothing to change: the drawer stays quiet instead of echoing a no-op.
    expect(priorityWrites).toEqual([]);
  });

  // A key panel is pasted from, not typed into: three keys with one repeat must
  // become two submissions, and the drawer has to say so instead of quietly
  // keeping the first line.
  it("adds every key of a multi-line paste exactly once", async () => {
    const added: string[] = [];
    renderDrawer([GEMINI_KEY], (secret) => added.push(secret));
    const input = await screen.findByPlaceholderText("可选，填写用于同步模型的 API Key");
    fireEvent.change(input, { target: { value: "sk-a\nsk-b\nsk-a" } });
    expect(await screen.findByText(/待添加 2 个密钥/)).toBeTruthy();
    expect(await screen.findByText(/已去掉 1 个重复/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "添加" }));
    await waitFor(() => expect(added).toEqual(["sk-a", "sk-b"]));
    expect(await screen.findByText(/本次已提交 2 个/)).toBeTruthy();
  });

  it("notes the type's key shape but never blocks a submit", async () => {
    const added: string[] = [];
    renderDrawer([{ ...GEMINI_KEY }], (secret) => added.push(secret));
    const input = await screen.findByPlaceholderText("可选，填写用于同步模型的 API Key");
    expect(await screen.findByText(/该类型的密钥格式：AIza…/)).toBeTruthy();
    fireEvent.change(input, { target: { value: "unrelated-token" } });
    expect(await screen.findByText(/看起来不像 gemini 的密钥/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "添加" }));
    await waitFor(() => expect(added).toEqual(["unrelated-token"]));
  });
});

// —— 批量测活 / 批量删除 ——
// The operator's loop: test every key, look at which ones are dead, delete
// exactly those. The three steps share one selection, so the middle one has to
// keep its promise about the first, and the last one must hit the server only
// for the key it claims to delete.
const DEAD_KEY = { ...GEMINI_KEY, id: 144, models_csv: "", models: [], model_count: -1 };

const TEST_REPORT = {
  tested: 2,
  ok: 1,
  failed: 1,
  results: [
    {
      credential_id: 143,
      ok: true,
      model_count: 12,
      sample: ["gemini-2.5-pro", "gemini-3.1-flash-lite"],
      latency_ms: 348,
    },
    {
      credential_id: 144,
      ok: false,
      category: "upstream_unauthorized",
      error: "model discovery failed: upstream_status (401)",
      model_count: 0,
      latency_ms: 121,
    },
  ],
};

describe("ChannelKeysDrawer batch keys", () => {
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

  function renderBatchDrawer() {
    const deletes: string[] = [];
    const testBodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        const method = (init?.method ?? "GET").toUpperCase();
        if (method === "DELETE") {
          deletes.push(path);
          return jsonResponse({ status: "deleted" });
        }
        if (method === "POST" && path.includes("/keys/test")) {
          testBodies.push(init?.body ? JSON.parse(String(init.body)) : null);
          return jsonResponse(TEST_REPORT);
        }
        if (path.includes("/admin/discovery/models")) return jsonResponse(CHANNEL_MODELS);
        if (path.includes("/credentials")) return jsonResponse([GEMINI_KEY, DEAD_KEY]);
        return jsonResponse({ error: "unexpected" }, 500);
      }),
    );
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    render(
      <QueryClientProvider client={queryClient}>
        <I18nProvider>
          <ToastProvider>
            <SessionProvider>
              <ChannelKeysDrawer
                channel={CHANNEL as never}
                apiKeys={[GEMINI_KEY, DEAD_KEY] as never}
                pending={false}
                onToggleKey={() => {}}
                onUpdateKeyModels={() => {}}
                onUpdateKeyPriority={() => {}}
                onDeleteKey={() => {}}
                onAddApiKey={() => {}}
                onSyncKeys={() => {}}
                onClose={() => {}}
              />
            </SessionProvider>
          </ToastProvider>
        </I18nProvider>
      </QueryClientProvider>,
    );
    return { deletes, testBodies };
  }

  it("tests every key and reports each one separately", async () => {
    const { testBodies } = renderBatchDrawer();
    expect(await screen.findByText("已选 0 个")).toBeTruthy();

    // Nothing selected: the button means "every key in this list".
    fireEvent.click(screen.getByRole("button", { name: "测活全部" }));
    await waitFor(() => expect(testBodies).toEqual([{ credential_ids: [143, 144] }]));

    expect(await screen.findByText(/正常 · 12 个模型 · 348 ms/)).toBeTruthy();
    expect(await screen.findByText(/密钥不可用：API Key 失效（401\/403）/)).toBeTruthy();
    expect(await screen.findByText(/测活完成：1 个正常 · 1 个失效（共 2 个）/)).toBeTruthy();
  });

  it("selects the dead keys and deletes exactly those", async () => {
    const { deletes } = renderBatchDrawer();
    fireEvent.click(await screen.findByRole("button", { name: "测活全部" }));

    fireEvent.click(await screen.findByRole("button", { name: "选中 1 个失效的" }));
    expect(await screen.findByText("已选 1 个")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "删除选中（1）" }));
    expect(await screen.findByText("将永久删除 1 个 API 密钥，且无法恢复。")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "删除" }));

    await waitFor(() => expect(deletes).toHaveLength(1));
    expect(deletes[0]).toContain("/admin/credentials/144");
    expect(await screen.findByText("已删除 1 个")).toBeTruthy();
  });
});
