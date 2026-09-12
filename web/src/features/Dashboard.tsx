import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  Activity,
  ArrowLeft,
  Boxes,
  Check,
  CheckCircle2,
  Coins,
  Copy,
  Database,
  HeartPulse,
  ScrollText,
  TrendingUp,
  Wallet,
  Zap,
} from "lucide-react";
import { api } from "../api/client";
import type {
  ProxyLog,
} from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { SetupGuide } from "./SetupGuide";
import { TelemetrySecondary, TelemetryStrip } from "../components/TelemetryStrip";
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
      // Clipboard unavailable
    }
  };
  return (
    <div className="endpoint-strip">
      <span className="endpoint-mono" aria-hidden="true">
        POST
      </span>
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
  const recentSummary = useQuery({
    queryKey: ["usage-summary", "24h"],
    queryFn: ({ signal }) =>
      s.usageSummary(
        undefined,
        signal,
        new Date(Date.now() - HOUR_24).toISOString(),
      ),
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
    queryKey: ["proxy-logs", { limit: 6 }],
    queryFn: ({ signal }) => s.proxyLogs({ limit: 6 }, signal),
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
      if (index >= 0 && index < n) {
        buckets[index]!.req += 1;
        buckets[index]!.tok += row.total_tokens ?? 0;
        buckets[index]!.cacheRead += row.cache_read_tokens ?? 0;
        buckets[index]!.cacheWrite += row.cache_creation_tokens ?? 0;
      }
    }
    return {
      requests: buckets.map((b) => b.req),
      tokens: buckets.map((b) => b.tok),
      cacheRead: buckets.map((b) => b.cacheRead),
      cacheWrite: buckets.map((b) => b.cacheWrite),
      labels: starts.map((ms) => {
        const d = new Date(ms);
        return `${String(d.getHours()).padStart(2, "0")}:00`;
      }),
      starts,
    };
  }, [usage.data, now, windowHours]);

  /** Detailed minute buckets for the selected hour. */
  const detail = useMemo(() => {
    if (selectedHour == null) return null;
    const hourStart = hourly.starts[selectedHour];
    if (hourStart == null) return null;
    const buckets = Array.from({ length: 12 }, () => ({ req: 0, tok: 0 }));
    for (const row of usage.data ?? []) {
      const ms = new Date(row.created_at).getTime();
      if (ms >= hourStart && ms < hourStart + 3600_000) {
        const index = Math.floor((ms - hourStart) / (5 * 60_000));
        if (index >= 0 && index < 12) {
          buckets[index]!.req += 1;
          buckets[index]!.tok += row.total_tokens ?? 0;
        }
      }
    }
    return {
      requests: buckets.map((b) => b.req),
      tokens: buckets.map((b) => b.tok),
      labels: Array.from({ length: 12 }, (_, i) => {
        const d = new Date(hourStart + i * 5 * 60_000);
        return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
      }),
      start: hourStart,
    };
  }, [selectedHour, hourly.starts, usage.data]);

  const recentRequests =
    recentSummary.data?.request_count ??
    recent.length;
  const recentTokens =
    recentSummary.data?.total_tokens ??
    recent.reduce((sum, row) => sum + (row.total_tokens ?? 0), 0);
  const prev24 =
    (summary.data?.request_count ?? 0) - recentRequests;
  const recentCost =
    recentSummary.data?.cost ??
    recentSummary.data?.estimated_cost ??
    recent.reduce((sum, row) => sum + (row.cost ?? 0), 0);
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

  const recentLogs = logs.data ?? [];

  return (
    <Page
      kicker={t("dashboard.kicker")}
      title={t("dashboard.title")}
      description={t("dashboard.description")}
    >
      <div className="cockpit-stack">
        <SetupGuide />

        {/* 1. 终端接入端点条 (Gateway Endpoint Strip) */}
        <EndpointStrip />

        {/* 2. 一体化遥测读数条 (Instrument Telemetry Band) */}
        <div className="telemetry-stack">
          <TelemetryStrip
            items={[
              {
                label: t("dashboard.totalRequests"),
                value: summary.data?.request_count ?? "—",
                hint: t("dashboard.totalRequestsHint"),
                icon: <Activity size={13} />,
                tone: "primary",
              },
              {
                label: t("dashboard.recentRequests"),
                value: summary.isPending ? "—" : recentRequests,
                hint: t("dashboard.recentRequestsHint"),
                icon: <ScrollText size={13} />,
                tone: "success",
                trend: requestTrend,
              },
              {
                label: t("dashboard.healthyChannels"),
                value: channels.isPending
                  ? "—"
                  : `${channelCounts.healthy}/${channelCounts.total}`,
                hint: t("dashboard.healthyChannelsHint"),
                icon: <HeartPulse size={13} />,
                tone: healthTone,
              },
              {
                label: t("dashboard.successRate"),
                value:
                  summary.isPending || successRate === null
                    ? "—"
                    : `${Math.round(successRate * 100)}%`,
                hint: t("dashboard.successRateHint"),
                icon: <CheckCircle2 size={13} />,
                tone: successTone,
              },
            ]}
          />
          <TelemetrySecondary
            items={[
              {
                label: t("dashboard.totalTokens"),
                value: summary.data
                  ? formatTokens(summary.data.total_tokens)
                  : "—",
                hint: t("dashboard.totalTokensHint"),
                icon: <Coins size={13} />,
              },
              {
                label: t("dashboard.cost24h"),
                value: recentSummary.isPending ? "—" : formatCost(recentCost),
                hint: t("dashboard.cost24hHint"),
                icon: <Wallet size={13} />,
              },
              {
                label: t("dashboard.cacheRead"),
                value: summary.isPending ? "—" : formatTokens(cacheRead24h),
                hint: t("dashboard.cacheReadHint"),
                icon: <Database size={13} />,
              },
            ]}
          />
        </div>

        {/* 3. 全景流量波形与状态分布监视舱 (Traffic & Result Matrix) */}
        <Panel className="cockpit-panel cockpit-chart-panel">
          <div className="panel-header cockpit-chart-header">
            <div className="cockpit-chart-title">
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
            height={detail ? 200 : 160}
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

        {/* 4. 双轨联动作战区：渠道健康状态阵列 + 实时遥测日志流 */}
        <div className="cockpit-dual-grid">
          {/* 左轨：渠道健康雷达点阵 */}
          <Panel className="cockpit-panel cockpit-health-panel">
            <div className="panel-header">
              <div className="cockpit-panel-title">
                <Boxes size={14} />
                <strong>{t("dashboard.channelHealth")}</strong>
              </div>
              <span className="panel-muted">
                {t("dashboard.enabledOf", {
                  n: channelCounts.enabled,
                  total: channelCounts.total,
                })}
              </span>
            </div>
            <ul className="cockpit-channel-list">
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
                  <li key={c.channel.id} className={`cockpit-channel-item is-${tone}`}>
                    <Link
                      className="cockpit-channel-name"
                      to={`/channels?id=${c.channel.id}`}
                      title={c.channel.name}
                    >
                      {c.channel.name}
                    </Link>
                    <span className="cockpit-channel-meta">
                      {health === "healthy" ? (
                        <span className="badge badge-ok">
                          <Zap size={10} /> {t("dashboard.ready")}
                        </span>
                      ) : health === "disabled" ? (
                        <span className="badge badge-neutral">
                          {t("dashboard.disabled")}
                        </span>
                      ) : (
                        <span
                          className={`badge badge-${health === "unhealthy" ? "danger" : "warn"}`}
                        >
                          <AlertTriangle size={10} />
                          {t(`channels.healthState.${health}`)}
                        </span>
                      )}
                    </span>
                  </li>
                );
              })}
            </ul>
          </Panel>

          {/* 右轨：最近代理请求流 */}
          <Panel className="cockpit-panel cockpit-logs-panel">
            <div className="panel-header">
              <div className="cockpit-panel-title">
                <ScrollText size={14} />
                <strong>{t("dashboard.recentLogs")}</strong>
              </div>
              <span className="panel-muted">
                {t("dashboard.cost", { n: recentCost.toFixed(6) })}
              </span>
            </div>
            {recentLogs.length === 0 ? (
              <p className="dashboard-empty">{t("dashboard.noLogs")}</p>
            ) : (
              <ul className="cockpit-log-list">
                {recentLogs.map((log: ProxyLog) => {
                  const tone = statusTone(log.status);
                  return (
                    <li key={log.id} className="cockpit-log-item">
                      <span className={`cockpit-log-status is-${tone}`} aria-hidden="true" />
                      <Link
                        className="cockpit-log-model"
                        to={`/models?model=${encodeURIComponent(log.model)}`}
                      >
                        {log.model}
                        {log.route_id ? ` #${log.route_id}` : ""}
                      </Link>
                      <div className="cockpit-log-right">
                        {(log.total_tokens ?? 0) > 0 ? (
                          <span className="mono-value">
                            {formatTokens(log.total_tokens ?? 0)}
                          </span>
                        ) : null}
                        <span className={`badge badge-${tone}`}>
                          {log.status}
                        </span>
                        <span className="mono-value">{log.latency_ms}ms</span>
                        <span className="cockpit-log-time">
                          {relativeTime(log.created_at, t)}
                        </span>
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </Panel>
        </div>

        {/* 5. 模型负载消耗排行与 24h 实时请求分析 */}
        <Panel className="cockpit-panel cockpit-usage-panel">
          <div className="panel-header">
            <div className="cockpit-panel-title">
              <TrendingUp size={14} />
              <strong>{t("dashboard.topModels")}</strong>
            </div>
            <span className="panel-muted">
              {t("dashboard.tokens24h", { n: formatTokens(recentTokens) })}
            </span>
          </div>
          <div className="cockpit-usage-body">
            <div className="cockpit-subcol">
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
                            transform: `scaleX(${m.requests / maxModelRequests})`,
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
            </div>

          </div>
        </Panel>
      </div>
    </Page>
  );
}