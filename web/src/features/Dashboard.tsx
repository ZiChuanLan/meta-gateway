import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  Activity,
  ArrowRight,
  Boxes,
  Coins,
  Cpu,
  HeartPulse,
  KeyRound,
  Plug,
  ScrollText,
  Zap,
} from "lucide-react";
import { api } from "../api/client";
import type { ProxyLog, UsageRecord } from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { StatGrid } from "../components/StatGrid";
import { HourlyTrafficChart } from "../components/charts";
import { Page, Panel } from "../components/ui";
import { formatTokens } from "../lib/format";

const HOUR_24 = 24 * 3600 * 1000;

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
    queryKey: ["proxy-logs", { limit: 8 }],
    queryFn: ({ signal }) => s.proxyLogs({ limit: 8 }, signal),
    refetchInterval: 15_000,
  });

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
      (c) => c.channel.status === "enabled" && !c.last_probe_error,
    ).length;
    return { total: all.length, enabled, healthy };
  }, [channels.data]);

  /** Hourly buckets (oldest → newest) for the SVG chart. */
  const hourly = useMemo(() => {
    const buckets = Array.from({ length: 24 }, () => ({ req: 0, tok: 0 }));
    for (const row of usage.data ?? []) {
      const h = Math.floor(
        (now - new Date(row.created_at).getTime()) / 3600_000,
      );
      if (h >= 0 && h < 24) {
        const bucket = buckets[23 - h];
        if (bucket) {
          bucket.req += 1;
          bucket.tok += row.total_tokens ?? 0;
        }
      }
    }
    const labels = buckets.map((_, i) => {
      const d = new Date(now - (23 - i) * 3600_000);
      return `${String(d.getHours()).padStart(2, "0")}:00`;
    });
    return {
      requests: buckets.map((b) => b.req),
      tokens: buckets.map((b) => b.tok),
      labels,
    };
  }, [usage.data, now]);

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

  const recentLogs = (logs.data ?? []).slice(0, 8);

  return (
    <Page
      kicker={t("dashboard.kicker")}
      title={t("dashboard.title")}
      description={t("dashboard.description")}
    >
      <div className="stack">
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
          columns={4}
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
          ]}
        />

        <Panel className="dashboard-panel dashboard-chart-panel">
          <div className="panel-header">
            <Activity size={15} />
            <strong>{t("dashboard.hourlyTraffic")}</strong>
            <span className="panel-muted">
              {t("dashboard.tokens24h", { n: formatTokens(recentTokens) })}
            </span>
          </div>
          <HourlyTrafficChart
            requests={hourly.requests}
            tokens={hourly.tokens}
            labels={hourly.labels}
          />
        </Panel>

        <div className="dashboard-grid">
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
              {(channels.data ?? []).slice(0, 8).map((c) => (
                <li key={c.channel.id}>
                  <span
                    className={`dot dot-${c.channel.status === "enabled" ? (c.last_probe_error ? "warn" : "ok") : "off"}`}
                  />
                  <Link
                    className="dashboard-model"
                    to={`/channels?id=${c.channel.id}`}
                  >
                    {c.channel.name}
                  </Link>
                  <span className="dashboard-meta">
                    {c.channel.status === "enabled" ? (
                      c.last_probe_error ? (
                        <span className="badge badge-warn">
                          <AlertTriangle size={11} />
                          {t("dashboard.degraded")}
                        </span>
                      ) : (
                        <span className="badge badge-ok">
                          <Zap size={11} /> {t("dashboard.ready")}
                        </span>
                      )
                    ) : (
                      <span className="badge badge-neutral">
                        {t("dashboard.disabled")}
                      </span>
                    )}
                  </span>
                </li>
              ))}
            </ul>
          </Panel>

          <Panel className="dashboard-panel dashboard-activity">
            <div className="panel-header">
              <Cpu size={15} />
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
          </Panel>
        </div>

        <Panel className="dashboard-panel">
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
    </Page>
  );
}
