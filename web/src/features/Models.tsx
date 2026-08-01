import {
	ExternalLink,
	GripVertical,
	Pencil,
	Plus,
	Power,
	RotateCcw,
	Search,
	Shield,
	Sparkles,
	Trash2,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
	Route,
	RouteMember,
	RoutingCandidate,
} from "../api/types";
import { ActionMenu, type ActionMenuItem } from "../components/ActionMenu";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { PaginationBar } from "../components/PaginationBar";
import { EntityState } from "../components/EntityState";
import { StatGrid } from "../components/StatGrid";
import {
	Button,
	ConfirmDialog,
	Dialog,
	Empty,
	ErrorState,
	Field,
	Page,
	Panel,
	StatusBadge,
} from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { TryPanel } from "./TryPanel";

const ROUTING_INVALIDATE_KEYS = [
	["routes"],
	["route-overviews"],
	["members"],
	["channel-overviews"],
	["models"],
	["explain"],
] as const;

/**
 * Models page — catalog first (New API feel).
 * Default: which models are available and which upstream serves them.
 * Advanced routing (priority/weight/explain) stays behind one toggle.
 */
export function Models() {
	const { t } = useI18n();
	const [params] = useSearchParams();
	const modelParam = params.get("model")?.trim() ?? "";
	const channelId = positiveId(params.get("channel_id"));

	return (
		<Page
			kicker={t("modelsPage.kicker")}
			title={t("modelsPage.title")}
			description={t("modelsPage.description")}
		>
			<ModelCatalog initialModel={modelParam} channelId={channelId} />
		</Page>
	);
}

function ModelCatalog({
	initialModel,
	channelId: channelIdFromUrl,
}: {
	initialModel: string;
	channelId?: number;
}) {
	const { client } = useSession();
	const { t } = useI18n();
	const service = api(client!);
	const navigate = useNavigate();
	const [, setSearchParams] = useSearchParams();

	const overviews = useQuery({
		queryKey: ["route-overviews"],
		queryFn: ({ signal }) => service.routeOverviews(signal),
		refetchInterval: 15_000,
	});
	const channels = useQuery({
		queryKey: ["channels"],
		queryFn: ({ signal }) => service.channels(signal),
	});

	const [selected, setSelected] = useState<number | null>(null);
	const [query, setQuery] = useState(initialModel);
	const [channelFilter, setChannelFilter] = useState(channelIdFromUrl ?? 0);
	const [showAdvanced, setShowAdvanced] = useState(false);
	const [edit, setEdit] = useState<Partial<Route> | null>(null);
	const [remove, setRemove] = useState<Route | null>(null);
	const [member, setMember] = useState<Partial<RouteMember> | null>(null);
	const [removeMember, setRemoveMember] = useState<RouteMember | null>(null);
	const [tryOpen, setTryOpen] = useState(false);
	const [contextMenu, setContextMenu] = useState<{
		routeId: number;
		top: number;
		left: number;
	} | null>(null);

	useEffect(() => {
		if (channelIdFromUrl) setChannelFilter(channelIdFromUrl);
	}, [channelIdFromUrl]);

	const rows = useMemo(() => {
		const term = query.trim().toLowerCase();
		return (overviews.data ?? []).filter((item) => {
			const members = item.members ?? [];
			if (channelFilter > 0) {
				if (!members.some((m) => m.channel.id === channelFilter)) {
					return false;
				}
			}
			if (!term) return true;
			if (item.route.model_pattern.toLowerCase().includes(term)) return true;
			return members.some((m) =>
				m.channel.name.toLowerCase().includes(term),
			);
		});
	}, [channelFilter, overviews.data, query]);

	const pagination = useClientPagination(rows);
	const pageRows = pagination.pageItems;

	useEffect(() => {
		if (!rows.length) {
			if (selected !== null) setSelected(null);
			return;
		}
		if (selected && rows.some((item) => item.route.id === selected)) return;
		const first = rows[0];
		if (first) setSelected(first.route.id);
	}, [rows, selected]);

	useEffect(() => {
		if (!initialModel || !overviews.data?.length) return;
		const match = overviews.data.find(
			(item) => item.route.model_pattern === initialModel,
		);
		if (match) {
			setSelected(match.route.id);
			setQuery(initialModel);
		}
	}, [initialModel, overviews.data]);

	const selectedOverview =
		overviews.data?.find((item) => item.route.id === selected) ?? null;
	const selectedRoute = selectedOverview?.route ?? null;
	const selectedMembers = selectedOverview?.members ?? [];
	const primary = primaryMember(selectedMembers);
	/** Members of the route currently being edited (may differ from selection). */
	const editingOverview =
		edit?.id != null
			? (overviews.data ?? []).find((item) => item.route.id === edit.id) ??
				null
			: null;
	const editingMembers = editingOverview?.members ?? [];

	const save = useAdminMutation({
		mutationFn: (value: Partial<Route>) =>
			value.id
				? service.updateRoute(value.id, value)
				: service.createRoute(value),
		invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
		onSuccess: (route) => {
			setSelected(route.id);
			setEdit(null);
		},
	});
	const del = useAdminMutation({
		mutationFn: service.deleteRoute,
		invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
		onSuccess: () => {
			setSelected(null);
			setRemove(null);
		},
	});
	const saveMember = useAdminMutation({
		mutationFn: (value: Partial<RouteMember>) =>
			value.id
				? service.updateMember(value.id, value)
				: service.createMember(selected!, value),
		invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
		onSuccess: () => setMember(null),
	});
	const delMember = useAdminMutation({
		mutationFn: (memberId: number) => service.deleteMember(memberId),
		invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
		onSuccess: () => setRemoveMember(null),
	});
	const toggleRoute = useAdminMutation({
		mutationFn: (route: Route) =>
			service.updateRoute(route.id, { ...route, enabled: !route.enabled }),
		invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
		pendingIdOf: (route) => route.id,
	});
	const toggleMember = useAdminMutation({
		mutationFn: (entry: RouteMember) =>
			service.updateMember(entry.id, { ...entry, enabled: !entry.enabled }),
		invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
		pendingIdOf: (entry) => entry.id,
	});
	const clearHealth = useAdminMutation({
		mutationFn: (memberId: number) => service.clearMemberHealth(memberId),
		invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
		pendingIdOf: (id) => id,
	});
	/** Persist visual order as descending priority (top row = highest).
	 *  Reordering makes the whole model independent, so the Connections page
	 *  won't overwrite this hand-tuned order later. */
	const reorderMembers = useAdminMutation({
		mutationFn: async (ordered: RoutingCandidate[]) => {
			const total = ordered.length;
			await Promise.all(
				ordered.map((candidate, index) => {
					const nextPriority = total - index;
					const entry = candidate.member;
					if (
						entry.priority === nextPriority &&
						entry.manual_override
					) {
						return Promise.resolve(entry);
					}
					return service.updateMember(entry.id, {
						...entry,
						priority: nextPriority,
						manual_override: true,
					});
				}),
			);
		},
		invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
	});
	/** Batch toggle "independent priority/weight" for every member of a model.
	 *  Turning it off snaps members back to the channel's global values. */
	const pinAllMembers = useAdminMutation({
		mutationFn: async (input: {
			pinned: boolean;
			members: RoutingCandidate[];
		}) => {
			await Promise.all(
				input.members.map((candidate) => {
					const entry = candidate.member;
					const target = input.pinned
						? { ...entry, manual_override: true }
						: {
								...entry,
								manual_override: false,
								priority: candidate.channel.priority,
								weight: candidate.channel.weight,
							};
					if (
						entry.manual_override === target.manual_override &&
						entry.priority === target.priority &&
						entry.weight === target.weight
					) {
						return Promise.resolve(entry);
					}
					return service.updateMember(entry.id, target);
				}),
			);
		},
		invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
	});
	const [dragMemberId, setDragMemberId] = useState<number | null>(null);

	const selectRow = (routeId: number) => {
		setSelected(routeId);
	};

	const modelActions = (
		route: Route,
		options?: { closeContext?: boolean },
	): ActionMenuItem[] => {
		const busy = toggleRoute.pendingId === route.id || del.isPending;
		const close = () => {
			if (options?.closeContext) setContextMenu(null);
		};
		return [
			{
				key: "try",
				label: t("try.open"),
				icon: <Sparkles size={14} />,
				disabled: busy,
				onSelect: () => {
					close();
					selectRow(route.id);
					setTryOpen(true);
				},
			},
			{
				key: "toggle",
				label: route.enabled
					? t("common.disableAction")
					: t("common.enableAction"),
				icon: <Power size={14} />,
				disabled: busy,
				onSelect: () => {
					close();
					toggleRoute.mutate(route);
				},
			},
			{
				key: "logs",
				label: t("modelsPage.openLogs"),
				icon: <ExternalLink size={14} />,
				onSelect: () => {
					close();
					navigate(
						`/logs?model=${encodeURIComponent(route.model_pattern)}`,
					);
				},
			},
			{
				key: "routing",
				label: t("modelsPage.showRouting"),
				onSelect: () => {
					close();
					selectRow(route.id);
					setShowAdvanced(true);
				},
			},
			{
				key: "edit",
				label: t("common.edit"),
				icon: <Pencil size={14} />,
				disabled: busy,
				onSelect: () => {
					close();
					save.reset();
					setEdit(route);
				},
			},
			{
				key: "delete",
				label: t("common.delete"),
				icon: <Trash2 size={14} />,
				danger: true,
				disabled: busy,
				onSelect: () => {
					close();
					setRemove(route);
				},
			},
		];
	};

	const total = overviews.data?.length ?? 0;
	const enabledCount = (overviews.data ?? []).filter((o) => o.route.enabled)
		.length;

	return (
		<div className="ops-canvas">
			<StatGrid
				items={[
					{
						label: t("modelsPage.stat.total"),
						value: overviews.isPending ? "—" : total,
					},
					{
						label: t("modelsPage.stat.enabled"),
						value: overviews.isPending ? "—" : enabledCount,
					},
					{
						label: t("modelsPage.stat.multi"),
						value: overviews.isPending
							? "—"
							: (overviews.data ?? []).filter(
									(o) => (o.members ?? []).length > 1,
								)
									.length,
					},
				]}
			/>

			<div className="split">
				<Panel
					className="ops-list-panel"
					title={t("modelsPage.listTitle")}
					actions={
						<Button
							variant="secondary"
							icon={<Plus size={16} />}
							onClick={() => {
								save.reset();
								setEdit({ enabled: true });
							}}
						>
							{t("routing.addRoute")}
						</Button>
					}
				>
					<div className="models-simple-toolbar">
						<label className="directory-search models-search">
							<Search size={14} aria-hidden="true" />
							<input
								value={query}
								onChange={(event) => setQuery(event.target.value)}
								placeholder={t("routing.searchPlaceholder")}
								aria-label={t("routing.searchPlaceholder")}
							/>
						</label>
						<select
							aria-label={t("ops.filterChannel")}
							value={channelFilter}
							onChange={(event) => {
								const next = Number(event.target.value) || 0;
								setChannelFilter(next);
								setSearchParams(
									next > 0 ? { channel_id: String(next) } : {},
									{ replace: true },
								);
							}}
						>
							<option value={0}>{t("ops.allChannels")}</option>
							{(channels.data ?? []).map((channel) => (
								<option key={channel.id} value={channel.id}>
									{channel.name}
								</option>
							))}
						</select>
					</div>

					<EntityState
						isLoading={overviews.isPending}
						isError={overviews.isError}
						error={overviews.error}
						isEmpty={!rows.length}
						empty={
							<EmptyHero
								kicker={t("modelsPage.emptyKicker")}
								title={t("modelsPage.emptyTitle")}
								body={t("modelsPage.empty")}
								actions={
									<>
										<Button
											icon={<Plus size={16} />}
											onClick={() => {
												save.reset();
												setEdit({ enabled: true });
											}}
										>
											{t("routing.addRoute")}
										</Button>
										<Link className="button button-secondary" to="/">
											{t("modelsPage.ctaConnections")}
										</Link>
									</>
								}
							/>
						}
						retry={() => overviews.refetch()}
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
							<div className="table-wrap">

							<table>
								<thead>
									<tr>
										<th>{t("common.model")}</th>
										<th>{t("modelsPage.col.upstream")}</th>
										<th className="status-col">{t("common.status")}</th>
										<th className="actions">{t("common.actions")}</th>
									</tr>
								</thead>
								<tbody>
									{pageRows.map((item) => {
										const active = item.route.id === selected;
										const head = primaryMember(item.members);
										const ready = item.members.filter(
											(entry) => candidateState(entry) === "ready",
										).length;
										const rowBusy =
											toggleRoute.pendingId === item.route.id ||
											del.isPending;
										return (
											<tr
												key={item.route.id}
												className={`is-clickable${active ? " is-selected" : ""}`}
												onClick={() => selectRow(item.route.id)}
												onContextMenu={(event) => {
													event.preventDefault();
													selectRow(item.route.id);
													setContextMenu({
														routeId: item.route.id,
														top: event.clientY,
														left: event.clientX,
													});
												}}
											>
												<td>
													<strong className="mono">
														{item.route.model_pattern}
													</strong>
												</td>
												<td>
													{head ? (
														<span title={head.channel.name}>
															{head.channel.name}
															{item.members.length > 1
																? ` +${item.members.length - 1}`
																: ""}
														</span>
													) : (
														<span className="muted">
															{t("modelsPage.noUpstream")}
														</span>
													)}
												</td>
												<td className="status-col">
													<StatusBadge
														value={
															!item.route.enabled
																? "disabled"
																: ready > 0
																	? "ready"
																	: "unverified"
														}
													/>
												</td>
												<td
													className="actions row-actions"
													onClick={(event) => event.stopPropagation()}
												>
													<ActionMenu
														compact
														label={t("common.moreActions")}
														disabled={rowBusy}
														items={modelActions(item.route)}
													/>
												</td>
											</tr>
										);
									})}
								</tbody>
							</table>
							</div>
						</ListShell>
						{contextMenu
							? (() => {
										const overview =
											rows.find(
												(row) => row.route.id === contextMenu.routeId,
											) ??
											(overviews.data ?? []).find(
												(row) => row.route.id === contextMenu.routeId,
											);
										if (!overview) return null;
										return (
											<ActionMenu
												label={t("common.moreActions")}
												open
												onOpenChange={(open) => {
													if (!open) setContextMenu(null);
												}}
												position={{
													top: contextMenu.top,
													left: contextMenu.left,
												}}
												items={modelActions(overview.route, {
													closeContext: true,
												})}
											/>
										);
								  })()
								: null}

					</EntityState>
				</Panel>

				<div className="detail-card ops-detail-card is-compact">
					{!selectedRoute || !selectedOverview ? (
						<div className="detail-empty">{t("modelsPage.selectHint")}</div>
					) : (
						<>
							<div className="detail-head">
								<div>
									<p className="detail-kicker">{t("modelsPage.detailKicker")}</p>
									<h2 className="mono">{selectedRoute.model_pattern}</h2>
									<small>
										{primary
											? t("modelsPage.servedBy", {
													name: primary.channel.name,
												})
											: t("modelsPage.noUpstream")}
										{selectedMembers.length > 1
											? ` · ${t("modelsPage.extraPaths", {
													n: selectedMembers.length - 1,
												})}`
											: ""}
									</small>
								</div>
								<StatusBadge
									value={selectedRoute.enabled ? "enabled" : "disabled"}
								/>
							</div>

							<div className="detail-primary-bar">
								<Button
									icon={<Sparkles size={14} />}
									onClick={() => setTryOpen(true)}
								>
									{t("try.open")}
								</Button>
								<ActionMenu
									compact
									label={t("common.moreActions")}
									disabled={toggleRoute.pendingId === selectedRoute.id}
									items={modelActions(selectedRoute)}
								/>
								<p className="detail-actions-hint">
									{t("modelsPage.rowActionsHint")}
								</p>
							</div>

							<button
								type="button"
								className="advanced-toggle"
								onClick={() => setShowAdvanced((value) => !value)}
							>
								{showAdvanced
									? t("modelsPage.hideRouting")
									: t("modelsPage.showRouting")}
							</button>

							{showAdvanced ? (
								<section className="models-advanced">
									<div className="models-advanced-bar">
										<p>{t("modelsPage.routingHint")}</p>
										<Button
											variant="secondary"
											icon={<Plus size={14} />}
											onClick={() => {
												saveMember.reset();
												setMember({
													priority: (selectedMembers.length || 0) + 1,
													weight: 100,
													enabled: true,
													manual_override: true,
												});
											}}
										>
											{t("routing.addMember")}
										</Button>
									</div>
									{selectedMembers.length > 1 ? (
										<p className="routing-reorder-hint">
											{reorderMembers.isPending
												? t("routing.savingOrder")
												: t("routing.reorderHint")}
										</p>
									) : null}
									{!selectedMembers.length ? (
										<Empty>{t("routing.noMembers")}</Empty>
									) : (
										sortMembers(selectedMembers).map((candidate, rowIndex) => {
											const state = candidateState(candidate);
											const entry = candidate.member;
											const activeCooldown = isActiveCooldown(entry);
											const ordered = sortMembers(selectedMembers);
											const busy =
												toggleMember.pendingId === entry.id ||
												clearHealth.pendingId === entry.id ||
												reorderMembers.isPending;
											const applyOrder = (next: RoutingCandidate[]) => {
												reorderMembers.mutate(next);
											};
											const moveBy = (delta: number) => {
												const from = ordered.findIndex(
													(item) => item.member.id === entry.id,
												);
												const to = from + delta;
												if (from < 0 || to < 0 || to >= ordered.length) return;
												const next = [...ordered];
												const temp = next[from]!;
												next[from] = next[to]!;
												next[to] = temp;
												applyOrder(next);
											};
											return (
												<div
													className={`member-row${dragMemberId === entry.id ? " is-dragging" : ""}`}
													key={entry.id}
													draggable={!reorderMembers.isPending}
													onDragStart={(event) => {
														setDragMemberId(entry.id);
														event.dataTransfer.effectAllowed = "move";
														event.dataTransfer.setData(
															"text/plain",
															String(entry.id),
														);
													}}
													onDragOver={(event) => {
														event.preventDefault();
														event.dataTransfer.dropEffect = "move";
													}}
													onDrop={(event) => {
														event.preventDefault();
														const sourceId = Number(
															event.dataTransfer.getData("text/plain"),
														);
														setDragMemberId(null);
														if (!sourceId || sourceId === entry.id) return;
														const current = sortMembers(selectedMembers);
														const from = current.findIndex(
															(item) => item.member.id === sourceId,
														);
														const to = current.findIndex(
															(item) => item.member.id === entry.id,
														);
														if (from < 0 || to < 0) return;
														const next = [...current];
														const [moved] = next.splice(from, 1);
														if (!moved) return;
														next.splice(to, 0, moved);
														applyOrder(next);
													}}
													onDragEnd={() => setDragMemberId(null)}
												>
													<button
														type="button"
														className="member-drag-handle"
														aria-label={t("routing.orderLabel")}
														title={t("routing.reorderHint")}
													>
														<GripVertical size={16} />
													</button>
													<div className="member-row-main">
														<strong>{candidate.channel.name}</strong>
														<small>
															#{rowIndex + 1}
															{" · "}
															{t("routing.priorityLabel")}: {entry.priority}
															{" · "}
															{t("routing.weightLabel")}: {entry.weight}
															{" · "}
															<StatusBadge value={state} />
														{entry.manual_override ? (
															<>
																{" "}
																<span title={t("routing.protectedHint")}>
																	<Shield
																		size={12}
																		style={{ verticalAlign: "middle" }}
																	/>{" "}
																</span>
																{t("routing.protectedLabel")}
															</>
															) : null}
															{activeCooldown && entry.last_error
																? ` · ${entry.last_error}`
																: null}
															{activeCooldown ? (
																<>{" "}
																	<CooldownHint until={entry.cooldown_until!} />
																</>
															) : null}
														</small>
													</div>
													<div className="member-controls">
														<button
															type="button"
															className="icon-button"
															aria-label={t("routing.moveUp")}
															title={t("routing.moveUp")}
															disabled={busy || rowIndex <= 0}
															onClick={() => moveBy(-1)}
														>
															↑
														</button>
														<button
															type="button"
															className="icon-button"
															aria-label={t("routing.moveDown")}
															title={t("routing.moveDown")}
															disabled={
																busy || rowIndex >= ordered.length - 1
															}
															onClick={() => moveBy(1)}
														>
															↓
														</button>
														{activeCooldown ? (
															<button
																type="button"
																className="member-clear-health"
																title={t("routing.clearHealth")}
																disabled={clearHealth.isPending}
																onClick={() => clearHealth.mutate(entry.id)}
															>
																<RotateCcw size={13} />
																{t("routing.clearHealth")}
															</button>
														) : null}
														<ActionMenu
															compact
															label={t("common.moreActions")}
															disabled={busy}
															items={[
																{
																	key: "toggle",
																	label: entry.enabled
																		? t("common.disableAction")
																		: t("common.enableAction"),
																	onSelect: () =>
																		toggleMember.mutate(entry),
																},
																...(activeCooldown
																	? [
																			{
																				key: "clear",
																				label: t("routing.clearHealth"),
																				onSelect: () =>
																					clearHealth.mutate(entry.id),
																			},
																		]
																	: []),
																{
																	key: "edit",
																	label: t("common.edit"),
																	icon: <Pencil size={14} />,
																	onSelect: () => {
																		saveMember.reset();
																		setMember(entry);
																	},
																},
																{
																	key: "delete",
																	label: t("common.delete"),
																	icon: <Trash2 size={14} />,
																	danger: true,
																	onSelect: () => setRemoveMember(entry),
																},
															]}
														/>
													</div>
												</div>
											);
										})
									)}
								</section>
							) : null}
						</>
					)}
				</div>
			</div>

			{edit ? (
				<RouteDialog
					value={edit}
					members={editingMembers}
					pending={save.isPending}
					error={save.error}
					onClose={() => setEdit(null)}
					onSave={(value) => {
						const { pin_priority, ...routeValue } = value;
						save.mutate(routeValue);
						if (pin_priority !== undefined && editingMembers.length) {
							pinAllMembers.mutate({
								pinned: pin_priority,
								members: editingMembers,
							});
						}
					}}
				/>
			) : null}
			{member && selected ? (
				<MemberDialog
					value={member}
					channels={(channels.data ?? []).map((channel) => ({
						id: channel.id,
						name: channel.name,
					}))}
					pending={saveMember.isPending}
					error={saveMember.error}
					onClose={() => setMember(null)}
					onSave={(value) => saveMember.mutate(value)}
				/>
			) : null}
			{remove ? (
				<ConfirmDialog
					title={t("routing.deleteRoute")}
					message={t("routing.deleteRouteMsg", {
						name: remove.model_pattern,
					})}
					pending={del.isPending}
					error={del.error}
					onClose={() => setRemove(null)}
					onConfirm={() => del.mutate(remove.id)}
				/>
			) : null}
			{removeMember ? (
				<ConfirmDialog
					title={t("routing.deleteMember")}
					message={t("routing.deleteMemberMsg", { id: removeMember.id })}
					pending={delMember.isPending}
					error={delMember.error}
					onClose={() => setRemoveMember(null)}
					onConfirm={() => delMember.mutate(removeMember.id)}
				/>
			) : null}
			{tryOpen && selectedRoute ? (
				<Dialog title={t("try.title")} onClose={() => setTryOpen(false)}>
					<TryPanel
						defaultModel={selectedRoute.model_pattern}
						upstreams={selectedMembers.map((candidate) => ({
							id: candidate.channel.id,
							name: candidate.channel.name,
							priority: candidate.member.priority,
							weight: candidate.member.weight,
						}))}
						onClose={() => setTryOpen(false)}
					/>
				</Dialog>
			) : null}
		</div>
	);
}

function RouteDialog({
	value,
	members,
	pending,
	error,
	onClose,
	onSave,
}: {
	value: Partial<Route>;
	members: RoutingCandidate[];
	pending: boolean;
	error: unknown;
	onClose: () => void;
	onSave: (value: Partial<Route> & { pin_priority?: boolean }) => void;
}) {
	const { t } = useI18n();
	const [form, setForm] = useState(value);
	const allPinned =
		members.length > 0 &&
		members.every((candidate) => candidate.member.manual_override);
	const [pinPriority, setPinPriority] = useState(allPinned);
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
						disabled={pending || !form.model_pattern}
						onClick={() =>
							onSave({ ...form, pin_priority: pinPriority })
						}
					>
						{pending ? t("common.working") : t("common.save")}
					</Button>
				</>
			}
		>
			<p className="channel-form-intro">{t("modelsPage.addRouteHint")}</p>
			<Field label={t("routing.exactModel")}>
				<input
					value={form.model_pattern ?? ""}
					onChange={(event) =>
						setForm({ ...form, model_pattern: event.target.value })
					}
					placeholder="gpt-4o-mini"
				/>
			</Field>
			<label className="check">
				<input
					type="checkbox"
					checked={form.enabled ?? false}
					onChange={(event) =>
						setForm({ ...form, enabled: event.target.checked })
					}
				/>
				<span>{t("routing.routeEnabled")}</span>
			</label>
			{members.length > 0 ? (
				<label className="check check-with-hint">
					<input
						type="checkbox"
						checked={pinPriority}
						onChange={(event) => setPinPriority(event.target.checked)}
					/>
					<span>
						<strong>{t("routing.independentLabel")}</strong>
						<small>{t("routing.independentHint")}</small>
					</span>
				</label>
			) : null}
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
	onSave: (value: Partial<RouteMember>) => void;
}) {
	const { t } = useI18n();
	const [form, setForm] = useState(value);
	return (
		<Dialog
			title={value.id ? t("routing.editMember") : t("routing.addMember")}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button
						disabled={pending || !form.channel_id}
						onClick={() => onSave(form)}
					>
						{pending ? t("common.working") : t("common.save")}
					</Button>
				</>
			}
		>
			{!value.id ? (
				<Field label={t("common.channel")}>
					<select
						value={form.channel_id ?? ""}
						onChange={(event) =>
							setForm({
								...form,
								channel_id: Number(event.target.value) || undefined,
							})
						}
					>
						<option value="">{t("common.select")}</option>
						{channels.map((channel) => (
							<option key={channel.id} value={channel.id}>
								{channel.name}
							</option>
						))}
					</select>
				</Field>
			) : null}
			<p className="channel-form-intro">{t("routing.memberDialogIntro")}</p>
			<div className="form-grid">
				<Field label={t("routing.priorityLabel")}>
					<input
						type="number"
						value={form.priority ?? 0}
						onChange={(event) =>
							setForm({ ...form, priority: Number(event.target.value) })
						}
					/>
					<span className="field-hint">{t("routing.priorityHint")}</span>
				</Field>
				<Field label={t("routing.weightLabel")}>
					<input
						type="number"
						min={0}
						value={form.weight ?? 100}
						onChange={(event) =>
							setForm({ ...form, weight: Number(event.target.value) })
						}
					/>
					<span className="field-hint">{t("routing.weightHint")}</span>
				</Field>
			</div>
			<label className="check check-with-hint">
				<input
					type="checkbox"
					checked={form.enabled ?? true}
					onChange={(event) =>
						setForm({ ...form, enabled: event.target.checked })
					}
				/>
				<span>
					<strong>{t("routing.enabledLabel")}</strong>
					<small>{t("routing.enabledHint")}</small>
				</span>
			</label>
			<label className="check check-with-hint">
				<input
					type="checkbox"
					checked={form.manual_override ?? false}
					onChange={(event) =>
						setForm({ ...form, manual_override: event.target.checked })
					}
				/>
				<span>
					<strong>{t("routing.protectedLabel")}</strong>
					<small>{t("routing.protectedHint")}</small>
				</span>
			</label>
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}

function primaryMember(members: RoutingCandidate[]) {
	if (!members.length) return null;
	return sortMembers(members)[0] ?? null;
}

function sortMembers(members: RoutingCandidate[]) {
	return [...members].sort((left, right) => {
		if (right.member.priority !== left.member.priority) {
			return right.member.priority - left.member.priority;
		}
		if (right.member.weight !== left.member.weight) {
			return right.member.weight - left.member.weight;
		}
		return left.channel.name.localeCompare(right.channel.name);
	});
}

function isActiveCooldown(member: Pick<RouteMember, "cooldown_until">) {
	if (!member.cooldown_until) return false;
	const until = new Date(member.cooldown_until).getTime();
	return Number.isFinite(until) && until > Date.now();
}

function candidateState(candidate: RoutingCandidate) {
	const member = candidate.member;
	if (!member.enabled) return "disabled";
	if (!candidate.credential_usable) return "no_credential";
	// Historical failures do not keep a member degraded after its penalty ends.
	if (isActiveCooldown(member)) return "cooling_down";
	return "ready";
}

function formatCooldownLeft(iso: string, now = Date.now()) {
	const until = new Date(iso).getTime();
	if (!Number.isFinite(until)) return "?";
	const seconds = Math.max(0, Math.ceil((until - now) / 1000));
	if (seconds >= 60) {
		const mins = Math.floor(seconds / 60);
		return `${mins}m${seconds % 60 > 0 ? ` ${seconds % 60}s` : ""}`;
	}
	return `${seconds}s`;
}

/** Cooldown countdown that re-renders itself every second until expiry. */
function CooldownHint({ until }: { until: string }) {
	const { t } = useI18n();
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (new Date(until).getTime() - Date.now() <= 0) return;
		const id = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(id);
	}, [until]);
	const remaining = new Date(until).getTime() - now;
	if (remaining <= 0) return null;
	return (
		<span className="member-cooldown-hint">
			{t("routing.cooldownHint", { left: formatCooldownLeft(until, now) })}
		</span>
	);
}

function positiveId(value: string | null) {
	if (!value) return undefined;
	const parsed = Number(value);
	return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
}
