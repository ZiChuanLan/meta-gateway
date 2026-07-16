import { DatabaseBackup, Play, RefreshCw, ShieldCheck } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import {
	Button,
	ConfirmDialog,
	DataTable,
	Empty,
	ErrorState,
	Loading,
	Page,
	Panel,
	StatusBadge,
	Tabs,
	formatBytes,
	formatDate,
} from "../components/ui";

export function Operations() {
	const { t } = useI18n();
	const [tab, setTab] = useState("discovery");
	return (
		<Page title={t("ops.title")} description={t("ops.description")}>
			<Tabs
				items={[
					{ value: "discovery", label: t("ops.tab.discovery") },
					{ value: "checkins", label: t("ops.tab.checkins") },
					{ value: "proxy", label: t("ops.tab.proxy") },
					{ value: "audit", label: t("ops.tab.audit") },
					{ value: "backups", label: t("ops.tab.backups") },
				]}
				active={tab}
				onChange={setTab}
			/>
			{tab === "discovery" && <Discovery />}
			{tab === "checkins" && <Checkins />}
			{tab === "proxy" && <ProxyLogs />}
			{tab === "audit" && <Audit />}
			{tab === "backups" && <Backups />}
		</Page>
	);
}

function Discovery() {
	const { client } = useSession();
	const { t } = useI18n();
	const s = api(client!);
	const qc = useQueryClient();
	const channels = useQuery({
		queryKey: ["channels"],
		queryFn: ({ signal }) => s.channels(signal),
	});
	const [filter, setFilter] = useState(0);
	const models = useQuery({
		queryKey: ["models", filter],
		queryFn: ({ signal }) => s.discoveredModels(filter || undefined, signal),
	});
	const refresh = useMutation({
		mutationFn: s.refreshAll,
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: ["models"] });
			void qc.invalidateQueries({ queryKey: ["channels"] });
			void qc.invalidateQueries({ queryKey: ["routes"] });
		},
	});
	const refreshOne = useMutation({
		mutationFn: s.refreshChannel,
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: ["models"] });
			void qc.invalidateQueries({ queryKey: ["channels"] });
			void qc.invalidateQueries({ queryKey: ["routes"] });
		},
	});
	const failedChannelIds = new Set(
		(refresh.data?.items ?? [])
			.filter((item) => item.error)
			.map((item) => item.channel_id),
	);
	const refreshBusy = refresh.isPending || refreshOne.isPending;
	return (
		<Panel
			actions={
				<>
					<select
						aria-label={t("ops.filterChannel")}
						value={filter}
						onChange={(e) => setFilter(Number(e.target.value))}
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
							onClick={() => refreshOne.mutate(filter)}
						>
							{refreshOne.isPending
								? t("ops.refreshing")
								: t("ops.refreshChannel")}
						</Button>
					)}
					<Button
						icon={<RefreshCw size={16} />}
						disabled={refreshBusy}
						onClick={() => refresh.mutate()}
					>
						{refresh.isPending ? t("ops.refreshing") : t("ops.refreshAll")}
					</Button>
				</>
			}
		>
			{refresh.error && <ErrorState error={refresh.error} />}
			{refreshOne.error && <ErrorState error={refreshOne.error} />}
			{refresh.data && (
				<div className="result-strip">
					<StatusBadge
						value={
							refresh.data.failure_count > 0 ? "failed" : "success"
						}
					/>
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
				<DataTable
					headers={[
						t("common.model"),
						t("common.channel"),
						t("common.source"),
						t("common.available"),
						t("common.latency"),
						t("common.checked"),
					]}
					empty={!models.data.length}
				>
					{models.data.map((m) => (
						<tr
							key={m.id}
							className={
								failedChannelIds.has(m.channel_id) ? "row-failed" : undefined
							}
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
			)}
		</Panel>
	);
}

function Checkins() {
	const { client } = useSession();
	const { t } = useI18n();
	const s = api(client!);
	const qc = useQueryClient();
	const [status, setStatus] = useState("");
	const logs = useQuery({
		queryKey: ["checkin-logs", status],
		queryFn: ({ signal }) =>
			s.checkinLogs(`?limit=100${status ? `&status=${status}` : ""}`, signal),
	});
	const run = useMutation({
		mutationFn: s.runAllCheckins,
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: ["checkin-logs"] });
			void qc.invalidateQueries({ queryKey: ["credentials"] });
		},
	});
	return (
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
						disabled={run.isPending}
						onClick={() => run.mutate()}
					>
						{run.isPending ? t("ops.running") : t("ops.runEnabled")}
					</Button>
				</>
			}
		>
			{run.error && <ErrorState error={run.error} />}
			{run.data && (
				<div className="result-strip">
					<StatusBadge
						value={run.data.failure_count > 0 ? "failed" : "success"}
					/>
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
				<DataTable
					headers={[
						t("common.credentialCol"),
						t("common.status"),
						t("common.source"),
						t("common.category"),
						t("common.reward"),
						t("common.latency"),
						t("common.time"),
					]}
					empty={!logs.data.length}
				>
					{logs.data.map((l) => (
						<tr key={l.id}>
							<td>
								#{l.credential_id}
								<small>{t("common.siteId", { id: l.site_id })}</small>
							</td>
							<td>
								<StatusBadge value={l.status} />
							</td>
							<td>{l.source}</td>
							<td>{l.category}</td>
							<td>{l.reward || "-"}</td>
							<td>{t("common.ms", { n: l.latency_ms })}</td>
							<td>{formatDate(l.ran_at)}</td>
						</tr>
					))}
				</DataTable>
			)}
		</Panel>
	);
}

function ProxyLogs() {
	const { client } = useSession();
	const { t } = useI18n();
	const q = useQuery({
		queryKey: ["proxy-logs"],
		queryFn: ({ signal }) => api(client!).proxyLogs(signal),
	});
	return (
		<Panel>
			{q.isPending ? (
				<Loading />
			) : q.isError ? (
				<ErrorState error={q.error} />
			) : (
				<DataTable
					headers={[
						t("common.request"),
						t("common.model"),
						t("common.channel"),
						t("common.attemptCol"),
						t("common.status"),
						t("common.latency"),
						t("common.errorBrief"),
						t("common.time"),
					]}
					empty={!q.data.length}
				>
					{q.data.map((l) => (
						<tr key={l.id}>
							<td className="mono truncate">{l.request_id}</td>
							<td>
								<strong>{l.model}</strong>
							</td>
							<td>#{l.channel_id}</td>
							<td>{l.attempt}</td>
							<td>
								<StatusBadge value={String(l.status)} />
							</td>
							<td>{t("common.ms", { n: l.latency_ms })}</td>
							<td>{l.error_brief || "-"}</td>
							<td>{formatDate(l.created_at)}</td>
						</tr>
					))}
				</DataTable>
			)}
		</Panel>
	);
}

function Audit() {
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
	return (
		<Panel
			actions={
				<Button
					variant="secondary"
					icon={<ShieldCheck size={16} />}
					onClick={() => setConfirm(true)}
				>
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
						empty={!q.data.length}
					>
						{q.data.map((e) => (
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
					{q.data.length === 100 && (
						<Button
							variant="secondary"
							onClick={() => setBefore(q.data.at(-1)?.id)}
						>
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

function Backups() {
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
					{q.data.map((b) => (
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
			)}
			<p className="panel-note">
				{t("ops.restoreNote", {
					cmd: "meta-gateway restore --from <backup-name>",
				})
					.split(/(meta-gateway restore --from <backup-name>)/)
					.map((part, i) =>
						part.startsWith("meta-gateway") ? (
							<code key={i}>{part}</code>
						) : (
							part
						),
					)}
			</p>
		</Panel>
	);
}
