import {
  ExternalLink,
  GripVertical,
  Info,
  Pencil,
  Plus,
  Power,
  RotateCcw,
  Search,
  Shield,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
  FinanceItem,
  ModelMetadata,
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
  InfoTip,
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
  const sticky = useQuery({
    queryKey: ["sticky"],
    queryFn: ({ signal }) => service.sticky(signal),
    retry: false,
    refetchInterval: 15_000,
  });
  const runtimeSettings = useQuery({
    queryKey: ["runtime-settings"],
    queryFn: ({ signal }) => service.runtimeSettings(signal),
    retry: false,
  });
  // Account finances (balance + per-model price per channel), cached upstream
  // for a short TTL; used to show call price / affordable calls per member.
  const finance = useQuery({
    queryKey: ["finance"],
    queryFn: ({ signal }) => service.finance(signal),
    retry: false,
    refetchInterval: 120_000,
  });
  // Models exposed by channels but not covered by any enabled route.
  const missing = useQuery({
    queryKey: ["missing-models"],
    queryFn: ({ signal }) => service.missingModels(signal),
    refetchInterval: 60_000,
  });
  // Model metadata library (capability annotations shown as badges).
  const metadata = useQuery({
    queryKey: ["model-metadata"],
    queryFn: ({ signal }) => service.modelMetadata(signal),
    refetchInterval: 60_000,
  });
  const metaByModel = useMemo(() => {
    const map = new Map<string, ModelMetadata>();
    for (const item of metadata.data?.items ?? []) {
      map.set(item.model_name, item);
    }
    return map;
  }, [metadata.data]);

  const [selected, setSelected] = useState<number | null>(null);
  const [query, setQuery] = useState(initialModel);
  const [channelFilter, setChannelFilter] = useState(channelIdFromUrl ?? 0);
  const [showAdvanced, setShowAdvanced] = useState(true);
  const [edit, setEdit] = useState<Partial<Route> | null>(null);
  const [editMeta, setEditMeta] = useState<ModelMetadata | null>(null);
  const [remove, setRemove] = useState<Route | null>(null);
  const [member, setMember] = useState<Partial<RouteMember> | null>(null);
  const [removeMember, setRemoveMember] = useState<RouteMember | null>(null);
  const [tryOpen, setTryOpen] = useState(false);
  const [priceSort, setPriceSort] = useState(false);
  const [missingDismissed, setMissingDismissed] = useState(
    () => sessionStorage.getItem("models.missingDismissed") === "1",
  );
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
      return members.some((m) => m.channel.name.toLowerCase().includes(term));
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
  const selectedMembers = useMemo(() => selectedOverview?.members ?? [], [selectedOverview?.members]);
  const selectedModel = selectedRoute?.model_pattern ?? "";
  // Price-aware member ordering (cheapest first) when the toggle is on.
  const financeItems = useMemo(() => finance.data?.items ?? [], [finance.data?.items]);
  const orderedMembers = useMemo(
    () =>
      priceSort
        ? sortMembersByPrice(selectedMembers, selectedModel, financeItems)
        : sortMembers(selectedMembers),
    [priceSort, selectedMembers, selectedModel, financeItems],
  );
  // Cheapest priced member (for the "cheapest" badge); null when none priced.
  const cheapestMemberId = useMemo(() => {
    if (!selectedModel || !financeItems.length) return null;
    let bestId: number | null = null;
    let bestPrice = Number.POSITIVE_INFINITY;
    for (const candidate of selectedMembers) {
      const price = memberPriceUsd(
        candidate.member,
        selectedModel,
        financeItems,
      );
      if (price != null && price < bestPrice) {
        bestPrice = price;
        bestId = candidate.member.id;
      }
    }
    return bestId;
  }, [selectedMembers, selectedModel, financeItems]);
  const primary = primaryMember(selectedMembers);
  const selectedRoutingMode = selectedRoute?.routing_mode || "auto";
  const effectivePolicy = getEffectiveRoutingPolicy(
    selectedRoutingMode,
    runtimeSettings.data?.editable,
  );
  const effectiveRetryRounds =
    selectedRoute?.retry_times ?? runtimeSettings.data?.editable.retry_times;
  const effectiveChannelRetries =
    selectedRoute?.channel_retry_times ??
    runtimeSettings.data?.editable.channel_retry_times;
  const retryPolicyIsOverridden = selectedRoute?.retry_times != null;
  const channelRetryPolicyIsOverridden =
    selectedRoute?.channel_retry_times != null;
  const explain = useQuery({
    queryKey: ["explain", selected],
    queryFn: ({ signal }) =>
      service.explain(selectedRoute!.model_pattern, signal),
    enabled: Boolean(selectedRoute),
    refetchInterval: 15_000,
  });
  /** Members of the route currently being edited (may differ from selection). */
  const editingOverview =
    edit?.id != null
      ? ((overviews.data ?? []).find((item) => item.route.id === edit.id) ??
        null)
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
const enableChannel = useAdminMutation({
	mutationFn: async (channelId: number) => {
		const list = channels.data ?? [];
		const ch = list.find((item) => item.id === channelId);
		if (!ch) throw new Error("channel not found");
		return service.updateChannel(channelId, { ...ch, status: "enabled" });
	},
	invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
	pendingIdOf: (channelId) => channelId,
});
  const toggleRoute = useAdminMutation({
    mutationFn: (route: Route) =>
      service.updateRoute(route.id, { ...route, enabled: !route.enabled }),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    pendingIdOf: (route) => route.id,
  });
  const saveRoutingMode = useAdminMutation({
    mutationFn: ({ route, mode }: { route: Route; mode: string }) =>
      service.updateRoute(route.id, { ...route, routing_mode: mode }),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    pendingIdOf: ({ route }) => route.id,
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
          if (entry.priority === nextPriority && entry.manual_override) {
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

	  const delMeta = useAdminMutation({
	mutationFn: (name: string) => service.deleteModelMetadata(name),
	invalidateKeys: [["model-metadata"]],
	  });
	  const saveMeta = useAdminMutation({
	mutationFn: (value: ModelMetadata) =>
	  service.upsertModelMetadata(value.model_name, value),
	invalidateKeys: [["model-metadata"]],
	  });

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
          navigate(`/logs?model=${encodeURIComponent(route.model_pattern)}`);
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
		key: "meta",
		label: t("modelsPage.editMetadata"),
		icon: <Shield size={14} />,
		onSelect: () => {
		  close();
		  setEditMeta(
			metaByModel.get(route.model_pattern) ?? {
			  model_name: route.model_pattern,
			  context_window: 0,
			  input_modalities: "",
			  output_modalities: "",
			  supports_thinking: -1,
			  vendor: "",
			  notes: "",
			},
		  );
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
  const enabledCount = (overviews.data ?? []).filter(
    (o) => o.route.enabled,
  ).length;

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
                ).length,
          },
        ]}
      />

      {sticky.data?.enabled ? (
        <Panel
          className="sticky-panel"
          title={t("sticky.title")}
          titleHelp={t("sticky.hint")}
        >
          <div className="sticky-stats">
            <span>
              <strong>{sticky.data.stats.bound_sessions}</strong>{" "}
              {t("sticky.bound")}
            </span>
            <span>
              <strong>{sticky.data.stats.hits}</strong> {t("sticky.hits")}
            </span>
            <span>
              <strong>{sticky.data.stats.binds}</strong> {t("sticky.binds")}
            </span>
            <span>
              <strong>{sticky.data.stats.escapes}</strong> {t("sticky.escapes")}
            </span>
            <span>
              <strong>
                {t("sticky.minutes", {
                  n: Math.max(1, Math.round(sticky.data.ttl_seconds / 60)),
                })}
              </strong>{" "}
              {t("sticky.ttl")}
            </span>
          </div>
          {sticky.data.entries.length ? (
            <div className="table-wrap sticky-entries">
              <table>
                <thead>
                  <tr>
                    <th>{t("sticky.col.key")}</th>
                    <th>{t("sticky.col.channel")}</th>
                    <th>{t("sticky.col.expires")}</th>
                  </tr>
                </thead>
                <tbody>
                  {sticky.data.entries.map((entry) => (
                    <tr key={entry.key}>
                      <td className="mono">{entry.key}</td>
                      <td>
                        {(channels.data ?? []).find(
                          (channel) => channel.id === entry.channel_id,
                        )?.name ?? `#${entry.channel_id}`}
                      </td>
                      <td className="muted">
                        {new Date(entry.expires_at).toLocaleString()}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Empty>{t("sticky.empty")}</Empty>
          )}
        </Panel>
      ) : null}

      {missing.data?.items?.length && !missingDismissed ? (
        <div className="missing-models-banner">
          <Info size={13} />
          <span>
            {t("modelsPage.missingModels", {
              count: missing.data.items.length,
            })}
          </span>
          <button
            type="button"
            className="missing-models-focus"
            onClick={() => {
              const first = missing.data!.items[0];
              if (first) setQuery(first.model);
            }}
          >
            {t("modelsPage.missingModelsFocus")}
          </button>
          <button
            type="button"
            className="missing-models-close"
            aria-label={t("common.dismiss")}
            title={t("common.dismiss")}
            onClick={() => {
              sessionStorage.setItem("models.missingDismissed", "1");
              setMissingDismissed(true);
            }}
          >
            <X size={12} />
          </button>
        </div>
      ) : null}

      <div className="split models-split">
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
                setSearchParams(next > 0 ? { channel_id: String(next) } : {}, {
                  replace: true,
                });
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
                    <Link className="button button-secondary" to="/channels">
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
								{(() => {
									const meta = metaByModel.get(
										item.route.model_pattern,
									);
									if (!meta) return null;
									return (
										<span className="model-meta-badges">
											{meta.context_window > 0 ? (
												<span
													className="model-meta-badge"
													title={t("modelsPage.metaCtx")}
												>
													{formatTokens(meta.context_window)}
												</span>
											) : null}
											{meta.supports_thinking > 0 ? (
												<span
													className="model-meta-badge is-thinking"
													title={t("modelsPage.metaThinking")}
												>
													{t("modelsPage.metaThinkingShort")}
												</span>
											) : null}
											{meta.vendor ? (
												<span
														className="model-meta-badge"
														title={t("modelsPage.metaVendor")}
													>
													{meta.vendor}
												</span>
											) : null}
										</span>
									);
								})()}
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
                    rows.find((row) => row.route.id === contextMenu.routeId) ??
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
                  <p className="detail-kicker">
                    {t("modelsPage.detailKicker")}
                  </p>
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
                <div className="routing-mode-control">
                  <span>{t("routing.mode.label")}</span>
                  <InfoTip label={t("routing.modeHint")} />
                  <select
                    className="routing-mode-select"
                    aria-label={t("routing.mode.label")}
                    value={selectedRoute.routing_mode || "auto"}
                    disabled={saveRoutingMode.pendingId === selectedRoute.id}
                    onChange={(event) => {
                      const next = event.target.value;
                      saveRoutingMode.mutate({
                        route: selectedRoute,
                        mode: next,
                      });
                    }}
                  >
                    <option value="auto">{t("routing.mode.auto")}</option>
                    <option value="adaptive">
                      {t("routing.mode.adaptive")}
                    </option>
                    <option value="latency">{t("routing.mode.latency")}</option>
                    <option value="weighted">
                      {t("routing.mode.weighted")}
                    </option>
                  </select>
                </div>
                <ActionMenu
                  compact
                  label={t("common.moreActions")}
                  disabled={toggleRoute.pendingId === selectedRoute.id}
                  items={modelActions(selectedRoute)}
                />
                <p className="detail-actions-hint">
                  {t("modelsPage.scopeHint")}
                </p>
              </div>
              <div className="routing-policy-summary">
                <span className="routing-policy-title">
                  {t("routing.effectivePolicy")}
                </span>
                {effectivePolicy ? (
                  <>
                    <span
                      className={`routing-signal${effectivePolicy.latency ? " is-on" : " is-off"}`}
                    >
                      {t("routing.signal.latency")}: {effectivePolicy.latency ? t("routing.signal.on") : t("routing.signal.off")}
                    </span>
                    <span
                      className={`routing-signal${effectivePolicy.error ? " is-on" : " is-off"}`}
                    >
                      {t("routing.signal.error")}: {effectivePolicy.error ? t("routing.signal.on") : t("routing.signal.off")}
                    </span>
                    <span className="routing-policy-source">
                      {t(effectivePolicy.source)}
                    </span>
                  </>
                ) : (
                  <span className="routing-policy-source">
                    {t("routing.policyLoading")}
                  </span>
                )}
              </div>
              <div className="routing-retry-summary">
                <span className="routing-policy-title">
                  {t("routing.retryPolicy")}
                </span>
                <span className="routing-policy-value">
                  {t("routing.retryRounds")}: {effectiveRetryRounds ?? "?"}
                  <small>
                    {t(
                      retryPolicyIsOverridden
                        ? "routing.policySource.model"
                        : "routing.policySource.global",
                    )}
                  </small>
                </span>
                <span className="routing-policy-value">
                  {t("routing.channelRetry")}: {effectiveChannelRetries ?? "?"}
                  <small>
                    {t(
                      channelRetryPolicyIsOverridden
                        ? "routing.policySource.model"
                        : "routing.policySource.global",
                    )}
                  </small>
                </span>
                <span
                  className={`routing-signal${runtimeSettings.data?.editable.cross_channel_failover_enabled ? " is-on" : " is-off"}`}
                >
                  {t("routing.failover")}: {runtimeSettings.data
                    ? runtimeSettings.data.editable.cross_channel_failover_enabled
                      ? t("routing.signal.on")
                      : t("routing.signal.off")
                    : "?"}
                </span>
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
                    <div className="models-advanced-help">
                      <span>{t("modelsPage.routingHint")}</span>
                    </div>
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
                <label className="price-sort-toggle" title={t("routing.priceSortHint")}>
                  <input
                    type="checkbox"
                    checked={priceSort}
                    onChange={(e) => setPriceSort(e.target.checked)}
                  />
                  <span>{t("routing.priceSort")}</span>
                </label>
              </div>
                  {selectedMembers.length > 1 ? (
                    <div className="routing-reorder-hint">
                      <span>
                        {reorderMembers.isPending
                          ? t("routing.savingOrder")
                          : t("routing.reorderHint")}
                      </span>
                      <InfoTip label={t("routing.reorderHint")} />
                    </div>
                  ) : null}
                  {!selectedMembers.length ? (
                    <Empty>{t("routing.noMembers")}</Empty>
                  ) : (
                    orderedMembers.map((candidate, rowIndex) => {
                      const entry = candidate.member;
                      const evaluation = explain.data?.candidates.find(
                        (item) => item.candidate.member.id === entry.id,
                      );
                      const activeCooldown = isActiveCooldown(entry);
                      const autoDisabled =
                        candidate.channel.status === "auto_disabled";
                      const state = autoDisabled
                        ? "auto_disabled"
                        : evaluation?.reasons.includes("circuit_open")
                          ? "circuit_open"
                          : candidateState(candidate);
                      // An expired cooldown is history, not an actionable
                      // cooldown. A disabled member still needs an explicit
                      // recovery action, unless the whole channel is parked
                      // (the channel-level recovery button handles that).
                      const canResetMemberHealth =
                        !autoDisabled &&
                        (activeCooldown ||
                          (!entry.enabled && entry.fail_count > 0));
                      const resetActionIsCooldown =
                        activeCooldown && entry.enabled;
                      const ordered = orderedMembers;
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
                          className={`member-row${dragMemberId === entry.id ? " is-dragging" : ""}${autoDisabled ? " is-auto-disabled" : ""}`}
                          key={entry.id}
                              draggable={!reorderMembers.isPending && !priceSort}
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
                              {(() => {
                                const score = evaluation?.score;
                                if (
                                  score == null ||
                                  Math.abs(score - entry.weight) < 0.01
                                ) {
                                  return null;
                                }
                                return (
                                  <>
                                    {" → "}
                                    <span
                                      className="member-effective-weight"
                                      title={t("routing.baseWeightHint")}
                                    >
                                      {Math.round(score)}
                                    </span>
                                  </>
                                );
                              })()}
									{entry.manual_override ? (
										<>
											{" "}
											<span
												className="member-protected"
												title={t("routing.protectedHint")}
											>
												<Shield size={12} /> {t("routing.protectedLabel")}
											</span>
										</>
									) : null}
									{memberFinance(entry, selectedModel, financeItems)
										? (() => {
												const info = memberFinance(
													entry,
													selectedModel,
													financeItems,
												)!;
												return (
													<>
														{" · "}
														<span
															className="member-finance"
															title={
																info.overdrawn
																	? t("routing.financeOverdrawnHint")
																	: t("routing.financeHint")
															}
														>
															{info.overdrawn
																	? t("routing.financeOverdrawn")
																	: t("routing.financeCalls", {
																			calls: info.calls,
																		})}
																{info.fixed
																	? t("routing.financeUnitCalls")
																	: t("routing.financeUnitM")}
															</span>
															{cheapestMemberId === entry.id ? (
																<span className="member-cheapest">
																	{t("routing.cheapest")}
																</span>
															) : null}
														</>
													);
												})()
											: (
												<>
													{" · "}
													<span
														className="member-finance is-na"
														title={t("routing.financeMissingHint")}
													>
														{t("routing.financeMissing")}
													</span>
												</>
											)}
                              {entry.fail_count > 0
                                ? ` · ${t(
                                    activeCooldown
                                      ? "routing.failCount"
                                      : "routing.failureHistory",
                                    { count: entry.fail_count },
                                  )}`
                                : null}
                              {activeCooldown && entry.last_error
                                ? ` · ${entry.last_error}`
                                : null}
                              {activeCooldown ? (
                                <>
                                  {" "}
                                  <CooldownHint until={entry.cooldown_until!} />
                                </>
                              ) : null}
                            </small>
                          </div>
                          <div className="member-controls">
                            <span className="member-row-state">
                              <StatusBadge value={state} />
                            </span>
                            {candidate.channel.status === "auto_disabled" ? (
                              <button
                                type="button"
                                className="member-clear-health"
                                title={t("routing.reenableChannelHint")}
                                disabled={
                                  enableChannel.pendingId ===
                                  candidate.channel.id
                                }
                                onClick={() =>
                                  enableChannel.mutate(candidate.channel.id)
                                }
                              >
                                <Power size={13} />
                                {t("routing.reenableChannel")}
                              </button>
                            ) : null}
                            {canResetMemberHealth ? (
                              <button
                                type="button"
                                className="member-clear-health"
                                title={t(
                                  resetActionIsCooldown
                                    ? "routing.clearHealth"
                                    : "routing.recoverMemberHint",
                                )}
                                disabled={clearHealth.isPending}
                                onClick={() => clearHealth.mutate(entry.id)}
                              >
                                <RotateCcw size={13} />
                                {t(
                                  resetActionIsCooldown
                                    ? "routing.clearHealth"
                                    : "routing.recoverMember",
                                )}
                              </button>
                            ) : null}
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
                                busy || rowIndex >= ordered.length - 1 || priceSort
                              }
                              onClick={() => moveBy(1)}
                            >
                              ↓
                            </button>
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
                                  onSelect: () => toggleMember.mutate(entry),
                                },
                                ...(canResetMemberHealth
                                  ? [
                                      {
                                        key: "clear",
                                        label: t(
                                          resetActionIsCooldown
                                            ? "routing.clearHealth"
                                            : "routing.recoverMember",
                                        ),
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
	  {editMeta ? (
		<ModelMetadataDialog
		  value={editMeta}
		  pending={saveMeta.isPending}
		  error={saveMeta.error}
		  onClose={() => setEditMeta(null)}
		  onSave={(value) => saveMeta.mutate(value)}
		  onDelete={
			metaByModel.has(editMeta.model_name)
			  ? () => delMeta.mutate(editMeta.model_name)
			  : undefined
		  }
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

// Compact token-count rendering for metadata badges (128000 → 128K).
function formatTokens(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1000) return `${Math.round(value / 1000)}K`;
  return String(value);
}

// Capability annotation editor for one canonical model name (context window,
// modalities, thinking support, vendor, notes).
function ModelMetadataDialog({
  value,
  pending,
  error,
  onClose,
  onSave,
  onDelete,
}: {
  value: ModelMetadata;
  pending: boolean;
  error?: unknown;
  onClose: () => void;
  onSave: (value: ModelMetadata) => void;
  onDelete?: () => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState<ModelMetadata>(value);
  const patch = (partial: Partial<ModelMetadata>) =>
    setForm((current) => ({ ...current, ...partial }));
  const thinkingOptions = [
    { value: -1, label: t("modelsPage.metaThinkingUnknown") },
    { value: 1, label: t("modelsPage.metaThinkingYes") },
    { value: 0, label: t("modelsPage.metaThinkingNo") },
  ];
  return (
    <Dialog
      title={t("modelsPage.metaTitle", { name: value.model_name })}
      onClose={onClose}
    >
      <div className="meta-form">
        <Field label={t("modelsPage.metaCtx")}>
          <input
            type="number"
            min={0}
            step={1000}
            value={form.context_window}
            onChange={(e) =>
              patch({ context_window: Math.max(0, Number(e.target.value) || 0) })
            }
            disabled={pending}
          />
        </Field>
        <Field label={t("modelsPage.metaInput")}>
          <input
            value={form.input_modalities}
            placeholder="text,image,audio"
            onChange={(e) => patch({ input_modalities: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("modelsPage.metaOutput")}>
          <input
            value={form.output_modalities}
            placeholder="text,audio"
            onChange={(e) => patch({ output_modalities: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("modelsPage.metaThinking")}>
          <select
            value={form.supports_thinking}
            onChange={(e) =>
              patch({ supports_thinking: Number(e.target.value) })
            }
            disabled={pending}
          >
            {thinkingOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("modelsPage.metaVendor")}>
          <input
            value={form.vendor}
            placeholder="DeepSeek, Google, OpenAI…"
            onChange={(e) => patch({ vendor: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("modelsPage.metaNotes")}>
          <input
            value={form.notes}
            onChange={(e) => patch({ notes: e.target.value })}
            disabled={pending}
          />
        </Field>
      </div>
	  {error ? <div className="inline-error">{String(error)}</div> : null}
      <div className="dialog-actions">
        {onDelete ? (
          <Button
            variant="danger"
            disabled={pending}
            onClick={() => {
              onDelete();
              onClose();
            }}
          >
            {t("common.delete")}
          </Button>
        ) : null}
        <span style={{ flex: 1 }} />
        <Button variant="secondary" disabled={pending} onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button disabled={pending} onClick={() => onSave(form)}>
          {pending ? t("common.working") : t("common.save")}
        </Button>
      </div>
    </Dialog>
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
            onClick={() => onSave({ ...form, pin_priority: pinPriority })}
          >
            {pending ? t("common.working") : t("common.save")}
          </Button>
        </>
      }
    >
			<div className="ops-panel-context">
				<span>{t("modelsPage.addRouteHint")}</span>
			</div>
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
      <div className="ops-panel-context" style={{ marginTop: 12 }}>
        <span>{t("routing.retryOverrideTitle")}</span>
      </div>
      <div className="form-grid">
        <Field
          label={t("routing.retryRounds")}
          hint={t("routing.retryRoundsHint")}
        >
          <input
            type="number"
            min={0}
            max={100}
            placeholder={t("routing.retryFollowGlobal")}
            value={form.retry_times ?? ""}
            onChange={(event) => {
              const v = event.target.value;
              setForm({
                ...form,
                retry_times: v === "" ? null : Number(v),
              });
            }}
          />
        </Field>
        <Field
          label={t("routing.channelRetry")}
          hint={t("routing.channelRetryHint")}
        >
          <input
            type="number"
            min={0}
            max={5}
            placeholder={t("routing.retryFollowGlobal")}
            value={form.channel_retry_times ?? ""}
            onChange={(event) => {
              const v = event.target.value;
              setForm({
                ...form,
                channel_retry_times: v === "" ? null : Number(v),
              });
            }}
          />
        </Field>
      </div>
      {members.length > 0 ? (
        <label className="check check-with-hint">
          <input
            type="checkbox"
            checked={pinPriority}
            onChange={(event) => setPinPriority(event.target.checked)}
          />
          <span>
									<strong>{t("routing.independentLabel")}</strong>
									<InfoTip label={t("routing.independentHint")} />
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
  // Editing priority/weight makes the member independent of the connection
  // defaults (otherwise a later connection edit or model re-sync overwrites
  // the values). Keeping the checkbox off and saving without touching the
  // values preserves the previous state.
  const [valuesTouched, setValuesTouched] = useState(false);
  const markTouched = () => setValuesTouched(true);
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
            onClick={() =>
              onSave({
                ...form,
                manual_override: (form.manual_override ?? false) || valuesTouched,
              })
            }
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
			<div className="ops-panel-context">
				<span>{t("routing.memberDialogIntro")}</span>
			</div>
      <div className="form-grid">
        <Field label={t("routing.priorityLabel")}>
          <input
            type="number"
            value={form.priority ?? 0}
            onChange={(event) => {
              markTouched();
              setForm({ ...form, priority: Number(event.target.value) });
            }}
          />
          <InfoTip label={t("routing.priorityHint")} />
        </Field>
        <Field label={t("routing.weightLabel")}>
          <input
            type="number"
            min={0}
            value={form.weight ?? 100}
            onChange={(event) => {
              markTouched();
              setForm({ ...form, weight: Number(event.target.value) });
            }}
          />
          <InfoTip label={t("routing.weightHint")} />
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
          <InfoTip label={t("routing.enabledHint")} />
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

/** Raw per-1M-token USD price for a member on a model, or null when unpriced. */
function memberPriceUsd(
  member: RouteMember,
  model: string,
  items: FinanceItem[],
): number | null {
  if (!member || !model || !items.length) return null;
  const item = items.find((entry) => entry.channel_id === member.channel_id);
  if (!item || item.quota_per_unit <= 0) return null;
  const price = item.prices?.[model];
  if (!price || !price.price_usd || price.price_usd <= 0) return null;
  return price.price_usd;
}

/** Price-aware ordering: cheapest first, unpriced members sink to the bottom. */
function sortMembersByPrice(
  members: RoutingCandidate[],
  model: string,
  items: FinanceItem[],
) {
  return [...members].sort((left, right) => {
    const lp = memberPriceUsd(left.member, model, items);
    const rp = memberPriceUsd(right.member, model, items);
    if (lp != null && rp != null) {
      if (lp !== rp) return lp - rp;
    } else if (lp != null) {
      return -1;
    } else if (rp != null) {
      return 1;
    }
    return sortMembers([left, right])[0] === left ? -1 : 1;
  });
}

function isActiveCooldown(member: Pick<RouteMember, "cooldown_until">) {
  if (!member.cooldown_until) return false;
  const until = new Date(member.cooldown_until).getTime();
  return Number.isFinite(until) && until > Date.now();
}

function candidateState(candidate: RoutingCandidate) {
  const member = candidate.member;
  // Channel-level guard: an auto-disabled channel is parked by the
  // consecutive-failure circuit. Surface it as the dominant state on every
  // member row so the model page (the routing view) shows it clearly.
  if (candidate.channel.status === "auto_disabled") return "auto_disabled";
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

/**
 * memberFinance resolves a member's call price and affordable call count on a
 * model from the finance overview. The upstream price table is quoted in quota
 * per 1M tokens; dividing by quota_per_unit yields the site-currency price.
 * Returns null when the channel has no finance data or the model is not priced.
 */
function memberFinance(
  member: RouteMember,
  model: string,
  items: FinanceItem[],
): { priceUsd: string; calls: string; fixed: boolean; overdrawn: boolean } | null {
  if (!member || !model || !items.length) return null;
  const item = items.find((entry) => entry.channel_id === member.channel_id);
  if (!item || item.quota_per_unit <= 0) return null;
  const price = item.prices?.[model];
  if (!price || !price.price_usd || price.price_usd <= 0) return null;
  const quotaPerUnit = item.quota_per_unit;
  const priceUsd = price.price_usd;
  const balanceUsd = item.balance / quotaPerUnit;
  const fixed = price.mode === "fixed";
  // fixed: price per request → affordable request count.
  // token: price per 1M tokens → affordable 1M-token units (shown as M).
  // A negative balance (overdrawn upstream) affords nothing; show 0 instead
  // of a misleading negative count and let the caller render the overdrawn state.
  const rawCalls =
    balanceUsd <= 0 ? 0 : Math.floor(balanceUsd / priceUsd);
  const formatUsd = (value: number) => {
    if (value >= 1) return value.toFixed(2);
    if (value >= 0.01) return value.toFixed(4);
    return value.toFixed(6);
  };
  const formatCount = (value: number) =>
    value >= 1000 ? `${Math.round(value / 1000)}k` : String(value);
  return {
    priceUsd: formatUsd(priceUsd),
    // Pure count — the render layer appends the unit (" 次" for fixed,
    // "M" for per-1M-token) exactly once.
    calls: formatCount(rawCalls),
    fixed,
    overdrawn: balanceUsd < 0,
  };
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

function getEffectiveRoutingPolicy(
  mode: string,
  runtime?: {
    routing_latency_aware: boolean;
    routing_error_aware: boolean;
  },
) {
  if (!runtime) return null;
  switch (mode) {
    case "adaptive":
      return {
        latency: true,
        error: true,
        source: "routing.policySource.model",
      };
    case "latency":
      return {
        latency: true,
        error: runtime.routing_error_aware,
        source: "routing.policySource.mixed",
      };
    case "weighted":
      return {
        latency: false,
        error: false,
        source: "routing.policySource.model",
      };
    default:
      return {
        latency: runtime.routing_latency_aware,
        error: runtime.routing_error_aware,
        source: "routing.policySource.global",
      };
  }
}

function positiveId(value: string | null) {
  if (!value) return undefined;
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
}
