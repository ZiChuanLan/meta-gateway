import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { AlertTriangle, Activity, Boxes, Cpu, Zap } from "lucide-react";
import { api } from "../api/client";
import type { ProxyLog, UsageRecord } from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { StatGrid } from "../components/StatGrid";
import { Page, Panel } from "../components/ui";

const HOUR_24 = 24 * 3600 * 1000;

function formatTokens(n: number) {
	if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
	if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
	return String(n);
}

function relativeTime(
	iso: string,
	t: (key: string, vars?: Record<string, string | number>) => string,
) {
	const ms = Date.now() - new Date(iso).getTime();
	if (ms < 60_000) return t("dashboard.justNow");
	if (ms < 3600_000) return t("dashboard.minutesAgo", { n: Math.floor(ms / 60_000) });
	if (ms < HOUR_24) return t("dashboard.hoursAgo", { n: Math.floor(ms / 3600_000) });
	return t("dashboard.daysAgo", { n: Math.floor(ms / HOUR_24) });
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

	const recent = useMemo(() => {
		const cutoff = Date.now() - HOUR_24;
		return (usage.data ?? []).filter((row) => new Date(row.created_at).getTime() >= cutoff);
	}, [usage.data]);

	const channelCounts = useMemo(() => {
		const all = channels.data ?? [];
		const enabled = all.filter((c) => c.channel.status === "enabled").length;
		const healthy = all.filter(
			(c) => c.channel.status === "enabled" && !c.last_probe_error,
		).length;
		return { total: all.length, enabled, healthy };
	}, [channels.data]);

	const recentTokens = recent.reduce((sum, row) => sum + (row.total_tokens ?? 0), 0);
	const recentRequests = recent.length;
	const recentCost = summary.data?.estimated_cost ?? 0;

	const recentLogs = (logs.data ?? []).slice(0, 8);

	return (
		<Page
			kicker={t("dashboard.kicker")}
			title={t("dashboard.title")}
			description={t("dashboard.description")}
		>
			<div className="stack">
				{channelCounts.total === 0 ? (
					<div className="setup-guide">
						<div className="setup-guide-copy">
							<strong>{t("dashboard.guideTitle")}</strong>
							<p>{t("dashboard.guideCopy")}</p>
						</div>
						<ol className="setup-guide-steps">
							<li>
								<span className="setup-guide-step-index">1</span>
								<span>{t("dashboard.guideStep1")}</span>
							</li>
							<li>
								<span className="setup-guide-step-index">2</span>
								<span>{t("dashboard.guideStep2")}</span>
							</li>
							<li>
								<span className="setup-guide-step-index">3</span>
								<span>{t("dashboard.guideStep3")}</span>
							</li>
							<li>
								<span className="setup-guide-step-index">4</span>
								<span>{t("dashboard.guideStep4")}</span>
							</li>
						</ol>
					</div>
				) : null}
				<StatGrid
				columns={4}
				items={[
					{
						label: t("dashboard.totalRequests"),
						value: summary.data?.request_count ?? "—",
						hint: t("dashboard.totalRequestsHint"),
					},
					{
						label: t("dashboard.totalTokens"),
						value: summary.data
							? formatTokens(summary.data.total_tokens)
							: "—",
						hint: t("dashboard.totalTokensHint"),
					},
					{
						label: t("dashboard.recentRequests"),
						value: summary.isPending ? "—" : recentRequests,
						hint: t("dashboard.recentRequestsHint"),
					},
					{
						label: t("dashboard.healthyChannels"),
						value: channels.isPending
							? "—"
							: `${channelCounts.healthy}/${channelCounts.total}`,
						hint: t("dashboard.healthyChannelsHint"),
					},
				]}
			/>

			<div className="dashboard-grid">
				<Panel className="dashboard-panel dashboard-activity">
					<div className="panel-header">
						<Activity size={15} />
						<strong>{t("dashboard.activity24h")}</strong>
						<span className="panel-muted">
							{t("dashboard.tokens24h", { n: formatTokens(recentTokens) })}
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
										{formatTokens(row.total_tokens ?? 0)} · {row.status}
									</span>
									<span className="dashboard-time">
										{relativeTime(row.created_at, t)}
									</span>
								</li>
							))}
						</ul>
					)}
				</Panel>

				<Panel className="dashboard-panel dashboard-health">
					<div className="panel-header">
						<Boxes size={15} />
						<strong>{t("dashboard.channelHealth")}</strong>
						<span className="panel-muted">
							{t("dashboard.enabledOf", { n: channelCounts.enabled, total: channelCounts.total })}
						</span>
					</div>
					<ul className="dashboard-list">
						{(channels.data ?? []).slice(0, 8).map((c) => (
							<li key={c.channel.id}>
								<span className="dashboard-model">{c.channel.name}</span>
								<span className="dashboard-meta">
									{c.channel.status === "enabled" ? (
										c.last_probe_error ? (
											<>
												<AlertTriangle size={12} />
												<span className="dashboard-warn">
													{t("dashboard.degraded")}
												</span>
											</>
										) : (
											<span className="dashboard-ok">
												<Zap size={12} /> {t("dashboard.ready")}
											</span>
										)
									) : (
										<span className="dashboard-warn">{t("dashboard.disabled")}</span>
									)}
								</span>
							</li>
						))}
					</ul>
				</Panel>
			</div>

			<Panel className="dashboard-panel">
				<div className="panel-header">
					<Cpu size={15} />
					<strong>{t("dashboard.recentLogs")}</strong>
					<span className="panel-muted">
						{t("dashboard.cost", { n: recentCost.toFixed(6) })}
					</span>
				</div>
				{recentLogs.length === 0 ? (
					<p className="dashboard-empty">{t("dashboard.noLogs")}</p>
				) : (
					<ul className="dashboard-list">
						{recentLogs.map((log: ProxyLog) => (
							<li key={log.id}>
								<span className="dashboard-model">
									{log.model}
									{log.route_id ? ` #${log.route_id}` : ""}
								</span>
								<span className="dashboard-meta">
									{log.status} · {log.latency_ms}ms
								</span>
								<span className="dashboard-time">
									{relativeTime(log.created_at, t)}
								</span>
							</li>
						))}
					</ul>
				)}
			</Panel>
			</div>
		</Page>
	);
}
