import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  Activity,
  ArrowLeft,
  ArrowRight,
  Boxes,
  Check,
  CheckCircle2,
  Coins,
  Copy,
  Cpu,
  Database,
  HeartPulse,
  KeyRound,
  Plug,
  ScrollText,
  TrendingUp,
  Wallet,
  Zap,
} from "lucide-react";
import { api } from "../api/client";
import type { ProxyLog, UsageRecord } from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { StatGrid } from "../components/StatGrid";
import { HourlyTrafficChart } from "../components/charts";
import { Button, Page, Panel } from "../components/ui";
import { formatCost, formatTokens } from "../lib/format";
import { channelHealthState } from "./channelHealth";

const HOUR_24 = 24 * 3600 * 1000;

/** Chart window options: hourly buckets over the last N hours. */
const WINDOWS = [
  { hours: 24, labelKey: "dashboard.window24h" },
  { hours: 48, labelKey: "dashboard.window48h" },
] as const;

function relativeTime(
  iso: string,
  t: (key: string, vars?: Record<string, string | number>) => string,
) {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 60_000) return t("dashboard.justNow");
  if (ms < 3600_000)
    return t("dashboard.minutesAgo", { n: Math.floor(ms / 60_000) });
  if (ms < HOUR_24)
    return t("dashboard.hoursAgo", { n: Math.floor(ms / 3600_000) });
  return t("dashboard.daysAgo", { n: Math.floor(ms / HOUR_24) });
}

/** HTTP status → semantic tone for log badges. */
function statusTone(status: number): "ok" | "warn" | "danger" | "neutral" {
  if (status >= 200 && status < 300) return "ok";
  if (status >= 400 && status < 500) return "warn";
  if (status >= 500) return "danger";
  return "neutral";
}

function EndpointStrip() {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const ready = useQuery({
    queryKey: ["ready"],
    queryFn: async () => {
      const response = await fetch("/readyz");
      return response.ok;
    },
    refetchInterval: 30_000,
  });
  const endpoint =
    typeof window !== "undefined"
      ? `${window.location.origin}/v1/chat/completions`
      : "/v1/chat/completions";
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(endpoint);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      // Clipboard unavailable (e.g. insecure context); leave as-is.
    }
  };
  return (
    <div className="endpoint-strip">
      <span
        className={`endpoint-dot${ready.data === true ? " is-healthy" : ""}`}
      />
      <div className="endpoint-copy">
        <strong>{t("dashboard.endpoint")}</strong>
        <code>{endpoint}</code>
      </div>
      <Button
        variant="secondary"
        icon={copied ? <Check size={14} /> : <Copy size={14} />}
        onClick={copy}
      >
        {copied ? t("dashboard.copied") : t("dashboard.copy")}
      </Button>
    </div>
  );
}

function ResultDistribution({
  ok,
  clientError,
  serverError,
  other,
}: {
  ok: number;
  clientError: number;
  serverError: number;
  other: number;
}) {
  const { t } = useI18n();
  const total = ok + clientError + serverError + other;
  if (total === 0) return null;
  const pct = (n: number) => `${(n / total) * 100}%`;
  const segments = [
    {
      key: "ok",
      n: ok,
      cls: "rd-ok",
      label: t("dashboard.resultOk"),
    },
    {
      key: "client",
      n: clientError,
      cls: "rd-warn",
      label: t("dashboard.resultClientError"),
    },
    {
      key: "server",
      n: serverError,
      cls: "rd-danger",
      label: t("dashboard.resultServerError"),
    },
    {
      key: "other",
      n: other,
      cls: "rd-neutral",
      label: t("dashboard.resultOther"),
    },
  ].filter((s) => s.n > 0);
  return (
    <div className="result-distribution">
      <div className="result-distribution-bar">
        {segments.map((s) => (
          <span key={s.key} className={s.cls} style={{ width: pct(s.n) }} />
        ))}
      </div>
      <div className="result-distribution-legend">
        {segments.map((s) => (
          <span key={s.key}>
            <i className={s.cls} />
            {s.label} {s.n}
          </span>
        ))}
      </div>
    </div>
  );
}


export function Dashboard() {
  const { client } = useSession();
  const s = api(client!);
  const { t } = useI18n();

  const summary = useQuery({
    queryKey: ["usage-summary"],
    queryFn: ({ signal }) => s.usageSummary(undefined, signal),
    refetchInterval: 30_000,
  });
  const usage = useQuery({
    queryKey: ["usage-latest"],
    queryFn: ({ signal }) => s.usageRecords({ limit: 500 }, signal),
    refetchInterval: 30_000,
  });
  const channels = useQuery({
    queryKey: ["channel-overviews"],
    queryFn: ({ signal }) => s.channelOverviews(signal),
    refetchInterval: 30_000,
  });
  const logs = useQuery({
    queryKey: ["proxy-logs", { limit: 5 }],
    queryFn: ({ signal }) => s.proxyLogs({ limit: 5 }, signal),
    refetchInterval: 15_000,
  });

  const [windowHours, setWindowHours] = useState<24 | 48>(24);
  const [selectedHour, setSelectedHour] = useState<number | null>(null);
  const now = Date.now();
  const recent = useMemo(() => {
    const cutoff = now - HOUR_24;
    return (usage.data ?? []).filter(
      (row) => new Date(row.created_at).getTime() >= cutoff,
    );
  }, [usage.data, now]);

  const channelCounts = useMemo(() => {
    const all = channels.data ?? [];
    const enabled = all.filter((c) => c.channel.status === "enabled").length;
    const healthy = all.filter(
      (c) => channelHealthState(c) === "healthy",
    ).length;
    return { total: all.length, enabled, healthy };
  }, [channels.data]);

  /** Wall-clock hourly buckets (oldest → newest) for the overview chart. */
  const hourly = useMemo(() => {
    const n = windowHours;
    const currentHour = new Date(now);
    currentHour.setMinutes(0, 0, 0);
    const firstStart = currentHour.getTime() - (n - 1) * 3600_000;
    const starts = Array.from(
      { length: n },
      (_, i) => firstStart + i * 3600_000,
    );
    const buckets = Array.from({ length: n }, () => ({
      req: 0,
      tok: 0,
      cacheRead: 0,
      cacheWrite: 0,
    }));
    for (const row of usage.data ?? []) {
      const index = Math.floor(
        (new Date(row.created_at).getTime() - firstStart) / 3600_000,
      );
      const bucket = buckets[index];
      if (!bucket) continue;
      bucket.req += 1;
      bucket.tok += row.total_tokens ?? 0;
      bucket.cacheRead += row.cache_read_tokens ?? 0;
      bucket.cacheWrite += row.cache_creation_tokens ?? 0;
    }
    const labels = starts.map((start) => {
      const d = new Date(start);
      const hh = `${String(d.getHours()).padStart(2, "0")}:00`;
      return n > 24 ? `${d.getMonth() + 1}/${d.getDate()} ${hh}` : hh;
    });
    return {
      requests: buckets.map((b) => b.req),
      tokens: buckets.map((b) => b.tok),
      cacheReads: buckets.map((b) => b.cacheRead),
      cacheWrites: buckets.map((b) => b.cacheWrite),
      labels,
      starts,
    };
  }, [usage.data, now, windowHours]);

  /** Ten-minute buckets inside the selected hour. */
  const detail = useMemo(() => {
    if (selectedHour == null) return null;
    const start = hourly.starts[selectedHour];
    if (start == null) return null;
    const interval = 10 * 60_000;
    const buckets = Array.from({ length: 6 }, () => ({
      req: 0,
      tok: 0,
      cacheRead: 0,
      cacheWrite: 0,
    }));
    for (const row of usage.data ?? []) {
      const index = Math.floor(
        (new Date(row.created_at).getTime() - start) / interval,
      );
      const bucket = buckets[index];
      if (!bucket) continue;
      bucket.req += 1;
      bucket.tok += row.total_tokens ?? 0;
      bucket.cacheRead += row.cache_read_tokens ?? 0;
      bucket.cacheWrite += row.cache_creation_tokens ?? 0;
    }
    const labels = buckets.map((_, i) => {
      const d = new Date(start + i * interval);
      return `${String(d.getHours()).padStart(2, "0")}:${String(
        d.getMinutes(),
      ).padStart(2, "0")}`;
    });
    return {
      requests: buckets.map((b) => b.req),
      tokens: buckets.map((b) => b.tok),
      cacheReads: buckets.map((b) => b.cacheRead),
      cacheWrites: buckets.map((b) => b.cacheWrite),
      labels,
      start,
    };
  }, [hourly.starts, selectedHour, usage.data]);

  /** Requests in the previous 24h window, for the trend badge. */
  const prev24 = useMemo(() => {
    const from = now - HOUR_24 * 2;
    const to = now - HOUR_24;
    return (usage.data ?? []).filter((row) => {
      const ms = new Date(row.created_at).getTime();
      return ms >= from && ms < to;
    }).length;
  }, [usage.data, now]);

  const recentTokens = recent.reduce(
    (sum, row) => sum + (row.total_tokens ?? 0),
    0,
  );
  const recentRequests = recent.length;
  const recentCost = summary.data?.estimated_cost ?? 0;
  const cacheRead24h = recent.reduce(
    (sum, row) => sum + (row.cache_read_tokens ?? 0),
    0,
  );
  const requestTrend =
    recentRequests > 0 && prev24 > 0 ? recentRequests / prev24 - 1 : null;
  const healthyRatio =
    channelCounts.total > 0 ? channelCounts.healthy / channelCounts.total : 1;
  const healthTone =
    channelCounts.total === 0
      ? "warning"
      : healthyRatio >= 1
        ? "success"
        : healthyRatio >= 0.5
          ? "warning"
          : "danger";

  // Status-code buckets over the visible chart window or selected hour.
  const windowRows = useMemo(() => {
    const cutoff = now - windowHours * 3600_000;
    return (usage.data ?? []).filter(
      (row) => new Date(row.created_at).getTime() >= cutoff,
    );
  }, [usage.data, now, windowHours]);
  const chartRows = useMemo(() => {
    if (detail == null) return windowRows;
    return (usage.data ?? []).filter((row) => {
      const ms = new Date(row.created_at).getTime();
      return ms >= detail.start && ms < detail.start + 3600_000;
    });
  }, [detail, usage.data, windowRows]);
  const breakdown = useMemo(() => {
    const buckets = { ok: 0, clientError: 0, serverError: 0, other: 0 };
    for (const row of chartRows) {
      const s = row.status;
      if (s >= 200 && s < 300) buckets.ok += 1;
      else if (s >= 400 && s < 500) buckets.clientError += 1;
      else if (s >= 500) buckets.serverError += 1;
      else buckets.other += 1;
    }
    return buckets;
  }, [chartRows]);
  const successRate =
    recentRequests > 0
      ? recent.filter((row) => row.status >= 200 && row.status < 300).length /
        recentRequests
      : null;
  const successTone =
    successRate === null
      ? "primary"
      : successRate >= 0.99
        ? "success"
        : successRate >= 0.9
          ? "warning"
          : "danger";

  // Model usage ranking over the visible 24h window.
  const topModels = useMemo(() => {
    const map = new Map<string, { requests: number; tokens: number }>();
    for (const row of recent) {
      const entry = map.get(row.model) ?? { requests: 0, tokens: 0 };
      entry.requests += 1;
      entry.tokens += row.total_tokens ?? 0;
      map.set(row.model, entry);
    }
    return [...map.entries()]
      .sort(
        (a, b) =>
          b[1].tokens - a[1].tokens || b[1].requests - a[1].requests,
      )
      .slice(0, 6)
      .map(([model, stats]) => ({ model, ...stats }));
  }, [recent]);
  const maxModelRequests = Math.max(1, ...topModels.map((m) => m.requests));

  const recentLogs = (logs.data ?? []).slice(0, 5);

  return (
    <Page
      kicker={t("dashboard.kicker")}
      title={t("dashboard.title")}
      description={t("dashboard.description")}
    >
      <div className="stack">
        <EndpointStrip />
        {!channels.isPending && channelCounts.total === 0 && (
          <section className="quickstart">
            <div className="quickstart-head">
              <strong>{t("dashboard.quickstart.title")}</strong>
              <span className="muted">{t("dashboard.quickstart.body")}</span>
            </div>
            <ol className="quickstart-steps">
              <li>
                <span className="quickstart-step-icon">
                  <Plug size={16} />
                </span>
                <div>
                  <strong>{t("dashboard.quickstart.step1")}</strong>
                  <span className="muted">
                    {t("dashboard.quickstart.step1Desc")}
                  </span>
                </div>
                <Link className="button button-secondary" to="/channels">
                  {t("dashboard.quickstart.action1")}
                  <ArrowRight size={14} />
                </Link>
              </li>
              <li>
                <span className="quickstart-step-icon">
                  <Boxes size={16} />
                </span>
                <div>
                  <strong>{t("dashboard.quickstart.step2")}</strong>
                  <span className="muted">
                    {t("dashboard.quickstart.step2Desc")}
                  </span>
                </div>
                <Link className="button button-secondary" to="/models">
                  {t("dashboard.quickstart.action2")}
                  <ArrowRight size={14} />
                </Link>
              </li>
              <li>
                <span className="quickstart-step-icon">
                  <KeyRound size={16} />
                </span>
                <div>
                  <strong>{t("dashboard.quickstart.step3")}</strong>
                  <span className="muted">
                    {t("dashboard.quickstart.step3Desc")}
                  </span>
                </div>
                <Link className="button button-secondary" to="/keys">
                  {t("dashboard.quickstart.action3")}
                  <ArrowRight size={14} />
                </Link>
              </li>
            </ol>
          </section>
        )}
        <StatGrid
          columns={7}
          items={[
            {
              label: t("dashboard.totalRequests"),
              value: summary.data?.request_count ?? "—",
              hint: t("dashboard.totalRequestsHint"),
              icon: <Activity size={14} />,
              tone: "primary",
            },
            {
              label: t("dashboard.totalTokens"),
              value: summary.data
                ? formatTokens(summary.data.total_tokens)
                : "—",
              hint: t("dashboard.totalTokensHint"),
              icon: <Coins size={14} />,
              tone: "info",
            },
            {
              label: t("dashboard.recentRequests"),
              value: summary.isPending ? "—" : recentRequests,
              hint: t("dashboard.recentRequestsHint"),
              icon: <ScrollText size={14} />,
              tone: "success",
              trend: requestTrend,
            },
            {
              label: t("dashboard.healthyChannels"),
              value: channels.isPending
                ? "—"
                : `${channelCounts.healthy}/${channelCounts.total}`,
              hint: t("dashboard.healthyChannelsHint"),
              icon: <HeartPulse size={14} />,
              tone: healthTone,
            },
            {
              label: t("dashboard.cost24h"),
              value: summary.isPending ? "—" : formatCost(recentCost),
              hint: t("dashboard.cost24hHint"),
              icon: <Wallet size={14} />,
              tone: "warning",
            },
            {
              label: t("dashboard.successRate"),
              value:
                summary.isPending || successRate === null
                  ? "—"
                  : `${Math.round(successRate * 100)}%`,
              hint: t("dashboard.successRateHint"),
              icon: <CheckCircle2 size={14} />,
              tone: successTone,
            },
            {
              label: t("dashboard.cacheRead"),
              value: summary.isPending ? "—" : formatTokens(cacheRead24h),
              hint: t("dashboard.cacheReadHint"),
              icon: <Database size={14} />,
              tone: "info",
            },
          ]}
        />

        <Panel className="dashboard-panel dashboard-chart-panel">
          <div className="panel-header dashboard-chart-header">
            <div className="dashboard-chart-title">
              {detail ? (
                <button
                  type="button"
                  className="chart-back-button"
                  onClick={() => setSelectedHour(null)}
                  aria-label={t("dashboard.chartBack")}
                >
                  <ArrowLeft size={14} />
                </button>
              ) : (
                <Activity size={15} />
              )}
              <strong>
                {detail
                  ? t("dashboard.hourlyDetail", {
                      label: hourly.labels[selectedHour ?? 0] ?? "",
                    })
                  : t("dashboard.hourlyTraffic")}
              </strong>
              {detail ? (
                <span className="chart-detail-pill">
                  {t("dashboard.chartDetailGranularity")}
                </span>
              ) : null}
            </div>
            <span className="panel-muted">
              {detail
                ? t("dashboard.chartDetailSummary", {
                    n: detail.requests.reduce((sum, value) => sum + value, 0),
                  })
                : t("dashboard.tokens24h", { n: formatTokens(recentTokens) })}
            </span>
            <div
              className="chart-window-tabs"
              role="tablist"
              aria-label={t("dashboard.chartWindow")}
            >
              {WINDOWS.map((w) => (
                <button
                  key={w.hours}
                  type="button"
                  role="tab"
                  aria-selected={windowHours === w.hours}
                  className={windowHours === w.hours ? "is-active" : ""}
                  onClick={() => {
                    setWindowHours(w.hours);
                    setSelectedHour(null);
                  }}
                >
                  {t(w.labelKey)}
                </button>
              ))}
            </div>
          </div>
          <HourlyTrafficChart
            key={detail ? `detail-${selectedHour}` : "overview"}
            requests={detail?.requests ?? hourly.requests}
            tokens={detail?.tokens ?? hourly.tokens}
            labels={detail?.labels ?? hourly.labels}
            height={detail ? 218 : 168}
            labelStep={detail ? 1 : windowHours > 24 ? 8 : 4}
            zoomed={detail != null}
            onSelect={detail ? undefined : setSelectedHour}
          />
          <ResultDistribution
            ok={breakdown.ok}
            clientError={breakdown.clientError}
            serverError={breakdown.serverError}
            other={breakdown.other}
          />
        </Panel>

        <div className="dashboard-grid dashboard-overview-grid">
          <Panel className="dashboard-panel dashboard-health">
            <div className="panel-header">
              <Boxes size={15} />
              <strong>{t("dashboard.channelHealth")}</strong>
              <span className="panel-muted">
                {t("dashboard.enabledOf", {
                  n: channelCounts.enabled,
                  total: channelCounts.total,
                })}
              </span>
            </div>
          <ul className="dashboard-list">
            {(channels.data ?? []).map((c) => {
                const health = channelHealthState(c);
                const tone =
                  health === "healthy"
                    ? "ok"
                    : health === "unhealthy"
                      ? "danger"
                      : health === "disabled"
                        ? "off"
                        : "warn";
                return (
                  <li key={c.channel.id}>
                    <span className={`dot dot-${tone}`} />
                    <Link
                      className="dashboard-model"
                      to={`/channels?id=${c.channel.id}`}
                    >
                      {c.channel.name}
                    </Link>
                    <span className="dashboard-meta">
                      {health === "healthy" ? (
                        <span className="badge badge-ok">
                          <Zap size={11} /> {t("dashboard.ready")}
                        </span>
                      ) : health === "disabled" ? (
                        <span className="badge badge-neutral">
                          {t("dashboard.disabled")}
                        </span>
                      ) : (
                        <span
                          className={`badge badge-${health === "unhealthy" ? "danger" : "warn"}`}
                        >
                          <AlertTriangle size={11} />
                          {t(`channels.healthState.${health}`)}
                        </span>
                      )}
                    </span>
                  </li>
                );
              })}
            </ul>
          </Panel>

          <Panel className="dashboard-panel dashboard-recent-logs">
            <div className="panel-header">
              <ScrollText size={15} />
              <strong>{t("dashboard.recentLogs")}</strong>
              <span className="panel-muted">
                {t("dashboard.cost", { n: recentCost.toFixed(6) })}
              </span>
            </div>
            {recentLogs.length === 0 ? (
              <p className="dashboard-empty">{t("dashboard.noLogs")}</p>
            ) : (
              <ul className="dashboard-list">
                {recentLogs.map((log: ProxyLog) => {
                  const tone = statusTone(log.status);
                  return (
                    <li key={log.id}>
                      <Link
                        className="dashboard-model"
                        to={`/models?model=${encodeURIComponent(log.model)}`}
                      >
                        {log.model}
                        {log.route_id ? ` #${log.route_id}` : ""}
                      </Link>
                      <span className="dashboard-meta">
                        <span className={`badge badge-${tone}`}>
                          {log.status}
                        </span>
                        <span className="mono-value">{log.latency_ms}ms</span>
                      </span>
                      <span className="dashboard-time">
                        {relativeTime(log.created_at, t)}
                      </span>
                    </li>
                  );
                })}
              </ul>
            )}
          </Panel>
        </div>

        <div className="dashboard-grid dashboard-usage-grid">
          <Panel className="dashboard-panel dashboard-usage-activity">
            <div className="panel-header">
              <TrendingUp size={15} />
              <strong>{t("dashboard.topModels")}</strong>
              <span className="panel-muted">
                {t("dashboard.tokens24h", { n: formatTokens(recentTokens) })}
              </span>
            </div>
            <div className="dashboard-usage-activity-body">
              <section className="dashboard-subpanel">
                {topModels.length === 0 ? (
                  <p className="dashboard-empty">{t("dashboard.topModelsEmpty")}</p>
                ) : (
                  <ul className="model-rank">
                    {topModels.map((m) => (
                      <li key={m.model}>
                        <Link
                          className="model-rank-name"
                          to={`/models?model=${encodeURIComponent(m.model)}`}
                          title={m.model}
                        >
                          {m.model}
                        </Link>
                        <span className="model-rank-track">
                          <span
                            className="model-rank-fill"
                            style={{
                              width: `${(m.requests / maxModelRequests) * 100}%`,
                            }}
                          />
                        </span>
                        <span className="model-rank-meta">
                          <strong>{m.requests}</strong>
                          <small>{t("dashboard.colRequests")}</small>
                          <i>·</i>
                          <strong>{formatTokens(m.tokens)}</strong>
                          <small>{t("dashboard.colTokens")}</small>
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
              <section className="dashboard-subpanel dashboard-activity-subpanel">
                <div className="dashboard-subpanel-head">
                  <Cpu size={14} />
                  <strong>{t("dashboard.activity24h")}</strong>
                  <span className="panel-muted">
                    {t("dashboard.recentRequests")}
                  </span>
                </div>
                {recent.length === 0 ? (
                  <p className="dashboard-empty">{t("dashboard.noActivity")}</p>
                ) : (
                  <ul className="dashboard-list">
                    {recent.slice(0, 10).map((row: UsageRecord) => (
                      <li key={row.id}>
                        <span className="dashboard-model">{row.model}</span>
                        <span className="dashboard-meta">
                          <span className="mono-value">
                            {formatTokens(row.total_tokens ?? 0)}
                          </span>
                          <span className="badge badge-neutral">{row.status}</span>
                        </span>
                        <span className="dashboard-time">
                          {relativeTime(row.created_at, t)}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            </div>
          </Panel>
        </div>
      </div>
    </Page>
  );
}
