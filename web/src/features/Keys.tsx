import { Copy, KeyRound, Plus, Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { CreatedDownstreamKey } from "../api/types";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
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

export function Keys() {
	const { client } = useSession();
	const { t } = useI18n();
	const [searchParams, setSearchParams] = useSearchParams();
	const service = api(client!);
	const query = useQuery({
		queryKey: ["keys"],
		queryFn: ({ signal }) => service.keys(signal),
	});
	const [add, setAdd] = useState(false);
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
		mutationFn: (v: { name: string; scopes?: string; token?: string }) =>
			service.createKey(v),
		invalidateKeys: [["keys"]],
		onSuccess: (result) => {
			setCreated(result);
			setAdd(false);
		},
	});
	const del = useAdminMutation({
		mutationFn: (id: number) => service.deleteKey(id),
		invalidateKeys: [["keys"]],
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
							label: t("keys.stat.hint"),
							value: t("keys.stat.hintValue"),
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
									<td>{t("keys.accessRelay")}</td>
									<td>
										<StatusBadge value={k.enabled} />
									</td>
									<td>{formatDate(k.created_at)}</td>
									<td className="actions">
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
					pending={create.isPending}
					error={create.error}
					onClose={() => setAdd(false)}
					onSave={(v) => create.mutate(v)}
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

function KeyDialog({
	pending,
	error,
	onClose,
	onSave,
}: {
	pending: boolean;
	error: unknown;
	onClose: () => void;
	onSave: (v: { name: string; scopes?: string; token?: string }) => void;
}) {
	const { t } = useI18n();
	const [name, setName] = useState("");
	const [customToken, setCustomToken] = useState("");
	const [useCustomToken, setUseCustomToken] = useState(false);
	const trimmedCustom = customToken.trim();
	const customTooShort =
		useCustomToken && trimmedCustom.length > 0 && trimmedCustom.length < 16;
	const canSubmit =
		Boolean(name.trim()) &&
		(!useCustomToken || (trimmedCustom.length >= 16 && !customTooShort));
	return (
		<Dialog
			title={t("keys.createDialog")}
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
								// Scopes are reserved for future fine-grained access; always relay today.
								scopes: "relay",
								token: useCustomToken ? trimmedCustom : undefined,
							})
						}
					>
						{t("common.create")}
					</Button>
				</>
			}
		>
			<p className="channel-form-intro">{t("keys.createHint")}</p>
			<Field label={t("common.name")}>
				<input
					autoFocus
					value={name}
					onChange={(e) => setName(e.target.value)}
					placeholder={t("keys.namePlaceholder")}
				/>
			</Field>
			<label className="check" style={{ marginTop: "0.75rem", display: "flex", gap: "0.5rem", alignItems: "center" }}>
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
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}
