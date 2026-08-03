import { DatabaseBackup, Play, RefreshCw, ShieldCheck } from "lucide-react";
import { useMutation, useQuery, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import type { RuntimeEditableSettings } from "../api/types";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { useModules } from "../hooks/useModules";
import { useToast } from "../toast";
import {
	SCHEDULE_PRESETS,
	scheduleFromSettings,
	settingsFromSchedule,
	type SchedulePresetId,
} from "../lib/schedulePresets";
import { PaginationBar } from "../components/PaginationBar";
import {
	Button,
	ConfirmDialog,
	DataTable,
	Empty,
	ErrorState,
	Loading,
	Panel,
	StatusBadge,
	formatBytes,
	formatDate,
} from "../components/ui";

const DISCOVERY_INVALIDATE_KEYS: QueryKey[] = [
	["models"],
	["channels"],
	["channel-overviews"],
	["routes"],
	["route-overviews"],
	["members"],
	["explain"],
];


export function DiscoveryPanel() {
	const { client } = useSession();
	const { t } = useI18n();
	const s = api(client!);
	const channels = useQuery({
		queryKey: ["channels"],
		queryFn: ({ signal }) => s.channels(signal),
	});
	const [filter, setFilter] = useState(0);
	const models = useQuery({
		queryKey: ["models", filter],
		queryFn: ({ signal }) => s.discoveredModels(filter || undefined, signal),
	});
	const refresh = useAdminMutation({
		mutationFn: s.refreshAll,
		invalidateKeys: DISCOVERY_INVALIDATE_KEYS,
	});
	const refreshOne = useAdminMutation({
		mutationFn: s.refreshChannel,
		invalidateKeys: DISCOVERY_INVALIDATE_KEYS,
		pendingIdOf: (channelId: number) => channelId,
	});
	const failedChannelIds = new Set(
		(refresh.data?.items ?? []).filter((item) => item.error).map((item) => item.channel_id),
	);
	const modelRows = models.data ?? [];
	const modelPagination = useClientPagination(modelRows, 15);
	const refreshBusy = refresh.isPending || refreshOne.isPending;
	return (
		<Panel
			actions={
				<>
					<select
						aria-label={t("ops.filterChannel")}
						value={filter}
						onChange={(e) => {
							refreshOne.reset();
							setFilter(Number(e.target.value));
						}}
					>
						<option value="0">{t("ops.allChannels")}</option>
						{channels.data?.map((c) => (
							<option value={c.id} key={c.id}>
								{c.name}
							</option>
						))}
					</select>
					{filter > 0 && (
						<Button
							variant="secondary"
							icon={<RefreshCw size={16} />}
							disabled={refreshBusy}
							onClick={() => {
								refresh.reset();
								refreshOne.mutate(filter);
							}}
						>
							{refreshOne.isPending ? t("ops.refreshing") : t("ops.refreshChannel")}
						</Button>
					)}
					<Button
						icon={<RefreshCw size={16} />}
						disabled={refreshBusy}
						onClick={() => {
							refreshOne.reset();
							refresh.mutate();
						}}
					>
						{refresh.isPending ? t("ops.refreshing") : t("ops.refreshAll")}
					</Button>
				</>
			}
		>
			{refresh.data && (
				<div className="result-strip">
					<StatusBadge value={refresh.data.failure_count > 0 ? "failed" : "success"} />
					<span>
						{t("ops.refreshSummary", {
							success: refresh.data.success_count,
							failure: refresh.data.failure_count,
						})}
					</span>
				</div>
			)}
			{refreshOne.data && (
				<div className="result-strip">
					<StatusBadge value="success" />
					<span>
						{t("ops.refreshChannelResult", {
							id: refreshOne.data.channel_id,
							models: refreshOne.data.models.length,
							routes: refreshOne.data.created_routes,
						})}
					</span>
				</div>
			)}
			{refresh.data && failedChannelIds.size > 0 && (
				<div className="result-strip result-strip-error">
					<span>
						{t("ops.refreshFailures", {
							channels: Array.from(failedChannelIds)
								.map((id) => `#${id}`)
								.join(", "),
						})}
					</span>
				</div>
			)}
			{models.isPending ? (
				<Loading />
			) : models.isError ? (
				<ErrorState error={models.error} />
			) : (
				<>
					<DataTable
						headers={[
							t("common.model"),
							t("common.channel"),
							t("common.source"),
							t("common.available"),
							t("common.latency"),
							t("common.checked"),
						]}
						empty={!modelRows.length}
					>
						{modelPagination.pageItems.map((m) => (
							<tr
								key={m.id}
								className={failedChannelIds.has(m.channel_id) ? "row-failed" : undefined}
							>
								<td>
									<strong>{m.model_name}</strong>
								</td>
								<td>#{m.channel_id}</td>
								<td>{m.source}</td>
								<td>
									<StatusBadge value={m.available} />
								</td>
								<td>{t("common.ms", { n: m.latency_ms })}</td>
								<td>{formatDate(m.checked_at)}</td>
							</tr>
						))}
					</DataTable>
					<PaginationBar
						page={modelPagination.page}
						totalPages={modelPagination.totalPages}
						total={modelPagination.total}
						pageSize={modelPagination.pageSize}
						rangeStart={modelPagination.rangeStart}
						rangeEnd={modelPagination.rangeEnd}
						hasPrev={modelPagination.hasPrev}
						hasNext={modelPagination.hasNext}
						onPageChange={modelPagination.setPage}
						onPageSizeChange={modelPagination.setPageSize}
					/>
				</>
			)}
		</Panel>
	);
}

/** Human-readable check-in category; falls back to the raw code. */
function checkinCategoryLabel(
	category: string,
	t: (key: string, vars?: Record<string, string | number>) => string,
): string {
	const key = `ops.checkinCategory.${category}`;
	const translated = t(key);
	// i18n returns the key itself when missing.
	if (translated === key) return category;
	return translated;
}

function checkinDetailText(
	log: { category: string; message?: string },
	t: (key: string, vars?: Record<string, string | number>) => string,
): string {
	const message = (log.message || "").trim();
	const label = checkinCategoryLabel(log.category, t);
	if (!message) return label;
	// Avoid "Label — Label" when message already matches a short English category phrase.
	if (message.toLowerCase() === log.category.toLowerCase()) return label;
	if (message === label) return label;
	return `${label} — ${message}`;
}

function siteDisplayName(
	siteId: number,
	sitesById: Map<number, { name: string; base_url: string }>,
	t: (key: string, vars?: Record<string, string | number>) => string,
): { title: string; subtitle: string } {
	const site = sitesById.get(siteId);
	if (!site) {
		return {
			title: t("common.siteId", { id: siteId }),
			subtitle: "",
		};
	}
	const name = site.name.trim() || site.base_url.trim() || t("common.siteId", { id: siteId });
	let host = "";
	try {
		host = site.base_url ? new URL(site.base_url).host : "";
	} catch {
		host = site.base_url;
	}
	const subtitleParts = [`#${siteId}`];
	if (host && host !== name) subtitleParts.push(host);
	return {
		title: name,
		subtitle: subtitleParts.join(" · "),
	};
}

export function CheckinsPanel() {
	const { client } = useSession();
	const { t } = useI18n();
	const toast = useToast();
	const { checkinEnabled, ready: modulesReady } = useModules();
	const s = api(client!);
	const qc = useQueryClient();
	const [status, setStatus] = useState("");
	const [confirmRun, setConfirmRun] = useState(false);
	const runtime = useQuery({
		queryKey: ["runtime-settings"],
		queryFn: ({ signal }) => s.runtimeSettings(signal),
		enabled: modulesReady && checkinEnabled,
	});
	const [scheduleDraft, setScheduleDraft] = useState<{
		preset: SchedulePresetId;
		cron: string;
	} | null>(null);
	useEffect(() => {
		if (scheduleDraft) return;
		if (!runtime.data?.editable) return;
		setScheduleDraft(
			scheduleFromSettings({
				enabled: runtime.data.editable.checkin_enabled,
				cron: runtime.data.editable.checkin_cron,
			}),
		);
	}, [runtime.data, scheduleDraft]);
	const saveSchedule = useAdminMutation({
		mutationFn: (next: { enabled: boolean; cron: string }) => {
			const editable = runtime.data?.editable;
			if (!editable) throw new Error("runtime settings unavailable");
			return s.updateRuntimeSettings({
				...editable,
				checkin_enabled: next.enabled,
				checkin_cron: next.cron,
			});
		},
		invalidateKeys: [["runtime-settings"]],
		onSuccess: () => {
			toast.push({ tone: "success", message: t("ops.checkin.scheduleSaved") });
		},
	});
	const logs = useQuery({
		queryKey: ["checkin-logs", status],
		queryFn: ({ signal }) =>
			s.checkinLogs(`?limit=100${status ? `&status=${status}` : ""}`, signal),
		enabled: modulesReady && checkinEnabled,
		retry: (failureCount, error) => {
			const message = error instanceof Error ? error.message : String(error);
			if (/plugin_disabled/i.test(message)) return false;
			const statusCode = (error as { status?: number } | null)?.status;
			if (statusCode === 404) return false;
			return failureCount < 2;
		},
	});
	const sites = useQuery({
		queryKey: ["sites"],
		queryFn: ({ signal }) => s.sites(signal),
		enabled: modulesReady && checkinEnabled,
	});
	const sitesById = useMemo(() => {
		const map = new Map<number, { name: string; base_url: string }>();
		for (const site of sites.data ?? []) {
			map.set(site.id, { name: site.name, base_url: site.base_url });
		}
		return map;
	}, [sites.data]);
	const run = useMutation({
		mutationFn: s.runAllCheckins,
		onSuccess: () => {
			setConfirmRun(false);
			void qc.invalidateQueries({ queryKey: ["checkin-logs"] });
			void qc.invalidateQueries({ queryKey: ["credentials"] });
			void qc.invalidateQueries({ queryKey: ["channel-overviews"] });
		},
		onError: () => setConfirmRun(false),
	});
	const checkinRows = logs.data ?? [];
	const checkinPagination = useClientPagination(checkinRows, 15);

	if (modulesReady && !checkinEnabled) {
		return (
			<Panel>
				<p className="detail-empty">{t("ops.checkinModuleOff")}</p>
			</Panel>
		);
	}

	const schedule = scheduleDraft ?? { preset: "off" as const, cron: "" };
	const scheduleDirty =
		runtime.data != null &&
		scheduleDraft != null &&
		(scheduleDraft.preset === "off"
			? runtime.data.editable.checkin_enabled
			: !runtime.data.editable.checkin_enabled ||
				runtime.data.editable.checkin_cron !== scheduleDraft.cron);

	return (
		<>
			<Panel title={t("ops.checkin.scheduleTitle")}>
				<p className="exchange-panel-note" style={{ marginBottom: 12 }}>
					{t("ops.checkin.scheduleHint")}
				</p>
				<label className="check" style={{ display: "flex", gap: 8, alignItems: "center" }}>
					<input
						type="checkbox"
						disabled={saveSchedule.isPending || scheduleDraft == null}
						checked={schedule.preset !== "off"}
						onChange={(e) => {
							if (!scheduleDraft) return;
							setScheduleDraft(
								e.target.checked
									? { preset: "daily", cron: "0 8 * * *" }
									: { preset: "off", cron: scheduleDraft.cron },
							);
						}}
					/>
					<span>{t("ops.checkin.scheduleEnabled")}</span>
				</label>
				{schedule.preset !== "off" ? (
					<div className="form-grid" style={{ marginTop: 12 }}>
						<label className="field">
							<span>{t("ops.checkin.schedulePreset")}</span>
							<select
								disabled={saveSchedule.isPending}
								value={schedule.preset}
								onChange={(e) => {
									const preset = e.target.value as SchedulePresetId;
									if (!scheduleDraft) return;
									const known = SCHEDULE_PRESETS.find(
										(item) => item.id === preset,
									);
									setScheduleDraft({
										preset,
										cron:
											preset === "custom"
												? scheduleDraft.cron
												: (known?.cron ?? scheduleDraft.cron),
									});
								}}
							>
								{SCHEDULE_PRESETS.filter((item) => item.id !== "off").map((item) => (
									<option key={item.id} value={item.id}>
										{t(`ops.schedule.preset.${item.id}`)}
									</option>
								))}
							</select>
						</label>
						{schedule.preset === "custom" ? (
							<label className="field">
								<span>{t("ops.checkin.scheduleCron")}</span>
								<input
									className="mono"
									disabled={saveSchedule.isPending}
									value={schedule.cron}
									onChange={(e) =>
										setScheduleDraft({ preset: "custom", cron: e.target.value })
									}
									placeholder="0 8 * * *"
								/>
							</label>
						) : (
							<div className="field">
								<span>{t("ops.checkin.scheduleCron")}</span>
								<input className="mono" disabled value={schedule.cron} readOnly />
							</div>
						)}
					</div>
				) : null}
				<div style={{ marginTop: 4 }}>
					<Button
						variant="secondary"
						disabled={!scheduleDirty || saveSchedule.isPending || scheduleDraft == null}
						onClick={() =>
							saveSchedule.mutate(settingsFromSchedule(scheduleDraft!))
						}
					>
						{saveSchedule.isPending ? t("common.working") : t("ops.checkin.scheduleSave")}
					</Button>
				</div>
			</Panel>

			<Panel
			actions={
				<>
					<select
						aria-label={t("ops.statusFilter")}
						value={status}
						onChange={(e) => setStatus(e.target.value)}
					>
						<option value="">{t("ops.allStatuses")}</option>
						<option value="success">success</option>
						<option value="failed">failed</option>
						<option value="skipped">skipped</option>
					</select>
					<Button
						icon={<Play size={16} />}
						disabled={run.isPending || !checkinEnabled}
						onClick={() => setConfirmRun(true)}
					>
						{run.isPending ? t("ops.running") : t("ops.runEnabled")}
					</Button>
				</>
			}
		>
			<p className="exchange-panel-note" style={{ marginBottom: 12 }}>
				{t("ops.checkinHint")}
			</p>
			{run.error && <ErrorState error={run.error} />}
			{run.data && (
				<div className="result-strip">
					<StatusBadge value={run.data.failure_count > 0 ? "failed" : "success"} />
					<span>
						{t("ops.checkinSummary", {
							success: run.data.success_count,
							failure: run.data.failure_count,
							skipped: run.data.skipped_count,
						})}
					</span>
				</div>
			)}
			{logs.isPending ? (
				<Loading />
			) : logs.isError ? (
				<ErrorState error={logs.error} />
			) : (
				<>
					<DataTable
						headers={[
							t("common.site"),
							t("common.status"),
							t("common.source"),
							t("ops.checkinDetail"),
							t("common.reward"),
							t("common.latency"),
							t("common.time"),
						]}
						empty={!checkinRows.length}
					>
						{checkinPagination.pageItems.map((l) => (
							<tr key={l.id}>
								<td>
									{(() => {
										const display = siteDisplayName(l.site_id, sitesById, t);
										return (
											<>
												<strong title={display.subtitle || undefined}>
													{display.title}
												</strong>
												<small>
													{display.subtitle
														? `${display.subtitle} · ${t("common.credentialId", { id: l.credential_id })}`
														: t("common.credentialId", { id: l.credential_id })}
												</small>
											</>
										);
									})()}
								</td>
								<td>
									<StatusBadge value={l.status} />
								</td>
								<td>{l.source}</td>
								<td>
									<span title={l.category}>{checkinDetailText(l, t)}</span>
								</td>
								<td>{l.reward || "-"}</td>
								<td>{t("common.ms", { n: l.latency_ms })}</td>
								<td>{formatDate(l.ran_at)}</td>
							</tr>
						))}
					</DataTable>
					<PaginationBar
						page={checkinPagination.page}
						totalPages={checkinPagination.totalPages}
						total={checkinPagination.total}
						pageSize={checkinPagination.pageSize}
						rangeStart={checkinPagination.rangeStart}
						rangeEnd={checkinPagination.rangeEnd}
						hasPrev={checkinPagination.hasPrev}
						hasNext={checkinPagination.hasNext}
						onPageChange={checkinPagination.setPage}
						onPageSizeChange={checkinPagination.setPageSize}
					/>
				</>
			)}
			{confirmRun ? (
				<ConfirmDialog
					title={t("ops.runEnabled")}
					message={t("ops.runEnabledConfirm")}
					confirmLabel={t("ops.runEnabled")}
					pending={run.isPending}
					error={run.error}
					onClose={() => setConfirmRun(false)}
					onConfirm={() => run.mutate()}
				/>
			) : null}
			</Panel>
		</>
	);
}

export function AuditPanel() {
	const { client } = useSession();
	const { t } = useI18n();
	const s = api(client!);
	const qc = useQueryClient();
	const [before, setBefore] = useState<number | undefined>();
	const [confirm, setConfirm] = useState(false);
	const q = useQuery({
		queryKey: ["audit", before],
		queryFn: ({ signal }) => s.auditEvents(before, signal),
	});
	const cleanup = useMutation({
		mutationFn: s.cleanupAudit,
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["audit"] });
			setConfirm(false);
		},
	});
	const auditRows = q.data ?? [];
	const auditPagination = useClientPagination(auditRows, 15);
	return (
		<Panel
			actions={
				<Button variant="secondary" icon={<ShieldCheck size={16} />} onClick={() => setConfirm(true)}>
					{t("ops.applyRetention")}
				</Button>
			}
		>
			{q.isPending ? (
				<Loading />
			) : q.isError ? (
				<ErrorState error={q.error} />
			) : (
				<>
					<DataTable
						headers={[
							t("common.action"),
							t("common.actor"),
							t("common.resource"),
							t("common.outcome"),
							t("common.status"),
							t("common.category"),
							t("common.time"),
						]}
						empty={!auditRows.length}
					>
						{auditPagination.pageItems.map((e) => (
							<tr key={e.id}>
								<td>
									<strong>{e.action}</strong>
									<small>#{e.id}</small>
								</td>
								<td>{e.actor_kind}</td>
								<td>
									{e.resource_kind || "-"}
									{e.resource_id ? ` #${e.resource_id}` : ""}
								</td>
								<td>
									<StatusBadge value={e.outcome} />
								</td>
								<td>{e.status_code}</td>
								<td>{e.category || "-"}</td>
								<td>{formatDate(e.created_at)}</td>
							</tr>
						))}
					</DataTable>
					<PaginationBar
						page={auditPagination.page}
						totalPages={auditPagination.totalPages}
						total={auditPagination.total}
						pageSize={auditPagination.pageSize}
						rangeStart={auditPagination.rangeStart}
						rangeEnd={auditPagination.rangeEnd}
						hasPrev={auditPagination.hasPrev}
						hasNext={auditPagination.hasNext}
						onPageChange={auditPagination.setPage}
						onPageSizeChange={auditPagination.setPageSize}
					/>
					{q.data.length === 100 && (
						<Button variant="secondary" onClick={() => setBefore(q.data.at(-1)?.id)}>
							{t("ops.olderEvents")}
						</Button>
					)}
					{before && (
						<Button variant="quiet" onClick={() => setBefore(undefined)}>
							{t("ops.newestEvents")}
						</Button>
					)}
				</>
			)}
			{confirm && (
				<ConfirmDialog
					title={t("ops.applyRetentionTitle")}
					message={t("ops.applyRetentionMsg")}
					confirmLabel={t("ops.runCleanup")}
					pending={cleanup.isPending}
					error={cleanup.error}
					onClose={() => setConfirm(false)}
					onConfirm={() => cleanup.mutate()}
				/>
			)}
		</Panel>
	);
}

export function BackupsPanel() {
	const { client } = useSession();
	const { t } = useI18n();
	const s = api(client!);
	const qc = useQueryClient();
	const q = useQuery({
		queryKey: ["backups"],
		queryFn: ({ signal }) => s.backups(signal),
	});
	const create = useMutation({
		mutationFn: s.createBackup,
		onSuccess: () => qc.invalidateQueries({ queryKey: ["backups"] }),
	});
	const backupRows = q.data ?? [];
	const backupPagination = useClientPagination(backupRows, 12);
	return (
		<Panel
			actions={
				<Button
					icon={<DatabaseBackup size={16} />}
					disabled={create.isPending}
					onClick={() => create.mutate()}
				>
					{create.isPending ? t("ops.creatingBackup") : t("ops.createBackup")}
				</Button>
			}
		>
			{create.error && <ErrorState error={create.error} />}
			{create.data && (
				<div className="result-strip">
					<StatusBadge value={create.data.status} />
					<span>
						{t("ops.backupCreated", {
							name: create.data.name,
							size: formatBytes(create.data.size_bytes),
							time: formatDate(create.data.created_at),
						})}
					</span>
				</div>
			)}
			{q.isPending ? (
				<Loading />
			) : q.isError ? (
				<ErrorState error={q.error} />
			) : !q.data.length ? (
				<Empty>{t("ops.noBackups")}</Empty>
			) : (
				<>
					<DataTable
						headers={[
							t("common.name"),
							t("common.status"),
							t("common.size"),
							t("common.duration"),
							t("common.checksum"),
							t("common.created"),
						]}
					>
						{backupPagination.pageItems.map((b) => (
							<tr key={b.id}>
								<td>
									<strong>{b.name}</strong>
								</td>
								<td>
									<StatusBadge value={b.status} />
								</td>
								<td>{formatBytes(b.size_bytes)}</td>
								<td>{t("common.ms", { n: b.duration_ms })}</td>
								<td className="mono truncate">{b.checksum}</td>
								<td>{formatDate(b.created_at)}</td>
							</tr>
						))}
					</DataTable>
					<PaginationBar
						page={backupPagination.page}
						totalPages={backupPagination.totalPages}
						total={backupPagination.total}
						pageSize={backupPagination.pageSize}
						rangeStart={backupPagination.rangeStart}
						rangeEnd={backupPagination.rangeEnd}
						hasPrev={backupPagination.hasPrev}
						hasNext={backupPagination.hasNext}
						onPageChange={backupPagination.setPage}
						onPageSizeChange={backupPagination.setPageSize}
					/>
				</>
			)}
			<p className="muted" style={{ marginTop: 12, fontSize: 12 }}>
				{t("ops.restoreNote", { cmd: "meta-gateway restore --from <backup-name>" })}
			</p>
		</Panel>
	);
}

function numberOr(value: string, fallback: number) {
	const parsed = Number(value);
	return Number.isFinite(parsed) ? parsed : fallback;
}

/** Admin-writable runtime parameters with hot reload. */
export function RuntimeSettingsPanel() {
	const { client } = useSession();
	const { t } = useI18n();
	const s = api(client!);
	const query = useQuery({
		queryKey: ["runtime-settings"],
		queryFn: ({ signal }) => s.runtimeSettings(signal),
	});
	const [draft, setDraft] = useState<RuntimeEditableSettings | null>(null);

	useEffect(() => {
		if (query.data?.editable) {
			setDraft({ ...query.data.editable });
		}
	}, [query.data]);

	const save = useAdminMutation({
		mutationFn: (body: RuntimeEditableSettings) => s.updateRuntimeSettings(body),
		invalidateKeys: [["runtime-settings"]],
	});
	const reset = useAdminMutation({
		mutationFn: () => s.resetRuntimeSettings(),
		invalidateKeys: [["runtime-settings"]],
	});

	if (query.isPending || !draft) {
		return (
			<Panel>
				<Loading />
			</Panel>
		);
	}
	if (query.isError) {
		return (
			<Panel>
				<ErrorState error={query.error} />
			</Panel>
		);
	}
	const data = query.data!;
	const busy = save.isPending || reset.isPending;
	const patch = <K extends keyof RuntimeEditableSettings>(
		key: K,
		value: RuntimeEditableSettings[K],
	) => setDraft((prev) => (prev ? { ...prev, [key]: value } : prev));

	return (
		<div className="runtime-settings">
			<div className="system-banner runtime-settings-banner">
				<strong>{t("ops.runtime.writableTitle")}</strong>
				<p>{data.note || t("ops.runtime.writableBody")}</p>
				<small>
					{t("ops.runtime.source")}: {data.source}
					{data.updated_at ? ` · ${formatDate(data.updated_at)}` : ""}
				</small>
			</div>

			{save.error || reset.error ? (
				<ErrorState error={save.error ?? reset.error} />
			) : null}
			{save.isSuccess ? (
				<div className="result-strip">
					<StatusBadge value="success" />
					<span>{t("ops.runtime.saved")}</span>
				</div>
			) : null}

			<div className="runtime-settings-grid">
				<Panel>
					<div className="panel-header">
						<strong>{t("ops.runtime.section.relay")}</strong>
					</div>
					<label className="field">
						<span>{t("ops.runtime.retryTimes")}</span>
						<input
							type="number"
							min={0}
							max={100}
							disabled={busy}
							value={draft.retry_times}
							onChange={(e) =>
								patch("retry_times", numberOr(e.target.value, draft.retry_times))
							}
						/>
						<small>{t("ops.runtime.retryTimesHint")}</small>
					</label>
					<label className="field">
						<span>{t("ops.runtime.cooldown")}</span>
						<input
							type="number"
							min={0}
							max={86400}
							disabled={busy}
							value={draft.cooldown_seconds}
							onChange={(e) =>
								patch(
									"cooldown_seconds",
									numberOr(e.target.value, draft.cooldown_seconds),
									)
								}
							/>
						<small>{t("ops.runtime.cooldownHint")}</small>
					</label>
				</Panel>

				<Panel>
					<div className="panel-header">
						<strong>{t("ops.runtime.section.checkin")}</strong>
					</div>
					<label className="check" style={{ display: "flex", gap: 8, alignItems: "center" }}>
						<input
							type="checkbox"
							disabled={busy}
							checked={draft.checkin_enabled}
							onChange={(e) => patch("checkin_enabled", e.target.checked)}
						/>
						<span>{t("ops.runtime.checkinEnabled")}</span>
					</label>
					<small className="muted">{t("ops.runtime.checkinEnabledHint")}</small>
					<label className="field" style={{ marginTop: 10 }}>
						<span>{t("ops.runtime.checkinCron")}</span>
						<input
							disabled={busy}
							value={draft.checkin_cron}
							onChange={(e) => patch("checkin_cron", e.target.value)}
							placeholder="0 8 * * *"
						/>
					</label>
				</Panel>

				<Panel>
					<div className="panel-header">
						<strong>{t("ops.runtime.section.limits")}</strong>
					</div>
					<div className="field">
						<span>{t("ops.runtime.relayRate")}</span>
						<div className="runtime-inline-fields">
							<label className="runtime-inline-field">
								<span>{t("ops.runtime.ratePerMinute")}</span>
								<input
									type="number"
									min={0}
									disabled={busy}
									value={draft.relay_rate_per_minute}
									onChange={(e) =>
										patch(
											"relay_rate_per_minute",
											numberOr(e.target.value, draft.relay_rate_per_minute),
										)
									}
								/>
							</label>
							<label className="runtime-inline-field">
								<span>{t("ops.runtime.rateBurst")}</span>
								<input
									type="number"
									min={0}
									disabled={busy}
									value={draft.relay_rate_burst}
									onChange={(e) =>
										patch(
											"relay_rate_burst",
											numberOr(e.target.value, draft.relay_rate_burst),
										)
									}
								/>
							</label>
						</div>
					</div>
					<div className="field">
						<span>{t("ops.runtime.adminRate")}</span>
						<div className="runtime-inline-fields">
							<label className="runtime-inline-field">
								<span>{t("ops.runtime.ratePerMinute")}</span>
								<input
									type="number"
									min={0}
									disabled={busy}
									value={draft.admin_rate_per_minute}
									onChange={(e) =>
										patch(
											"admin_rate_per_minute",
											numberOr(e.target.value, draft.admin_rate_per_minute),
										)
									}
								/>
							</label>
							<label className="runtime-inline-field">
								<span>{t("ops.runtime.rateBurst")}</span>
								<input
									type="number"
									min={0}
									disabled={busy}
									value={draft.admin_rate_burst}
									onChange={(e) =>
										patch(
											"admin_rate_burst",
											numberOr(e.target.value, draft.admin_rate_burst),
										)
									}
								/>
							</label>
						</div>
					</div>
				</Panel>

				<Panel>
					<div className="panel-header">
						<strong>{t("ops.runtime.section.audit")}</strong>
					</div>
					<label className="field">
						<span>{t("ops.runtime.auditDays")}</span>
						<input
							type="number"
							min={0}
							disabled={busy}
							value={draft.audit_retention_days}
							onChange={(e) =>
								patch(
									"audit_retention_days",
									numberOr(e.target.value, draft.audit_retention_days),
								)
							}
						/>
					</label>
					<label className="field">
						<span>{t("ops.runtime.auditRows")}</span>
						<input
							type="number"
							min={0}
							disabled={busy}
							value={draft.audit_retention_rows}
							onChange={(e) =>
								patch(
									"audit_retention_rows",
									numberOr(e.target.value, draft.audit_retention_rows),
								)
							}
						/>
					</label>
				</Panel>
				<Panel>
					<div className="panel-header">
						<strong>{t("ops.runtime.section.routing")}</strong>
					</div>
					<label className="field">
						<span>{t("ops.runtime.autoDisable")}</span>
						<input
							type="number"
							min={0}
							disabled={busy}
							value={draft.channel_auto_disable_threshold}
							onChange={(e) =>
								patch(
									"channel_auto_disable_threshold",
									numberOr(e.target.value, draft.channel_auto_disable_threshold),
								)
							}
						/>
						<small>{t("ops.runtime.autoDisableHint")}</small>
					</label>
					<label className="check" style={{ display: "flex", gap: 8, alignItems: "center" }}>
						<input
							type="checkbox"
							disabled={busy}
							checked={draft.routing_latency_aware}
							onChange={(e) => patch("routing_latency_aware", e.target.checked)}
						/>
						<span>{t("ops.runtime.latencyAware")}</span>
					</label>
					<small className="muted">{t("ops.runtime.latencyAwareHint")}</small>
				</Panel>



				<Panel>
					<div className="panel-header">
						<strong>{t("ops.runtime.section.server")}</strong>
					</div>
					<p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
						{t("ops.runtime.serverReadonly")}
					</p>
					<div className="runtime-setting-row">
						<span className="runtime-setting-label">{t("ops.runtime.httpAddr")}</span>
						<strong className="runtime-setting-value mono">
							{data.server_http_addr}
						</strong>
					</div>
					<div className="runtime-setting-row">
						<span className="runtime-setting-label">{t("ops.runtime.dataDir")}</span>
						<strong className="runtime-setting-value mono">{data.data_dir}</strong>
					</div>
				</Panel>
			</div>

			<div className="runtime-settings-actions">
				<Button
					disabled={busy}
					onClick={() => {
						save.reset();
						save.mutate(draft);
					}}
				>
					{save.isPending ? t("common.working") : t("ops.runtime.save")}
				</Button>
				<Button
					variant="secondary"
					disabled={busy || !data.has_override}
					onClick={() => {
						reset.reset();
						reset.mutate();
					}}
				>
					{reset.isPending ? t("common.working") : t("ops.runtime.resetEnv")}
				</Button>
			</div>
		</div>
	);
}
