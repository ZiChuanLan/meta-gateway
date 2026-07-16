import { Pencil, Plus, Search, Trash2 } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Route, RouteMember } from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import {
	Button,
	ConfirmDialog,
	DataTable,
	Dialog,
	Empty,
	ErrorState,
	Field,
	IconButton,
	Loading,
	Page,
	Panel,
	StatusBadge,
	Tabs,
} from "../components/ui";

export function Routing() {
	const { t } = useI18n();
	const [tab, setTab] = useState("routes");
	return (
		<Page title={t("routing.title")} description={t("routing.description")}>
			<Tabs
				items={[
					{ value: "routes", label: t("routing.tab.routes") },
					{ value: "explain", label: t("routing.tab.explain") },
				]}
				active={tab}
				onChange={setTab}
			/>
			{tab === "routes" ? <RouteManager /> : <Explain />}
		</Page>
	);
}

function RouteManager() {
	const { client } = useSession();
	const { t } = useI18n();
	const s = api(client!);
	const qc = useQueryClient();
	const routes = useQuery({
		queryKey: ["routes"],
		queryFn: ({ signal }) => s.routes(signal),
	});
	const channels = useQuery({
		queryKey: ["channels"],
		queryFn: ({ signal }) => s.channels(signal),
	});
	const [selected, setSelected] = useState<number | null>(null);
	const [edit, setEdit] = useState<Partial<Route> | null>(null);
	const [remove, setRemove] = useState<Route | null>(null);
	const [member, setMember] = useState<Partial<RouteMember> | null>(null);
	const [removeMember, setRemoveMember] = useState<RouteMember | null>(null);
	useEffect(() => {
		if (!selected && routes.data?.[0]) setSelected(routes.data[0].id);
	}, [routes.data, selected]);
	const members = useQuery({
		queryKey: ["members", selected],
		queryFn: ({ signal }) => s.members(selected!, signal),
		enabled: !!selected,
	});
	const save = useMutation({
		mutationFn: (v: Partial<Route>) =>
			v.id ? s.updateRoute(v.id, v) : s.createRoute(v),
		onSuccess: (r) => {
			qc.invalidateQueries({ queryKey: ["routes"] });
			setSelected(r.id);
			setEdit(null);
		},
	});
	const del = useMutation({
		mutationFn: s.deleteRoute,
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["routes"] });
			setSelected(null);
			setRemove(null);
		},
	});
	const saveMember = useMutation({
		mutationFn: (v: Partial<RouteMember>) =>
			v.id ? s.updateMember(v.id, v) : s.createMember(selected!, v),
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["members", selected] });
			setMember(null);
		},
	});
	const delMember = useMutation({
		mutationFn: s.deleteMember,
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["members", selected] });
			setRemoveMember(null);
		},
	});
	return (
		<div className="master-detail">
			<Panel
				title={t("routing.routes")}
				actions={
					<IconButton
						label={t("routing.addRoute")}
						onClick={() => setEdit({ enabled: true })}
					>
						<Plus />
					</IconButton>
				}
			>
				{routes.isPending ? (
					<Loading />
				) : routes.isError ? (
					<ErrorState error={routes.error} />
				) : !routes.data.length ? (
					<Empty />
				) : (
					<div className="route-list">
						{routes.data.map((r) => (
							<button
								className={selected === r.id ? "active" : ""}
								key={r.id}
								onClick={() => setSelected(r.id)}
							>
								<div>
									<strong>{r.model_pattern}</strong>
									<span>{t("common.routeId", { id: r.id })}</span>
								</div>
								<StatusBadge value={r.enabled} />
							</button>
						))}
					</div>
				)}
			</Panel>
			<Panel
				title={
					selected
						? routes.data?.find((r) => r.id === selected)?.model_pattern
						: t("routing.routeDetails")
				}
				actions={
					selected && (
						<>
							<Button
								variant="secondary"
								icon={<Pencil size={15} />}
								onClick={() =>
									setEdit(routes.data?.find((r) => r.id === selected) ?? null)
								}
							>
								{t("common.edit")}
							</Button>
							<Button
								variant="quiet"
								icon={<Trash2 size={15} />}
								onClick={() =>
									setRemove(routes.data?.find((r) => r.id === selected) ?? null)
								}
							>
								{t("common.delete")}
							</Button>
							<Button
								icon={<Plus size={15} />}
								onClick={() =>
									setMember({
										enabled: true,
										auto: false,
										manual_override: true,
										priority: 0,
										weight: 100,
									})
								}
							>
								{t("routing.addMember")}
							</Button>
						</>
					)
				}
			>
				{!selected ? (
					<Empty>{t("routing.selectRoute")}</Empty>
				) : members.isPending ? (
					<Loading />
				) : members.isError ? (
					<ErrorState error={members.error} />
				) : (
					<DataTable
						headers={[
							t("common.channel"),
							t("common.priority"),
							t("common.weight"),
							t("common.ownership"),
							t("common.health"),
							t("common.actions"),
						]}
						empty={!members.data?.length}
					>
						{members.data?.map((m) => (
							<tr key={m.id}>
								<td>
									<strong>
										{channels.data?.find((c) => c.id === m.channel_id)?.name ??
											`#${m.channel_id}`}
									</strong>
								</td>
								<td>{m.priority}</td>
								<td>{m.weight}</td>
								<td>
									<StatusBadge
										value={
											m.manual_override
												? "manual_override"
												: m.auto
													? "automatic"
													: "manual"
										}
									/>
								</td>
								<td>
									{m.cooldown_until ? (
										<StatusBadge value="cooling_down" />
									) : (
										<StatusBadge value={m.enabled} />
									)}
									<small>
										{m.fail_count
											? t("common.failures", { n: m.fail_count })
											: ""}
									</small>
								</td>
								<td className="actions">
									<IconButton
										label={t("routing.editMember")}
										onClick={() => setMember(m)}
									>
										<Pencil />
									</IconButton>
									<IconButton
										label={t("routing.deleteMember")}
										onClick={() => setRemoveMember(m)}
									>
										<Trash2 />
									</IconButton>
								</td>
							</tr>
						))}
					</DataTable>
				)}
			</Panel>
			{edit && (
				<RouteDialog
					value={edit}
					pending={save.isPending}
					error={save.error}
					onClose={() => setEdit(null)}
					onSave={(v) => save.mutate(v)}
				/>
			)}
			{remove && (
				<ConfirmDialog
					title={t("routing.deleteRoute")}
					message={t("routing.deleteRouteMsg", { name: remove.model_pattern })}
					pending={del.isPending}
					error={del.error}
					onClose={() => setRemove(null)}
					onConfirm={() => del.mutate(remove.id)}
				/>
			)}
			{member && (
				<MemberDialog
					value={member}
					channels={channels.data ?? []}
					pending={saveMember.isPending}
					error={saveMember.error}
					onClose={() => setMember(null)}
					onSave={(v) => saveMember.mutate(v)}
				/>
			)}
			{removeMember && (
				<ConfirmDialog
					title={t("routing.deleteMember")}
					message={t("routing.deleteMemberMsg", {
						id: removeMember.channel_id,
					})}
					pending={delMember.isPending}
					error={delMember.error}
					onClose={() => setRemoveMember(null)}
					onConfirm={() => delMember.mutate(removeMember.id)}
				/>
			)}
		</div>
	);
}

function RouteDialog({
	value,
	pending,
	error,
	onClose,
	onSave,
}: {
	value: Partial<Route>;
	pending: boolean;
	error: unknown;
	onClose: () => void;
	onSave: (v: Partial<Route>) => void;
}) {
	const { t } = useI18n();
	const [f, setF] = useState(value);
	return (
		<Dialog
			title={value.id ? t("routing.editRoute") : t("routing.addRoute")}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button
						disabled={pending || !f.model_pattern}
						onClick={() => onSave(f)}
					>
						{t("common.save")}
					</Button>
				</>
			}
		>
			<Field label={t("routing.exactModel")}>
				<input
					value={f.model_pattern ?? ""}
					onChange={(e) => setF({ ...f, model_pattern: e.target.value })}
				/>
			</Field>
			<Field label={t("common.notes")}>
				<textarea
					value={f.notes ?? ""}
					onChange={(e) => setF({ ...f, notes: e.target.value })}
				/>
			</Field>
			<label className="check">
				<input
					type="checkbox"
					checked={f.enabled ?? false}
					onChange={(e) => setF({ ...f, enabled: e.target.checked })}
				/>
				<span>{t("routing.routeEnabled")}</span>
			</label>
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}

function MemberDialog({
	value,
	channels,
	pending,
	error,
	onClose,
	onSave,
}: {
	value: Partial<RouteMember>;
	channels: Array<{ id: number; name: string }>;
	pending: boolean;
	error: unknown;
	onClose: () => void;
	onSave: (v: Partial<RouteMember>) => void;
}) {
	const { t } = useI18n();
	const [f, setF] = useState(value);
	return (
		<Dialog
			title={value.id ? t("routing.editMember") : t("routing.addMember")}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button disabled={pending || !f.channel_id} onClick={() => onSave(f)}>
						{t("common.save")}
					</Button>
				</>
			}
		>
			<div className="form-grid">
				<Field label={t("common.channel")}>
					<select
						value={f.channel_id ?? ""}
						onChange={(e) => setF({ ...f, channel_id: Number(e.target.value) })}
					>
						<option value="">{t("common.select")}</option>
						{channels.map((c) => (
							<option key={c.id} value={c.id}>
								{c.name}
							</option>
						))}
					</select>
				</Field>
				<Field label={t("common.priority")}>
					<input
						type="number"
						value={f.priority ?? 0}
						onChange={(e) => setF({ ...f, priority: Number(e.target.value) })}
					/>
				</Field>
				<Field label={t("common.weight")}>
					<input
						type="number"
						min="0"
						value={f.weight ?? 0}
						onChange={(e) => setF({ ...f, weight: Number(e.target.value) })}
					/>
				</Field>
			</div>
			<div className="check-grid">
				<label className="check">
					<input
						type="checkbox"
						checked={f.enabled ?? false}
						onChange={(e) => setF({ ...f, enabled: e.target.checked })}
					/>
					<span>{t("common.enabled")}</span>
				</label>
				<label className="check">
					<input
						type="checkbox"
						checked={f.auto ?? false}
						onChange={(e) => setF({ ...f, auto: e.target.checked })}
					/>
					<span>{t("common.automatic")}</span>
				</label>
				<label className="check">
					<input
						type="checkbox"
						checked={f.manual_override ?? false}
						onChange={(e) => setF({ ...f, manual_override: e.target.checked })}
					/>
					<span>{t("routing.protectDiscovery")}</span>
				</label>
			</div>
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}

function Explain() {
	const { client } = useSession();
	const { t } = useI18n();
	const s = api(client!);
	const [model, setModel] = useState("");
	const [submitted, setSubmitted] = useState("");
	const query = useQuery({
		queryKey: ["explain", submitted],
		queryFn: ({ signal }) => s.explain(submitted, signal),
		enabled: !!submitted,
	});
	return (
		<Panel title={t("routing.explainTitle")}>
			<form
				className="search-bar"
				onSubmit={(e) => {
					e.preventDefault();
					setSubmitted(model.trim());
				}}
			>
				<input
					aria-label={t("routing.exactModel")}
					placeholder={t("routing.modelPlaceholder")}
					value={model}
					onChange={(e) => setModel(e.target.value)}
				/>
				<Button
					type="submit"
					icon={<Search size={16} />}
					disabled={!model.trim()}
				>
					{t("routing.explain")}
				</Button>
			</form>
			{!submitted ? (
				<Empty>{t("routing.explainEmpty")}</Empty>
			) : query.isPending ? (
				<Loading />
			) : query.isError ? (
				<ErrorState error={query.error} />
			) : !query.data.candidates.length ? (
				<>
					<div className="result-strip">
						<span>{t("common.routeId", { id: query.data.route_id })}</span>
						<span>
							{t("common.selectedPriority", {
								value: query.data.selected_priority ?? t("common.noneValue"),
							})}
						</span>
					</div>
					<Empty>{t("routing.explainNoCandidates")}</Empty>
				</>
			) : (
				<>
					<div className="result-strip">
						<span>{t("common.routeId", { id: query.data.route_id })}</span>
						<span>
							{t("common.selectedPriority", {
								value: query.data.selected_priority ?? t("common.noneValue"),
							})}
						</span>
					</div>
					<DataTable
						headers={[
							t("common.channel"),
							t("common.member"),
							t("common.eligible"),
							t("routing.priorityWeight"),
							t("common.reasons"),
						]}
					>
						{query.data.candidates.map((e) => (
							<tr
								key={e.candidate.member.id}
								className={!e.eligible ? "row-failed" : undefined}
							>
								<td>
									<strong>{e.candidate.channel.name}</strong>
									<small>#{e.candidate.channel.id}</small>
								</td>
								<td>#{e.candidate.member.id}</td>
								<td>
									<StatusBadge value={e.eligible ? "eligible" : "ineligible"} />
								</td>
								<td>
									{e.candidate.member.priority} / {e.candidate.member.weight}
								</td>
								<td>{e.reasons.join(", ") || "-"}</td>
							</tr>
						))}
					</DataTable>
				</>
			)}
		</Panel>
	);
}
