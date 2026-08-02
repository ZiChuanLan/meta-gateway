import { Copy, KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { CreatedDownstreamKey, DownstreamKey } from "../api/types";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { ModelPicker } from "../components/ModelPicker";
import { ScopePicker } from "../components/ScopePicker";
import { PaginationBar } from "../components/PaginationBar";
import { EntityState } from "../components/EntityState";
import { StatGrid } from "../components/StatGrid";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import {
	Button,
	ConfirmDialog,
	DataTable,
	Dialog,
	ErrorState,
	Field,
	IconButton,
	Page,
	Panel,
	StatusBadge,
	formatDate,
} from "../components/ui";

function formatTokens(value?: number) {
	if (value == null || Number.isNaN(value)) return "0";
	return new Intl.NumberFormat().format(value);
}

function formatQuota(used?: number, total?: number) {
	const usedLabel = formatTokens(used ?? 0);
	if (!total || total <= 0) return `${usedLabel} / ∞`;
	return `${usedLabel} / ${formatTokens(total)}`;
}

function formatCost(value?: number) {
	if (value == null || value <= 0) return "—";
	return value.toFixed(4);
}

export function Keys() {
	const { client } = useSession();
	const { t } = useI18n();
	const [searchParams, setSearchParams] = useSearchParams();
	const service = api(client!);
	const query = useQuery({
		queryKey: ["keys"],
		queryFn: ({ signal }) => service.keys(signal),
	});
	const discovered = useQuery({
		queryKey: ["discovered-models"],
		queryFn: ({ signal }) => service.discoveredModels(undefined, signal),
	});
	const allModels = useMemo(() => {
		const seen = new Set<string>();
		const out: string[] = [];
		for (const model of discovered.data ?? []) {
			const name = model.model_name.trim();
			if (!name || seen.has(name)) continue;
			seen.add(name);
			out.push(name);
		}
		return out.sort((a, b) => a.localeCompare(b));
	}, [discovered.data]);
	const usage = useQuery({
		queryKey: ["usage-summary"],
		queryFn: ({ signal }) => service.usageSummary(undefined, signal),
	});
	const [add, setAdd] = useState(false);
	const [edit, setEdit] = useState<DownstreamKey | null>(null);
	const [created, setCreated] = useState<CreatedDownstreamKey | null>(null);
	const [remove, setRemove] = useState<number | null>(null);
	const openedCreateFromQuery = useRef(false);

	useEffect(() => {
		if (openedCreateFromQuery.current) return;
		if (searchParams.get("create") !== "1") return;
		openedCreateFromQuery.current = true;
		setAdd(true);
		const next = new URLSearchParams(searchParams);
		next.delete("create");
		setSearchParams(next, { replace: true });
	}, [searchParams, setSearchParams]);

	const create = useAdminMutation({
		mutationFn: (v: {
			name: string;
			scopes?: string;
			token?: string;
			quota_total_tokens?: number;
			price_prompt_per_1k?: number;
			price_completion_per_1k?: number;
			model_allowlist?: string;
			model_denylist?: string;
		}) => service.createKey(v),
		invalidateKeys: [["keys"], ["usage-summary"]],
		onSuccess: (result) => {
			setCreated(result);
			setAdd(false);
		},
	});
	const update = useAdminMutation({
		mutationFn: (v: {
			id: number;
			body: {
				name?: string;
				enabled?: boolean;
				scopes?: string;
				quota_total_tokens?: number;
				price_prompt_per_1k?: number;
				price_completion_per_1k?: number;
				model_allowlist?: string;
				model_denylist?: string;
				reset_used?: boolean;
			};
		}) => service.updateKey(v.id, v.body),
		invalidateKeys: [["keys"], ["usage-summary"]],
		onSuccess: () => setEdit(null),
	});
	const del = useAdminMutation({
		mutationFn: (id: number) => service.deleteKey(id),
		invalidateKeys: [["keys"], ["usage-summary"]],
		pendingIdOf: (id) => id,
		onSuccess: () => setRemove(null),
	});

	const rows = useMemo(() => query.data ?? [], [query.data]);
	const pagination = useClientPagination(rows, 12);
	const pageRows = pagination.pageItems;
	const enabledCount = useMemo(
		() => rows.filter((key) => key.enabled).length,
		[rows],
	);
	const totalUsed = useMemo(
		() => rows.reduce((sum, key) => sum + (key.quota_used_tokens ?? 0), 0),
		[rows],
	);

	const openCreate = () => {
		create.reset();
		setAdd(true);
	};

	return (
		<Page
			kicker={t("keys.kicker")}
			title={t("keys.title")}
			description={t("keys.description")}
			actions={
				<Button icon={<Plus size={16} />} onClick={openCreate}>
					{t("keys.create")}
				</Button>
			}
		>
			<div className="ops-canvas">
				<StatGrid
					items={[
						{
							label: t("keys.stat.total"),
							value: query.isPending ? "—" : rows.length,
						},
						{
							label: t("keys.stat.enabled"),
							value: query.isPending ? "—" : enabledCount,
						},
						{
							label: t("keys.stat.usedTokens"),
							value: query.isPending
								? "—"
								: formatTokens(usage.data?.total_tokens ?? totalUsed),
						},
						{
							label: t("keys.stat.requests"),
							value: usage.isPending ? "—" : formatTokens(usage.data?.request_count ?? 0),
						},
					]}
				/>

				<Panel className="ops-list-panel">
					<EntityState
						isLoading={query.isPending}
						isError={query.isError}
						error={query.error}
						isEmpty={!rows.length}
						empty={
							<EmptyHero
								kicker={t("keys.emptyKicker")}
								title={t("keys.emptyTitle")}
								body={t("keys.empty")}
								actions={
									<>
										<Button icon={<Plus size={16} />} onClick={openCreate}>
											{t("keys.create")}
										</Button>
										<Link className="button button-secondary" to="/">
											{t("keys.ctaConnections")}
										</Link>
									</>
								}
							/>
						}
						retry={() => query.refetch()}
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
									t("common.name"),
									t("keys.accessCol"),
									t("keys.quotaCol"),
									t("keys.costCol"),
									t("common.status"),
									t("common.created"),
									t("common.actions"),
								]}
							>
								{pageRows.map((k) => (
									<tr key={k.id}>
										<td>
											<strong>{k.name}</strong>
											<small>#{k.id}</small>
										</td>
										<td>{k.scopes?.trim() || "relay"}</td>
										<td>
											<code>{formatQuota(k.quota_used_tokens, k.quota_total_tokens)}</code>
										</td>
										<td>{formatCost(k.estimated_cost)}</td>
										<td>
											<StatusBadge value={k.enabled} />
										</td>
										<td>{formatDate(k.created_at)}</td>
										<td className="actions">
											<IconButton
												label={t("keys.edit")}
												onClick={() => {
													update.reset();
													setEdit(k);
												}}
											>
												<Pencil />
											</IconButton>
											<IconButton
												label={t("keys.delete")}
												disabled={del.pendingId === k.id}
												onClick={() => setRemove(k.id)}
											>
												<Trash2 />
											</IconButton>
										</td>
									</tr>
								))}
							</DataTable>
						</ListShell>
					</EntityState>
				</Panel>
			</div>

			{add && (
				<KeyDialog
					mode="create"
					pending={create.isPending}
					error={create.error}
					onClose={() => setAdd(false)}
					onSave={(v) => create.mutate(v)}
					allModels={allModels}
				/>
			)}
			{edit && (
				<KeyDialog
					mode="edit"
					initial={edit}
					pending={update.isPending}
					error={update.error}
					onClose={() => setEdit(null)}
					onSave={(v) =>
						update.mutate({
							id: edit.id,
							body: {
								name: v.name,
								scopes: v.scopes,
								quota_total_tokens: v.quota_total_tokens,
								price_prompt_per_1k: v.price_prompt_per_1k,
								price_completion_per_1k: v.price_completion_per_1k,
								model_allowlist: v.model_allowlist,
								model_denylist: v.model_denylist,
								reset_used: v.reset_used,
							},
						})
					}
					allModels={allModels}
				/>
			)}
			{created && (
				<Dialog
					title={t("keys.copyTitle")}
					onClose={() => setCreated(null)}
					actions={
						<Button onClick={() => setCreated(null)}>{t("keys.stored")}</Button>
					}
				>
					<p className="warning">{t("keys.copyWarning")}</p>
					<div className="secret-output">
						<code>{created.token}</code>
						<IconButton
							label={t("keys.copyToken")}
							onClick={() => navigator.clipboard.writeText(created.token)}
						>
							<Copy />
						</IconButton>
					</div>
				</Dialog>
			)}
			{remove && (
				<ConfirmDialog
					title={t("keys.revoke")}
					message={t("keys.revokeMsg")}
					confirmLabel={t("keys.revokeConfirm")}
					pending={del.isPending}
					error={del.error}
					onClose={() => setRemove(null)}
					onConfirm={() => del.mutate(remove)}
				/>
			)}
		</Page>
	);
}

type KeyFormValues = {
	name: string;
	scopes?: string;
	token?: string;
	quota_total_tokens?: number;
	price_prompt_per_1k?: number;
	price_completion_per_1k?: number;
	model_allowlist?: string;
	model_denylist?: string;
	expires_at?: string;
	allowed_ips?: string;
	reset_used?: boolean;
};

function KeyDialog({
	mode,
	initial,
	pending,
	error,
	onClose,
	onSave,
	allModels,
}: {
	mode: "create" | "edit";
	initial?: DownstreamKey;
	pending: boolean;
	error: unknown;
	onClose: () => void;
	onSave: (v: KeyFormValues) => void;
	allModels: string[];
}) {
	const { t } = useI18n();
	const [name, setName] = useState(initial?.name ?? "");
	const [customToken, setCustomToken] = useState("");
	const [useCustomToken, setUseCustomToken] = useState(false);
	const [scopes, setScopes] = useState<string[]>(() =>
		(initial?.scopes?.trim() || "relay")
			.split(/[,;|\s]+/)
			.map((entry) => entry.trim())
			.filter(Boolean),
	);
	const [quotaTotal, setQuotaTotal] = useState(
		String(initial?.quota_total_tokens && initial.quota_total_tokens > 0 ? initial.quota_total_tokens : ""),
	);
	const [pricePrompt, setPricePrompt] = useState(
		String(initial?.price_prompt_per_1k && initial.price_prompt_per_1k > 0 ? initial.price_prompt_per_1k : ""),
	);
	const [priceCompletion, setPriceCompletion] = useState(
		String(
			initial?.price_completion_per_1k && initial.price_completion_per_1k > 0
				? initial.price_completion_per_1k
				: "",
		),
	);
	const splitModels = (raw?: string) =>
		(raw ?? "")
			.split(",")
			.map((entry) => entry.trim())
			.filter(Boolean);
	const [allowlist, setAllowlist] = useState<string[]>(() =>
		splitModels(initial?.model_allowlist),
	);
	const [denylist, setDenylist] = useState<string[]>(() =>
		splitModels(initial?.model_denylist),
	);
	const [expiresAt, setExpiresAt] = useState(initial?.expires_at ?? "");
	const [allowedIPs, setAllowedIPs] = useState(initial?.allowed_ips ?? "");
	const [resetUsed, setResetUsed] = useState(false);
	const trimmedCustom = customToken.trim();
	const customTooShort =
		useCustomToken && trimmedCustom.length > 0 && trimmedCustom.length < 16;
	const canSubmit =
		Boolean(name.trim()) &&
		(mode === "edit" || !useCustomToken || (trimmedCustom.length >= 16 && !customTooShort));

	const parseOptionalNumber = (raw: string) => {
		const trimmed = raw.trim();
		if (!trimmed) return 0;
		const value = Number(trimmed);
		return Number.isFinite(value) && value >= 0 ? value : 0;
	};

	return (
		<Dialog
			title={mode === "create" ? t("keys.createDialog") : t("keys.editDialog")}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button
						disabled={pending || !canSubmit}
						icon={<KeyRound size={16} />}
						onClick={() =>
							onSave({
								name: name.trim(),
								scopes: scopes.length > 0 ? scopes.join(",") : "relay",
								token: mode === "create" && useCustomToken ? trimmedCustom : undefined,
								quota_total_tokens: parseOptionalNumber(quotaTotal),
								price_prompt_per_1k: parseOptionalNumber(pricePrompt),
								price_completion_per_1k: parseOptionalNumber(priceCompletion),
								model_allowlist: allowlist.join(","),
								model_denylist: denylist.join(","),
								expires_at: expiresAt.trim() || undefined,
								allowed_ips: allowedIPs.trim() || undefined,
								reset_used: mode === "edit" ? resetUsed : undefined,
							})
						}
					>
						{mode === "create" ? t("common.create") : t("common.save")}
					</Button>
				</>
			}
		>
			<p className="channel-form-intro">
				{mode === "create" ? t("keys.createHint") : t("keys.editHint")}
			</p>
			<Field label={t("common.name")}>
				<input
					autoFocus
					value={name}
					onChange={(e) => setName(e.target.value)}
					placeholder={t("keys.namePlaceholder")}
				/>
			</Field>
			<Field
				label={t("common.scopes")}
				hint={t("keys.scopesHint")}
			>
				<ScopePicker value={scopes} onChange={setScopes} disabled={pending} />
			</Field>
			<Field label={t("keys.quotaTotal")} hint={t("keys.quotaTotalHint")}>
				<input
					type="number"
					min={0}
					step={1}
					value={quotaTotal}
					onChange={(e) => setQuotaTotal(e.target.value)}
					placeholder="0 = unlimited"
				/>
			</Field>
			<div className="split" style={{ gap: "0.75rem" }}>
				<Field label={t("keys.pricePrompt")} hint={t("keys.priceHint")}>
					<input
						type="number"
						min={0}
						step="0.0001"
						value={pricePrompt}
						onChange={(e) => setPricePrompt(e.target.value)}
						placeholder="0"
					/>
				</Field>
				<Field label={t("keys.priceCompletion")}>
					<input
						type="number"
						min={0}
						step="0.0001"
						value={priceCompletion}
						onChange={(e) => setPriceCompletion(e.target.value)}
						placeholder="0"
					/>
				</Field>
			</div>
			<Field
				label={t("keys.modelAllowlist")}
				hint={t("keys.modelAllowlistHint")}
			>
				<ModelPicker
					allModels={allModels}
					selected={allowlist}
					onChange={setAllowlist}
					placeholder={t("keys.modelPickerPlaceholder")}
					emptyLabel={t("keys.modelPickerEmpty")}
				/>
			</Field>
			<Field
				label={t("keys.modelDenylist")}
				hint={t("keys.modelDenylistHint")}
			>
				<ModelPicker
					allModels={allModels}
					selected={denylist}
					onChange={setDenylist}
					placeholder={t("keys.modelPickerPlaceholder")}
					emptyLabel={t("keys.modelPickerEmpty")}
				/>
			</Field>
			<div className="split" style={{ gap: "0.75rem" }}>
				<Field
					label={t("keys.expiresAt")}
					hint={t("keys.expiresAtHint")}
				>
					<input
						type="datetime-local"
						value={expiresAt ? toLocalInput(expiresAt) : ""}
						disabled={pending}
						onChange={(e) => setExpiresAt(e.target.value ? toRFC3339(e.target.value) : "")}
					/>
				</Field>
				<Field
					label={t("keys.allowedIPs")}
					hint={t("keys.allowedIPsHint")}
				>
					<textarea
						value={allowedIPs}
						disabled={pending}
						onChange={(e) => setAllowedIPs(e.target.value)}
						placeholder="1.2.3.4&#10;10.0.0.0/8"
						style={{ minHeight: 64 }}
					/>
				</Field>
			</div>
			{mode === "edit" ? (
				<label
					className="check"
					style={{ marginTop: "0.75rem", display: "flex", gap: "0.5rem", alignItems: "center" }}
				>
					<input
						type="checkbox"
						checked={resetUsed}
						disabled={pending}
						onChange={(event) => setResetUsed(event.target.checked)}
					/>
					<span>{t("keys.resetUsed")}</span>
				</label>
			) : (
				<>
					<label
						className="check"
						style={{ marginTop: "0.75rem", display: "flex", gap: "0.5rem", alignItems: "center" }}
					>
						<input
							type="checkbox"
							checked={useCustomToken}
							disabled={pending}
							aria-label={t("keys.useCustomToken")}
							onChange={(event) => {
								setUseCustomToken(event.target.checked);
								if (!event.target.checked) setCustomToken("");
							}}
						/>
						<span>{t("keys.useCustomToken")}</span>
					</label>
					{useCustomToken ? (
						<Field label={t("keys.customToken")} hint={t("keys.customTokenHint")}>
							<input
								type="password"
								autoComplete="new-password"
								aria-label={t("keys.customToken")}
								value={customToken}
								onChange={(e) => setCustomToken(e.target.value)}
								placeholder={t("keys.customTokenPlaceholder")}
								disabled={pending}
							/>
						</Field>
					) : (
						<p className="exchange-panel-note">{t("keys.autoTokenHint")}</p>
					)}
				</>
			)}
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}

/** Converts an RFC3339 string to a local datetime-local input value. */
function toLocalInput(iso: string): string {
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return "";
	const pad = (n: number) => String(n).padStart(2, "0");
	return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** Converts a datetime-local input value to RFC3339 (local time). */
function toRFC3339(local: string): string {
	const date = new Date(local);
	if (Number.isNaN(date.getTime())) return "";
	return date.toISOString();
}
