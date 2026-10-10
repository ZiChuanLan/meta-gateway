import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SiteProbeAction, SiteProbeReport } from "../../api/types";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { ToastProvider } from "../../toast";
import { CATALOG_URL, SiteProbeDialog } from "./SiteProbeDialog";

const clients: QueryClient[] = [];

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function report(): SiteProbeReport {
  return {
    policy: { ratio_threshold: 0.9, min_samples: 5, low_rounds: 2, high_rounds: 2 },
    generated_at: "2026-10-04T00:00:00Z",
    sites: [
      {
        site_id: 1,
        site_name: "公益站A",
        probe_source_kind: "uptime_kuma",
        probe_source_url: "https://stat.example.com/status/ai",
        probe_source_enabled: true,
        probe_last_run_at: "2026-10-04 00:10:00",
        monitor_count: 3,
        auto_apply: false,
        policy: { ratio_threshold: 0.9, min_samples: 5, low_rounds: 2, high_rounds: 2 },
      },
    ],
    rows: [
      {
        route: "glm-5.2",
        match: "normalized",
        raw_model: "z-ai/glm-5.2",
        site_id: 1,
        site_name: "公益站A",
        group_name: "GLM",
        verdict: "low",
        low_streak: 2,
        ok_streak: 0,
        price_only: false,
        source_kind: "uptime_kuma",
        rounds: [
          {
            run_id: 2,
            ratio: 0.3,
            samples: 10,
            up_count: 3,
            observed_at: "2026-10-04 00:10:00",
          },
        ],
        members: [
          {
            member_id: 7,
            channel_id: 3,
            channel_name: "A-key",
            enabled: true,
            auto_disabled: false,
          },
        ],
      },
    ],
    name_only: [],
    unmatched: [
      {
        site_id: 1,
        site_name: "公益站A",
        raw_model: "kimi-k3",
        ratio: 1,
        samples: 10,
      },
    ],
  };
}

const disableAction: SiteProbeAction = {
  route: "glm-5.2",
  site_id: 1,
  site_name: "公益站A",
  channel_id: 3,
  channel_name: "A-key",
  kind: "disable",
  reason: "site probe: 2 rounds below 90% — 30% (3/10 样本) on 公益站A",
  members_moved: 0,
};

function mockBackend(
  options: {
    actions?: SiteProbeAction[];
    report?: SiteProbeReport;
    reportError?: boolean;
    siteProbeIntervalSeconds?: number;
    siteProbeJitterSeconds?: number;
  } = {},
) {
  // One report object for the whole test: the save endpoint below writes into
  // it, so a refetch after a switch reflects what the backend now stores — which
  // is exactly what the dialog reads back.
  const reportData = options.report ?? report();
  const apply = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body ?? "{}"));
    return json({
      actions: body.dry_run
        ? (options.actions ?? [disableAction])
        : (options.actions ?? [disableAction]),
      dry_run: body.dry_run,
    });
  });
  const collect = vi.fn(() => json({ collected: 1, failed: 0, actions: [] }));
  const catalogImport = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body ?? "{}"));
    if (!body.url) return json({ error: "missing catalog url" }, 400);
    // matched/skipped/unmatched mirror the backend: only sites we already have
    // can be matched, and entries we route nothing through are left alone.
    return json({
      matched: 2,
      skipped: 1,
      unmatched: 39,
      created: 0,
      collected: 2,
      failed: 0,
    });
  });
  const save = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body ?? "{}"));
    const stored = reportData.sites.find((site) => site.site_id === body.site_id);
    if (stored) {
      stored.probe_source_kind = body.kind;
      stored.probe_source_url = body.url;
      stored.probe_source_enabled = body.enabled;
      const config = JSON.parse(String(body.config || "{}"));
      stored.auto_apply = Boolean(config.auto_apply);
      if (config.policy) stored.policy = config.policy;
    }
    return json({
      id: body.site_id,
      name: "公益站A",
      base_url: "https://a.example",
      platform: "new-api",
      status: "enabled",
      created_at: "",
      updated_at: "",
      probe_source_kind: body.kind,
      probe_source_url: body.url,
      probe_source_config: body.config,
      probe_source_enabled: body.enabled,
    });
  });
  const detect = vi.fn(() =>
    json({
      kind: "uptime_kuma",
      url: "https://stat.example.com/status/ai",
      base: "https://stat.example.com",
      slug: "ai",
      title: "AI",
      groups: [{ id: 6, name: "GLM" }],
      monitors: [
        {
          id: "44",
          name: "z-ai/glm-5.2",
          type: "keyword",
          group_name: "GLM",
          samples: 10,
          up_count: 9,
          ratio: 0.9,
          weak_evidence: false,
        },
      ],
    }),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path === "/admin/sites") {
        return json([
          {
            id: 1,
            name: "公益站A",
            base_url: "https://a.example",
            platform: "new-api",
            status: "enabled",
            created_at: "",
            updated_at: "",
            probe_source_kind: "uptime_kuma",
            probe_source_url: "https://stat.example.com/status/ai",
            probe_source_enabled: true,
          },
        ]);
      }
      if (path === "/admin/site-probe/report")
        return options.reportError ? json({ error: "load_failed" }, 500) : json(reportData);
      if (path === "/admin/site-probe/apply") return apply(input, init);
      if (path === "/admin/site-probe/collect") return collect();
      if (path === "/admin/site-probe/detect") return detect();
      if (path === "/admin/site-probe/source") return save(input, init);
      if (path === "/admin/site-probe/catalog/import") return catalogImport(input, init);
      if (path === "/admin/runtime-settings")
        return json({
          editable: {
            site_probe_interval_seconds: options.siteProbeIntervalSeconds ?? 900,
            site_probe_jitter_seconds: options.siteProbeJitterSeconds ?? 120,
          },
        });
      return json({});
    }),
  );
  return { apply, collect, detect, save, catalogImport };
}

function renderDialog(onClose = () => {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <SiteProbeDialog onClose={onClose} />
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  clients.length = 0;
  // The admin client only exists once a token is present; without it every
  // query would short-circuit and the test would measure nothing.
  window.localStorage.setItem("meta-gateway.admin-token", "test-token");
  // Pin the locale: jsdom reports an English navigator, and the assertions
  // below are written against the console's default (zh) copy.
  window.localStorage.setItem("meta-gateway.locale", "zh-CN");
});

afterEach(() => {
  cleanup();
  window.localStorage.clear();
  vi.unstubAllGlobals();
});

describe("site probe dialog", () => {
  // Auto-apply is per site, so the switch has to reach the API as that site's
  // stored config — together with the thresholds a background round will use.
  it("formats source timestamps and prices without losing explicit zero prices", async () => {
    const data = report();
    data.sites[0]!.probe_last_run_at = "2026-10-04T00:10:00Z";
    data.rows[0]!.observed_price = {
      mode: "token",
      currency_symbol: "$",
      input_per_million: 0,
      output_per_million: 4.999999999999999,
    };
    mockBackend({ report: data });
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");
    expect(screen.getByText(/\$0 \/ \$5/)).toBeInTheDocument();
    expect(document.querySelector(".site-probe-card-run")?.textContent).not.toContain("T00:10:00Z");
  });

  it("shows report errors and does not permit blind preview", async () => {
    mockBackend({ reportError: true });
    renderDialog();
    await waitFor(() => expect(screen.getByRole("button", { name: "预览影响" })).toBeDisabled());
    await waitFor(() => expect(screen.getByText(/load_failed/)).toBeInTheDocument());
  });

  it("saves automatic sources and keeps site thresholds separate from preview", async () => {
    const data = report();
    data.sites[0]!.policy = { ...data.policy, ratio_threshold: 0.5 };
    const { save, collect } = mockBackend({ report: data });
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");
    const preview = screen.getAllByRole("spinbutton")[0];
    expect(preview).toHaveValue(90);
    fireEvent.click(screen.getByRole("button", { name: "配置来源" }));
    expect(preview).toHaveValue(90);
    expect(screen.queryByPlaceholderText("https://stat.example.com/status/ai")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "保存并启用" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    const body = JSON.parse(String(save.mock.calls[0]?.[1]?.body));
    expect(body.auto).toBe(true);
    expect(body.site_id).toBe(1);
    expect(JSON.parse(body.config).policy.ratio_threshold).toBe(0.5);
    await waitFor(() => expect(collect).toHaveBeenCalledTimes(1));
  });

  // The cadence is a runtime setting rather than a control of this dialog, so the
  // dialog states the value it is running with: an operator judging a reading has
  // to be able to tell whether the number behind it is minutes or hours old.
  it("states the collection cadence it is running with", async () => {
    mockBackend({ siteProbeIntervalSeconds: 900, siteProbeJitterSeconds: 120 });
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");
    const cadence = await screen.findByText(/自动采集：每 15 分钟一轮/);
    expect(cadence).toHaveTextContent("抖动 ±2 分钟");
    expect(cadence).toHaveTextContent("运行设置");
  });

  // Flipping the switch writes the flag immediately. A later thresholds save used
  // to echo a draft copy taken when the site was picked, so it could silently put
  // the switch back — and the consequence of the switch is now stated next to it
  // instead of behind 高级设置, where the same flag had a second control.
  it("keeps the auto-apply switch out of 高级设置 and does not revert it on save", async () => {
    const { save } = mockBackend();
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");

    const offHint = await screen.findByText(/只采集与展示/);
    expect(document.querySelector(".advanced-section")?.contains(offHint)).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: "自动应用 关" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "自动应用 开" })).toBeInTheDocument(),
    );
    expect(await screen.findByText(/已开启：每轮采集结束后/)).toBeInTheDocument();

    // Saving thresholds must carry the flag the backend now holds, not the copy
    // from before the switch was flipped.
    fireEvent.click(screen.getByRole("button", { name: "配置来源" }));
    fireEvent.click(screen.getByRole("button", { name: "保存并启用" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
    const second = JSON.parse(String(save.mock.calls[1]?.[1]?.body));
    expect(JSON.parse(second.config).auto_apply).toBe(true);
  });

  it("scopes preview and apply to the explicitly selected site", async () => {
    const { apply } = mockBackend();
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");
    fireEvent.change(screen.getByLabelText("路由操作范围"), { target: { value: "1" } });
    fireEvent.click(screen.getByRole("button", { name: "预览影响" }));
    await screen.findByText("禁用成员");
    await waitFor(() => expect(screen.getByRole("button", { name: "应用" })).toBeEnabled());
    expect(JSON.parse(String(apply.mock.calls[0]?.[1]?.body)).site_ids).toEqual([1]);
    fireEvent.change(screen.getByLabelText("路由操作范围"), { target: { value: "" } });
    expect(screen.getByRole("button", { name: "应用" })).toBeDisabled();
  });

  it("filters cards without evidence instead of matching every name against an empty query", async () => {
    const data = report();
    data.rows = [];
    mockBackend({ report: data });
    renderDialog();
    await waitFor(() => expect(document.querySelectorAll(".site-probe-rail-item")).toHaveLength(1));
    fireEvent.click(screen.getByLabelText("只看有数据的"));
    expect(document.querySelectorAll(".site-probe-rail-item")).toHaveLength(0);
  });

  it("requires re-detection after a manual URL changes", async () => {
    mockBackend();
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");
    fireEvent.click(screen.getByRole("button", { name: "配置来源" }));
    fireEvent.click(screen.getByLabelText(/自动探针/));
    fireEvent.change(screen.getByPlaceholderText("https://stat.example.com/status/ai"), {
      target: { value: "https://different.example/status/ai" },
    });
    expect(screen.getByRole("button", { name: "保存并启用" })).toBeDisabled();
  });

  it("does not offer apply when the preview contains only protected members", async () => {
    mockBackend({ actions: [{ ...disableAction, skipped: "single_member" }] });
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");
    fireEvent.click(screen.getByRole("button", { name: "预览影响" }));
    await waitFor(() =>
      expect(
        within(document.querySelector(".site-probe-changes") as HTMLElement).getByRole("table"),
      ).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "应用" })).toBeDisabled();
  });

  it("imports the catalog in one click and reads the new sites immediately", async () => {
    const { catalogImport } = mockBackend();
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");

    // One click: no preview table, no confirmation, no follow-up buttons.
    fireEvent.click(screen.getByRole("button", { name: "从监测目录导入" }));
    await waitFor(() => expect(catalogImport).toHaveBeenCalledTimes(1));
    const body = JSON.parse(String(catalogImport.mock.calls[0]?.[1]?.body));
    // The directory url is explicit (an import must never silently fall back
    // to a different directory than the operator's button names), and
    // collect_now stays unset: the backend collects by default.
    expect(body.url).toBe(CATALOG_URL);
    expect(body.collect_now).toBeUndefined();
    expect(
      await screen.findByText(/已匹配并启用 2 个站点（其中 1 个已有自定义源，跳过）/),
    ).toBeTruthy();
    // The entries we have no channel for are reported, not imported.
    expect(screen.getByText(/另有 39 个站点你还没有渠道，未导入/)).toBeTruthy();
    // The readings refresh themselves — no "collect now" to press.
    expect(await screen.findByText("z-ai/glm-5.2")).toBeTruthy();
  });

  it("labels third-party availability and prints prices in the site's own currency", async () => {
    // Two things this screen must never blur: whose number an availability is,
    // and which currency a price is in. A monitoring directory measured the
    // first, and 南梁's table is denominated in CNY — printing "$" for it would
    // put a wrong number in someone's cost model.
    const external = report();
    external.rows = [
      {
        route: "cn:glm-5.2",
        match: "namespace",
        raw_model: "glm-5.2",
        site_id: 1,
        site_name: "南梁 API",
        verdict: "low",
        low_streak: 0,
        ok_streak: 0,
        price_only: true,
        availability_source: "watchbot",
        rounds: [
          {
            run_id: 3,
            ratio: 0,
            samples: 0,
            up_count: 0,
            observed_at: "2026-10-04 00:10:00",
            price: {
              mode: "token",
              currency: "CNY",
              currency_symbol: "¥",
              input_per_million: 1.35,
              output_per_million: 4.05,
            },
          },
        ],
        external: {
          source: "watchbot",
          ratio: 0.31,
          first_token_ms: 900,
          observed_at: "2026-10-04T00:00:00Z",
        },
        // What the backend projects out of the newest round: the price cell
        // reads this, not the raw round.
        observed_price: {
          mode: "token",
          currency: "CNY",
          currency_symbol: "¥",
          input_per_million: 1.35,
          output_per_million: 4.05,
        },
        members: [],
      },
    ];
    mockBackend({ report: external });
    renderDialog();

    expect(await screen.findByText("glm-5.2")).toBeTruthy();
    expect(screen.getByText("31%")).toBeTruthy();
    expect(screen.getByText("第三方 watchbot")).toBeTruthy();
    expect(screen.getByText(/¥1\.35 \/ ¥4\.05/)).toBeTruthy();
    // No adopt button: a CNY amount cannot be written into USD billing columns.
    expect(screen.queryByRole("button", { name: /采用价格/ })).toBeNull();
  });

  it("shows our own relay traffic when the site publishes no availability", async () => {
    // A New-API price table carries prices and no health at all, so the whole
    // point is that the card still says something about availability — and
    // says WHOSE number it is.
    const priceOnly = report();
    priceOnly.rows = [
      {
        route: "cn:glm-5.2",
        match: "namespace",
        raw_model: "glm-5.2",
        site_id: 1,
        site_name: "公益站A",
        verdict: "low",
        low_streak: 0,
        ok_streak: 0,
        price_only: true,
        availability_source: "traffic",
        rounds: [
          {
            run_id: 2,
            ratio: 0,
            samples: 0,
            up_count: 0,
            observed_at: "2026-10-04 00:10:00",
            price: {
              mode: "token",
              currency: "USD",
              currency_symbol: "$",
              input_per_million: 0.15,
            },
          },
        ],
        traffic: {
          samples: 14,
          failures: 3,
          ratio: 0.7857,
          avg_first_byte_ms: 900,
          window_hours: 24,
        },
        members: [
          {
            member_id: 7,
            channel_id: 3,
            channel_name: "A-key",
            enabled: true,
            auto_disabled: false,
          },
        ],
      },
    ];
    mockBackend({ report: priceOnly });
    renderDialog();

    expect(await screen.findByText("glm-5.2")).toBeTruthy();
    expect(screen.getByText("78.6% (14)")).toBeTruthy();
    expect(screen.getByText("本站实测 24h")).toBeTruthy();
    expect(screen.getByText("去命名空间")).toBeTruthy();
    expect(screen.getByText("低可用")).toBeTruthy();
    expect(screen.queryByText("暂无样本")).toBeNull();
  });

  it("toggles a site's probe source from its row", async () => {
    const { save } = mockBackend();
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");

    fireEvent.click(screen.getAllByRole("button", { name: "已开启" })[0]!);
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    const body = JSON.parse(String(save.mock.calls[0]?.[1]?.body));
    expect(body.site_id).toBe(1);
    expect(body.enabled).toBe(false); // flipping the switch
    expect(body.auto).toBe(true);
  });

  it("reveals the URL field only when auto mode is off", async () => {
    mockBackend();
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");

    // Auto on: no URL box at all.
    expect(screen.queryByPlaceholderText("https://stat.example.com/status/ai")).toBeNull();

    // Addressed by its label, not by DOM order: the toolbar has checkboxes of
    // its own, and "the first one" is not a contract anybody promised.
    fireEvent.click(screen.getByLabelText(/自动探针/));
    expect(screen.getByPlaceholderText("https://stat.example.com/status/ai")).toBeTruthy();
  });

  it("shows what the site reports next to the member it would affect", async () => {
    mockBackend();
    renderDialog();

    expect(await screen.findByText("z-ai/glm-5.2")).toBeTruthy();
    // The reading is the site's own numbers, and the member it maps to is shown
    // so a disable is never a surprise.
    expect(screen.getByText("30% (3/10)")).toBeTruthy();
    expect(screen.getByText("低可用")).toBeTruthy();
    expect(screen.getByText("去前缀")).toBeTruthy();
    expect(screen.getByText("A-key")).toBeTruthy();
  });

  it("previews before it applies, and never calls apply without a preview", async () => {
    const { apply } = mockBackend();
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");

    const applyButton = screen.getByRole("button", { name: "应用" });
    // Applying blind is exactly what this tool must not allow.
    expect((applyButton as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "预览影响" }));
    await waitFor(() => expect(apply).toHaveBeenCalledTimes(1));
    expect(JSON.parse(String(apply.mock.calls[0]?.[1]?.body)).dry_run).toBe(true);
    expect(await screen.findByText("禁用成员")).toBeTruthy();
    expect(screen.getByText(/site probe: 2 rounds below 90%/)).toBeTruthy();
    expect((applyButton as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(applyButton);
    await waitFor(() => expect(apply).toHaveBeenCalledTimes(2));
    expect(JSON.parse(String(apply.mock.calls[1]?.[1]?.body)).dry_run).toBe(false);
    expect(await screen.findByText(/已执行 1 项变更/)).toBeTruthy();
  });

  it("identifies a pasted source URL before it is saved", async () => {
    const { detect } = mockBackend();
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");

    // The URL field lives behind the auto switch; turning auto off reveals it.
    fireEvent.click(screen.getByLabelText(/自动探针/));
    fireEvent.change(screen.getByPlaceholderText("https://stat.example.com/status/ai"), {
      target: { value: "https://stat.example.com/status/ai" },
    });
    fireEvent.click(screen.getByRole("button", { name: "识别来源" }));
    await waitFor(() => expect(detect).toHaveBeenCalled());
    expect(await screen.findByText(/Uptime Kuma 状态页「AI」/)).toBeTruthy();
  });
});

// The defect these pin is the one that made this screen unreadable in production:
// a row's title was the ROUTE name, so every site publishing a model that matched
// that route rendered under the same heading. On 2026-10-07 that was 15 rows
// across 8 sites all reading "STRRX", with the site's own model name only in a
// tooltip — which is what an operator reports as "my edit changed every channel".
describe("site probe row identity", () => {
  it("titles a row with the model the site publishes and shows the route as a chip", async () => {
    mockBackend();
    renderDialog();
    const title = await screen.findByText("z-ai/glm-5.2");
    const cell = title.closest(".site-probe-model-name");
    expect(cell?.textContent).toContain("z-ai/glm-5.2");
    // The route follows as a chip: the connection stays visible without
    // pretending the two names are the same thing.
    expect(cell?.textContent).toContain("glm-5.2");
    expect(cell?.textContent).toContain("→");
  });

  // A name-only match is a candidate, not a reading: the route has no member on
  // this site, so there is nothing to judge, disable or price here.
  it("reports a name-only match as a candidate instead of a reading", async () => {
    const data = report();
    data.name_only = [
      {
        site_id: 1,
        site_name: "公益站A",
        raw_model: "kimi-k3",
        route: "STRRX",
        match: "namespace",
        ratio: 0.3,
        samples: 10,
      },
    ];
    mockBackend({ report: data });
    renderDialog();
    await screen.findByText("z-ai/glm-5.2");
    // The rail counts it separately from the attached rows…
    expect(screen.getByText(/1 已接入 · 1 当前站点模型数据/)).toBeInTheDocument();
    // …and the detail names the collision rather than hiding it.
    fireEvent.click(screen.getByText("当前站点模型数据（1）"));
    const list = within(document.querySelector(".site-probe-candidate-list") as HTMLElement);
    expect(list.getByText("kimi-k3")).toBeInTheDocument();
    expect(list.getByText(/→ STRRX/)).toBeInTheDocument();
  });
});
