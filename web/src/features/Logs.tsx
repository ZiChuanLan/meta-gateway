import { ExternalLink, RefreshCw } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { AuditPanel, DiscoveryPanel } from "./OpsPanels";
import { api } from "../api/client";
import type { ProxyLog } from "../api/types";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { PaginationBar } from "../components/PaginationBar";
import { EntityState } from "../components/EntityState";
import { StatGrid } from "../components/StatGrid";
import {
	Button,
	DataTable,
	Page,
	Panel,
	StatusBadge,
	Tabs,
	formatDate,
} from "../components/ui";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";

function ProxyLogsPanel() {
	const { client } = useSession();
	const { t } = useI18n();
	const service = api(client!);
	const [params, setParams] = useSearchParams();
	const channelId = positiveId(params.get("channel_id"));
	const modelParam = params.get("model")?.trim() || "";
	const failedOnly = params.get("status") !== "all";
	const [modelDraft, setModelDraft] = useState(modelParam);
	const [beforeId, setBeforeId] = useState<number | undefined>(undefined);
	const [selected, setSelected] = useState<ProxyLog | null>(null);

	const filters = useMemo(
		() => ({
			channel_id: channelId,
			model: modelParam || undefined,
			status: failedOnly ? ("failed" as const) : undefined,
			before_id: beforeId,
			limit: 100,
		}),
		[beforeId, channelId, failedOnly, modelParam],
	);

	const logs = useQuery({
		queryKey: ["proxy-logs", filters],
		queryFn: ({ signal }) => service.proxyLogs(filters, signal),
	});
	const channels = useQuery({
		queryKey: ["channels"],
		queryFn: ({ signal }) => service.channels(signal),
	});
	const channelName = useMemo(() => {
		const map = new Map<number, string>();
		for (const channel of channels.data ?? []) {
			map.set(channel.id, channel.name);
		}
		return map;
	}, [channels.data]);

	const rows = logs.data ?? [];
	const pagination = useClientPagination(rows, 20);
	const pageRows = pagination.pageItems;
	const canLoadMore = rows.length >= 100;
	const failedCount = rows.filter((log) => log.status >= 400).length;

	const setFilter = (patch: Record<string, string | null>) => {
		const next = new URLSearchParams(params);
		for (const [key, value] of Object.entries(patch)) {
			if (value == null || value === "") next.delete(key);
			else next.set(key, value);
		}
		setBeforeId(undefined);
		setSelected(null);
		setParams(next, { replace: true });
	};

	return (
		<>
			<div className="toolbar" style={{ marginBottom: 12, flexWrap: "wrap", gap: 8 }}>
				<label className="check" style={{ margin: 0 }}>
					<input
						type="checkbox"
						checked={failedOnly}
						onChange={(e) =>
							setFilter({ status: e.target.checked ? null : "all" })
						}
					/>
					<span>{t("logsPage.failedOnly")}</span>
				</label>
				<Button
					variant="secondary"
					icon={<RefreshCw size={16} />}
					onClick={() => {
						setBeforeId(undefined);
						void logs.refetch();
					}}
				>
					{t("common.refresh")}
				</Button>
			</div>
				<StatGrid
					items={[
						{
							label: t("logsPage.stat.shown"),
							value: logs.isPending ? "—" : rows.length,
						},
						{
							label: t("logsPage.stat.failed"),
							value: logs.isPending ? "—" : failedCount,
						},
						{
							label: t("logsPage.stat.focus"),
							value: failedOnly
								? t("logsPage.focus.failures")
								: t("logsPage.focus.all"),
						},
					]}
				/>

				<div className="split">
					<Panel className="ops-list-panel">
						<div className="filter-bar">
							<select
								aria-label={t("ops.filterChannel")}
								value={channelId ?? 0}
								onChange={(e) => {
									const value = Number(e.target.value);
									setFilter({
										channel_id: value > 0 ? String(value) : null,
									});
								}}
							>
								<option value={0}>{t("ops.allChannels")}</option>
								{(channels.data ?? []).map((c) => (
									<option key={c.id} value={c.id}>
										{c.name}
									</option>
								))}
							</select>
							<input
								value={modelDraft}
								placeholder={t("common.model")}
								onChange={(e) => setModelDraft(e.target.value)}
								onKeyDown={(e) => {
									if (e.key === "Enter") {
										setFilter({ model: modelDraft.trim() || null });
									}
								}}
							/>
							<Button
								variant="secondary"
								onClick={() => setFilter({ model: modelDraft.trim() || null })}
							>
								{t("common.apply")}
							</Button>
							{(channelId || modelParam || !failedOnly) && (
								<Button
									variant="quiet"
									onClick={() => {
										setModelDraft("");
										setFilter({
											channel_id: null,
											model: null,
											status: null,
										});
									}}
								>
									{t("common.clearFilters")}
								</Button>
							)}
						</div>

						<EntityState
							isLoading={logs.isPending}
							isError={logs.isError}
							error={logs.error}
							isEmpty={!rows.length}
							empty={
								<EmptyHero
									kicker={t("logsPage.emptyKicker")}
									title={t("logsPage.emptyTitle")}
									body={t("logsPage.empty")}
									actions={
										<>
											<Link className="button button-primary" to="/">
												{t("logsPage.ctaChannels")}
											</Link>
											<Link
												className="button button-secondary"
												to="/keys?create=1"
											>
												{t("logsPage.ctaKeys")}
											</Link>
										</>
									}
								/>
							}
							retry={() => logs.refetch()}
						>
							<ListShell
								footer={
									<PaginationBar
									page={pagination.page}
									totalPages={pagination.totalPages}
									total={pagination.total}
									pageSize={pagination.pageSize}
									rangeStart={pagination.rangeStart}
									rangeEnd={pagination.rangeEnd}
									hasPrev={pagination.hasPrev}
									hasNext={pagination.hasNext}
									onPageChange={pagination.setPage}
									onPageSizeChange={pagination.setPageSize}
									/>
								}
							>
							<DataTable
								headers={[
									t("common.time"),
									t("common.model"),
									t("common.route"),
									t("common.channel"),
									t("common.status"),
									t("common.tokens"),
									t("common.latency"),
								]}
							>
								{pageRows.map((log) => {
									const active = selected?.id === log.id;
									return (
										<tr
											key={log.id}
											className={[
												log.status >= 400 ? "row-failed" : "",
												"is-clickable",
												active ? "is-selected" : "",
											]
												.filter(Boolean)
												.join(" ")}
											onClick={() => setSelected(log)}
										>
											<td>{formatDate(log.created_at)}</td>
											<td>
												<strong>{log.model}</strong>
												<small className="mono">{log.request_id}</small>
											</td>
											<td>
												{log.route_id ? (
													<Link
														to={`/models?model=${encodeURIComponent(
															log.route_pattern ?? "",
														)}`}
														title={log.route_pattern || undefined}
													>
														<code>#{log.route_id}</code>
													</Link>
												) : (
													"—"
												)}
											</td>
											<td>
												{channelName.get(log.channel_id) ??
													`#${log.channel_id}`}
											</td>
											<td>
												<StatusBadge
													value={
														log.status >= 400 ? "failed" : String(log.status)
													}
												/>
												{log.attempt > 1 ? (
													<span className="log-retry-mark" title={t("logsPage.retried")}>
														{t("logsPage.retried")}
													</span>
												) : null}
											</td>
											<td>{log.total_tokens ? log.total_tokens : "—"}</td>
											<td>{t("common.ms", { n: log.latency_ms })}</td>
										</tr>
									);
								})}
							</DataTable>
							</ListShell>
							{canLoadMore ? (
								<div style={{ marginTop: 12, textAlign: "center" }}>
									<Button
										variant="secondary"
										onClick={() => {
											const last = rows[rows.length - 1];
											if (last) setBeforeId(last.id);
										}}
									>
										{t("common.loadMore")}
									</Button>
								</div>
							) : null}
						</EntityState>
					</Panel>

					<div className="detail-card ops-detail-card is-compact">
						{!selected ? (
							<div className="detail-empty">{t("logsPage.selectHint")}</div>
						) : (
							<>
								<div className="detail-head">
									<div>
										<p className="detail-kicker">{t("logsPage.detailKicker")}</p>
										<h2>{selected.model}</h2>
										<small className="mono">{selected.request_id}</small>
									</div>
									<StatusBadge
										value={
											selected.status >= 400 ? "failed" : String(selected.status)
										}
									/>
								</div>
								<div className="detail-meta">
									<div>
										<span className="label">{t("common.time")}</span>
										<span>{formatDate(selected.created_at)}</span>
									</div>
									<div>
										<span className="label">{t("common.channel")}</span>
										<span>
											{channelName.get(selected.channel_id) ??
												`#${selected.channel_id}`}
										</span>
									</div>
									{selected.route_id ? (
										<div>
											<span className="label">{t("common.route")}</span>
											<span>
												<Link
													to={`/models?model=${encodeURIComponent(
														selected.route_pattern ?? "",
													)}`}
												>
													<code>
														#{selected.route_id}
														{selected.route_pattern
															? ` · ${selected.route_pattern}`
															: ""}
													</code>
												</Link>
											</span>
										</div>
									) : null}
									<div>
										<span className="label">{t("common.latency")}</span>
										<span>{t("common.ms", { n: selected.latency_ms })}</span>
									</div>
									<div>
										<span className="label">{t("common.tokens")}</span>
										<span>
											{selected.total_tokens
												? `${selected.prompt_tokens ?? 0} + ${selected.completion_tokens ?? 0} = ${selected.total_tokens}`
												: "—"}
										</span>
									</div>
									<div>
										<span className="label">{t("common.attemptCol")}</span>
										<span>
											{selected.attempt}
											{selected.attempt > 1 ? (
												<StatusBadge value="retried" />
											) : null}
										</span>
									</div>
									<div>
										<span className="label">{t("common.errorBrief")}</span>
										<span title={selected.error_brief || undefined}>
											{selected.error_brief || "—"}
										</span>
									</div>
								</div>
								<div className="detail-primary-bar">
									<Link
										className="button button-primary"
										to={`/?id=${selected.channel_id}`}
									>
										{t("logsPage.openConnection")}
									</Link>
									<Link
										className="button button-secondary"
										to={`/models?model=${encodeURIComponent(selected.model)}`}
									>
										<ExternalLink size={14} />
										{t("logsPage.openModel")}
									</Link>
								</div>
							</>
						)}
					</div>
				</div>
		</>
	);
}

export function Logs() {
	const { t } = useI18n();
	const [params, setParams] = useSearchParams();
	const rawTab = params.get("tab");
	const tabItems = [
		{ value: "proxy", label: t("logsPage.tab.proxy") },
		{ value: "discovery", label: t("logsPage.tab.discovery") },
		{ value: "audit", label: t("logsPage.tab.audit") },
	];
	const active = tabItems.some((item) => item.value === rawTab)
		? (rawTab as string)
		: "proxy";

	const changeTab = (value: string) => {
		const next = new URLSearchParams(params);
		if (value === "proxy") next.delete("tab");
		else next.set("tab", value);
		// Keep channel/model filters only on proxy tab.
		if (value !== "proxy") {
			next.delete("channel_id");
			next.delete("model");
			next.delete("status");
		}
		setParams(next, { replace: true });
	};

	return (
		<Page
			kicker={t("logsPage.kicker")}
			title={t("logsPage.title")}
			description={t("logsPage.hubDescription")}
		>
			<div className="ops-canvas">
				<Tabs items={tabItems} active={active} onChange={changeTab} />
				{active === "proxy" ? <ProxyLogsPanel /> : null}
				{active === "discovery" ? <DiscoveryPanel /> : null}
				{active === "audit" ? <AuditPanel /> : null}
			</div>
		</Page>
	);
}

function positiveId(value: string | null) {
	if (!value) return undefined;
	const parsed = Number(value);
	return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
}
