import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
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
  Play,
  ScrollText,
  Timer,
  TrendingUp,
  Wallet,
  Zap,
} from "lucide-react";
import { api } from "../api/client";
import type { ProxyLog } from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { SetupGuide } from "./SetupGuide";
import { TelemetrySecondary, TelemetryStrip } from "../components/TelemetryStrip";
import { TimeRangePicker, describeRange, useUrlTimeRange } from "../components/TimeRangePicker";
import { HourlyTrafficChart } from "../components/charts";
import { Button, Page, Panel } from "../components/ui";
import { formatCost, formatTokens } from "../lib/format";
import { channelHealthState } from "./channelHealth";
import { DashboardAura } from "../components/DashboardAura";
import { GatewayPreview } from "../components/GatewayTransition";

const MINUTE_MS = 60_000;

/** Bucket labels are formatted here so they follow the viewer's timezone. */
function seriesLabels(since: string, bucketSeconds: number, count: number): string[] {
  const start = new Date(since).getTime();
  const pad = (n: number) => String(n).padStart(2, "0");
  return Array.from({ length: count }, (_, i) => {
    const d = new Date(start + i * bucketSeconds * 1000);
    if (bucketSeconds >= 86400) return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
    if (bucketSeconds >= 3600) return `${pad(d.getHours())}:00`;
    return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  });
}

/** Localized bucket-size caption ("1 hour", "6 h", "15 min"). */
function granularity(seconds: number): { key: string; n: number } {
  if (seconds >= 86400) return { key: "dashboard.unitDay", n: Math.round(seconds / 86400) };
  if (seconds >= 3600) return { key: "dashboard.unitHour", n: Math.round(seconds / 3600) };
  return { key: "dashboard.unitMinute", n: Math.max(1, Math.round(seconds / 60)) };
}

function relativeTime(
  iso: string,
  t: (key: string, vars?: Record<string, string | number>) => string,
) {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < MINUTE_MS) return t("dashboard.justNow");
  if (ms < 3600_000) return t("dashboard.minutesAgo", { n: Math.floor(ms / MINUTE_MS) });
  if (ms < 24 * 3600_000)
    return t("dashboard.hoursAgo", { n: Math.floor(ms / 3600_000) });
  return t("dashboard.daysAgo", { n: Math.floor(ms / (24 * 3600_000)) });
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

/** Drill-down target: a bucket of the main series. */
type Zoom = { since: string; until: string; label: string };

export function Dashboard() {
  const [replayEntrance, setReplayEntrance] = useState(false);
  const { client } = useSession();
  const s = api(client!);
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const range = useUrlTimeRange(params, setParams);
  const [zoom, setZoom] = useState<Zoom | null>(null);

  // All-time totals stay available so "how many requests has this gateway ever
  // served" does not depend on the selected window.
  const allTime = useQuery({
    queryKey: ["usage-summary", "all"],
    queryFn: ({ signal }) => s.usageSummary(undefined, signal),
    refetchInterval: 30_000,
  });
  // Everything else is windowed: the cards, the matrix, the chart, the ranking.
  const rangeSummary = useQuery({
    queryKey: ["usage-summary", "range", { since: range.since, until: range.until }],
    queryFn: ({ signal }) =>
      s.usageSummary(undefined, signal, range.since, range.until),
    refetchInterval: 30_000,
  });
  // The equal-length window immediately before, for an honest trend badge.
  const previous = useQuery({
    queryKey: ["usage-summary", "previous", { since: range.since, until: range.until }],
    enabled: Boolean(range.since && range.until),
    queryFn: ({ signal }) => {
      const from = new Date(range.since!).getTime();
      const span = new Date(range.until!).getTime() - from;
      return s.usageSummary(
        undefined,
        signal,
        new Date(from - span).toISOString(),
        range.since,
      );
    },
    refetchInterval: 60_000,
  });
  const zoomSummary = useQuery({
    queryKey: ["usage-summary", "range", { since: zoom?.since, until: zoom?.until }],
    enabled: zoom != null,
    queryFn: ({ signal }) => s.usageSummary(undefined, signal, zoom!.since, zoom!.until),
  });
  const series = useQuery({
    queryKey: [
      "usage-series",
      zoom ? { since: zoom.since, until: zoom.until, buckets: 12 } : { since: range.since, until: range.until, buckets: 48 },
    ],
    queryFn: ({ signal }) =>
      zoom
        ? s.usageSeries({ since: zoom.since, until: zoom.until, buckets: 12 }, signal)
        : s.usageSeries({ since: range.since, until: range.until, buckets: 48 }, signal),
    refetchInterval: zoom ? false : 30_000,
  });
  const topModels = useQuery({
    queryKey: ["usage-top-models", { since: range.since, until: range.until }],
    queryFn: ({ signal }) =>
      s.usageTopModels({ since: range.since, until: range.until, limit: 6 }, signal),
    refetchInterval: 30_000,
  });
  const channels = useQuery({
    queryKey: ["channel-overviews"],
    queryFn: ({ signal }) => s.channelOverviews(signal),
    refetchInterval: 30_000,
  });
  const logs = useQuery({
    queryKey: ["proxy-logs", { limit: 8, since: range.since, until: range.until }],
    queryFn: ({ signal }) =>
      s.proxyLogs({ limit: 8, since: range.since, until: range.until }, signal),
    refetchInterval: 15_000,
  });

  const channelCounts = useMemo(() => {
    const all = channels.data ?? [];
    const enabled = all.filter((c) => c.channel.status === "enabled").length;
    const healthy = all.filter((c) => channelHealthState(c) === "healthy").length;
    return { total: all.length, enabled, healthy };
  }, [channels.data]);

  const summary = rangeSummary.data;
  const matrix = (zoom ? zoomSummary.data : undefined) ?? summary;

  const windowRequests = summary?.request_count ?? 0;
  const previousRequests = previous.data?.request_count ?? 0;
  const requestTrend =
    range.since && previousRequests > 0 && windowRequests > 0
      ? windowRequests / previousRequests - 1
      : null;

  // 2xx share. Older gateways omit the breakdown, so fall back to "total minus
  // everything that is not 2xx".
  const okCount =
    matrix == null
      ? 0
      : (matrix.ok_count ??
        Math.max(
          0,
          matrix.request_count -
            (matrix.client_error_count ?? 0) -
            (matrix.server_error_count ?? 0) -
            (matrix.other_count ?? 0),
        ));
  const successRate =
    matrix && matrix.request_count > 0 ? okCount / matrix.request_count : null;
  const successTone =
    successRate === null
      ? "primary"
      : successRate >= 0.99
        ? "success"
        : successRate >= 0.9
          ? "warning"
          : "danger";

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

  const bucketSeconds = series.data?.bucket_seconds ?? 3600;
  const labels = useMemo(
    () =>
      series.data
        ? seriesLabels(
            series.data.since,
            series.data.bucket_seconds,
            series.data.requests.length,
          )
        : [],
    [series.data],
  );
  const windowTokens = (series.data?.tokens ?? []).reduce((sum, n) => sum + n, 0);
  const grain = granularity(bucketSeconds);

  const openBucket = (index: number) => {
    if (!series.data) return;
    const start =
      new Date(series.data.since).getTime() + index * series.data.bucket_seconds * 1000;
    setZoom({
      since: new Date(start).toISOString(),
      until: new Date(start + series.data.bucket_seconds * 1000).toISOString(),
      label: labels[index] ?? "",
    });
  };

  const ranked = topModels.data ?? [];
  const maxModelRequests = Math.max(1, ...ranked.map((m) => m.requests));
  const recentLogs = logs.data ?? [];
  const rangeCaption = describeRange(range.since, range.until, t);

  return (
    <Page
      className="dashboard-page"
      kicker={t("dashboard.kicker")}
      title={t("dashboard.title")}
      description={t("dashboard.description")}
      actions={<Button variant="quiet" icon={<Play size={14} />} onClick={() => setReplayEntrance(true)}>{t("motion.replay")}</Button>}
    >
      <DashboardAura />
      {replayEntrance ? <GatewayPreview onClose={() => setReplayEntrance(false)} /> : null}
      <div className="cockpit-stack">
        <SetupGuide />

        {/* 0. 时间区间：驱动本页所有读数、图表与排行 */}
        <div className="dashboard-range-bar">
          <span className="dashboard-range-label">
            <Timer size={13} />
            {t("timeRange.label")}
          </span>
          <TimeRangePicker
            range={range}
            onRefresh={() => {
              setZoom(null);
              void series.refetch();
            }}
          />
        </div>

        {/* 1. 终端接入端点条 (Gateway Endpoint Strip) */}
        <EndpointStrip />

        {/* 2. 一体化遥测读数条 (Instrument Telemetry Band) */}
        <div className="telemetry-stack">
          <TelemetryStrip
            items={[
              {
                label: t("dashboard.totalRequests"),
                value: allTime.data?.request_count ?? "—",
                hint: t("dashboard.totalRequestsHint"),
                icon: <Activity size={13} />,
                tone: "primary",
              },
              {
                label: t("dashboard.recentRequests"),
                value: rangeSummary.isPending ? "—" : windowRequests,
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
                  rangeSummary.isPending || successRate === null
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
                value: allTime.data
                  ? formatTokens(allTime.data.total_tokens)
                  : "—",
                hint: t("dashboard.totalTokensHint"),
                icon: <Coins size={13} />,
              },
              {
                label: t("dashboard.rangeCost"),
                value: rangeSummary.isPending ? "—" : formatCost(summary?.cost ?? 0),
                hint: t("dashboard.rangeCostHint"),
                icon: <Wallet size={13} />,
              },
              {
                label: t("dashboard.cacheRead"),
                value: rangeSummary.isPending
                  ? "—"
                  : formatTokens(summary?.cache_read_tokens ?? 0),
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
              {zoom ? (
                <button
                  type="button"
                  className="chart-back-button"
                  onClick={() => setZoom(null)}
                  aria-label={t("dashboard.chartBack")}
                >
                  <ArrowLeft size={14} />
                </button>
              ) : (
                <Activity size={15} />
              )}
              <strong>
                {zoom
                  ? t("dashboard.hourlyDetail", { label: zoom.label })
                  : t("dashboard.hourlyTraffic")}
              </strong>
              <span className="chart-detail-pill">
                {t("dashboard.granularity", { unit: t(grain.key, { n: grain.n }) })}
              </span>
            </div>
            <span className="panel-muted">
              {zoom
                ? t("dashboard.chartDetailSummary", {
                    n: (series.data?.requests ?? []).reduce((sum, n) => sum + n, 0),
                  })
                : t("dashboard.rangeTokens", { n: formatTokens(windowTokens) })}
            </span>
          </div>
          <HourlyTrafficChart
            key={zoom ? `zoom-${zoom.since}` : "overview"}
            requests={series.data?.requests ?? []}
            tokens={series.data?.tokens ?? []}
            failed={series.data?.failed ?? []}
            labels={labels}
            height={zoom ? 200 : 168}
            labelStep={Math.max(1, Math.round((labels.length || 1) / 8))}
            zoomed={zoom != null}
            onSelect={zoom ? undefined : openBucket}
          />
          <ResultDistribution
            ok={okCount}
            clientError={matrix?.client_error_count ?? 0}
            serverError={matrix?.server_error_count ?? 0}
            other={matrix?.other_count ?? 0}
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

          {/* 右轨：最近代理请求流（跟随所选区间） */}
          <Panel className="cockpit-panel cockpit-logs-panel">
            <div className="panel-header">
              <div className="cockpit-panel-title">
                <ScrollText size={14} />
                <strong>{t("dashboard.recentLogs")}</strong>
              </div>
              <span className="panel-muted">{rangeCaption}</span>
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

        {/* 5. 模型负载消耗排行（SQL 聚合，不受列表行数上限影响） */}
        <Panel className="cockpit-panel cockpit-usage-panel">
          <div className="panel-header">
            <div className="cockpit-panel-title">
              <TrendingUp size={14} />
              <strong>{t("dashboard.topModels")}</strong>
            </div>
            <span className="panel-muted">
              {t("dashboard.rangeTokens", { n: formatTokens(windowTokens) })}
            </span>
          </div>
          <div className="cockpit-usage-body">
            <div className="cockpit-subcol">
              {ranked.length === 0 ? (
                <p className="dashboard-empty">{t("dashboard.topModelsEmpty")}</p>
              ) : (
                <ul className="model-rank">
                  {ranked.map((m) => (
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
                        <strong>{formatTokens(m.total_tokens)}</strong>
                        <small>{t("dashboard.colTokens")}</small>
                        {m.failed > 0 ? (
                          <>
                            <i>·</i>
                            <strong className="is-warn">{m.failed}</strong>
                            <small>{t("dashboard.colFailed")}</small>
                          </>
                        ) : null}
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
