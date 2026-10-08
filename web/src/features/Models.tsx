import { ModelWorkspaceLayout } from "./models/ModelWorkspaceLayout";
import {
  Activity,
  RefreshCw,
  Combine,
  History,
  Info,
  Plus,
  SlidersHorizontal,
  X,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type FocusEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
  ModelMatchMode,
  ModelMetadata,
  Route,
  RouteMember,
  RoutingCandidate,
} from "../api/types";
import { ActionMenu } from "../components/ActionMenu";
import { TelemetryStrip } from "../components/TelemetryStrip";
import { Button, ConfirmDialog, Dialog, Empty, Page, PageActions, Panel } from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useToast } from "../toast";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { TryPanel } from "./TryPanel";
import { MemberModelsPage as MemberModels } from "./MemberModelsPage";
import { positiveId } from "../lib/positiveId";
import { readScopedTabState, writeScopedTabState } from "../lib/tabState";
import { useListSelection } from "../lib/useListSelection";
import { useModelsBoard } from "./models/useModelsBoard";
import { useModelFilters } from "./models/useModelFilters";
import { useCooldownExpiry } from "../lib/cooldownClock";
import { ModelMetadataDialog } from "./models/ModelMetadataDialog";
import { modelActions as routeActions, type ModelActionDeps } from "./models/modelActions";
import { RouteDirectory } from "./models/RouteDirectory";
import { RouteDialog } from "./models/RouteDialog";
import { MemberDialog } from "./models/MemberDialog";
import { ModelToolDialogs, useModelTools } from "./models/ModelTools";
import { ModelChangesPanel } from "./models/ModelChangesPanel";
import { RouteDetailPanel } from "./models/RouteDetailPanel";

function readMissingDismissed() {
  try {
    return sessionStorage.getItem("models.missingDismissed") === "1";
  } catch {
    return false;
  }
}

function storeMissingDismissed() {
  try {
    sessionStorage.setItem("models.missingDismissed", "1");
  } catch {
    // Storage may be disabled; dismiss for this render only.
  }
}

// Tab-scoped persistence for the models workspace, namespaced and shared: see
// lib/tabState.ts. The wrappers keep every call site in this file unchanged.
const readTabState = <T,>(key: string, fallback: T): T =>
  readScopedTabState("models", key, fallback);
const writeTabState = <T,>(key: string, value: T): void =>
  writeScopedTabState("models", key, value);
import { primaryMember, sortMembers, getEffectiveRoutingPolicy } from "./models/routingPolicy";

const ROUTING_INVALIDATE_KEYS = [
  ["routes"],
  ["route-overviews"],
  ["members"],
  ["channel-overviews"],
  ["models"],
  ["explain"],
  ["model-pricing"],
] as const;

/**
 * Models page — catalog first (New API feel).
 * Default: which models are available and which upstream serves them.
 * Advanced routing (priority/weight/explain) stays behind one toggle.
 */
/**
 * The models page, split by role.
 *
 * Data containers remain separate to enforce scoped requests. Both share the
 * directory/detail layout, catalogue table, search and pricing presentation.
 * Staff edit the gateway's
 * routes and members, while a member reads the catalogue they may call and
 * arranges their own order. Separate components on purpose — a member runs no
 * queries against the admin endpoints, so there is nothing to guard at request
 * time and no hooks that would differ between the two branches.
 */
export function Models() {
  const { role } = useSession();
  return role === "member" ? <MemberModels /> : <AdminModels />;
}

function AdminModels() {
  const { t } = useI18n();
  const [params] = useSearchParams();
  const modelParam = params.get("model")?.trim() ?? "";
  const channelId = positiveId(params.get("channel_id"));
  const groupParam = params.get("group")?.trim() ?? "";

  return (
    <Page title={t("modelsPage.title")} description={t("modelsPage.description")}>
      <ModelsWorkspace initialModel={modelParam} channelId={channelId} initialGroup={groupParam} />
    </Page>
  );
}

/**
 * The models workspace: the page that owns the catalogue's state (queries, filters,
 * selection, bulk actions and dialogs) and composes the four pieces the A02 split
 * produced — RouteDirectory, RouteDetailPanel, ModelTools and RoutePolicyCard — over
 * the shared ModelWorkspaceLayout.
 */
function ModelsWorkspace({
  initialModel,
  channelId: channelIdFromUrl,
  initialGroup,
}: {
  initialModel: string;
  channelId?: number;
  initialGroup: string;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const service = api(client!);
  const toast = useToast();
  const navigate = useNavigate();
  const [params, setSearchParams] = useSearchParams();

  // Everything this page reads (the eight queries and the three derived lookups)
  // is owned by models/useModelsBoard — including the refetch intervals, which are
  // a policy rather than an accident of where the query happened to be written.
  const board = useModelsBoard({ service });
  const {
    overviews,
    channels,
    sticky,
    runtimeSettings,
    missing,
    virtualModels,
    metaByModel,
    pluginPageOf,
    financeItems,
  } = board;
  // URL wins over tab-scoped state on first mount; tab state survives the
  // bare-path sidebar navigation that drops the query string. Read once, so the
  // states and the filter panel's default-open decision cannot disagree.
  // Search, the three filters, the group list and the rows they narrow: owned by
  // models/useModelFilters. Local names kept so the rest of the page reads as
  // before; pagination stays here (a view choice about those rows).
  const filters = useModelFilters({
    overviews: overviews.data ?? [],
    metaByModel,
    initial: { model: initialModel, group: initialGroup, channel: channelIdFromUrl },
  });
  const {
    query,
    setQuery,
    channelFilter,
    setChannelFilter,
    groupFilter,
    setGroupFilter,
    statusFilter,
    setStatusFilter,
    showModelFilters,
    setShowModelFilters,
    activeFilterCount,
    modelGroups,
    rows,
  } = filters;
  // The selected route and the members disclosure are the page's own (the URL and
  // the tab remember the first); the filter hook deliberately owns neither.
  const [selected, setSelected] = useState<number | null>(() =>
    readTabState<number | null>("selected", null),
  );
  const [showAdvanced, setShowAdvanced] = useState(true);
  useEffect(() => writeTabState("selected", selected), [selected]);
  const [edit, setEdit] = useState<Partial<Route> | null>(null);
  const [editMeta, setEditMeta] = useState<ModelMetadata | null>(null);
  const [remove, setRemove] = useState<Route | null>(null);
  const [member, setMember] = useState<Partial<RouteMember> | null>(null);
  const [removeMember, setRemoveMember] = useState<RouteMember | null>(null);
  const [tryOpen, setTryOpen] = useState(false);
  // The tools' own panels (capabilities / site probe / unify / unify history /
  // upstream changes) own their open state; this page only opens them.
  const tools = useModelTools();
  const {
    setProbeOpen,
    setSiteProbeOpen,
    setCapabilitiesOpen,
    setUnifyOpen,
    setUnifyHistoryOpen,
    openChanges,
  } = tools;
  /** Route whose "attach every channel serving this model" preview is open. */
  const [autoMatchRoute, setAutoMatchRoute] = useState<Route | null>(null);
  const [bulkSelect, setBulkSelect] = useState(false);
  const [selectedMemberIds, setSelectedMemberIds] = useState<Set<number>>(() => new Set());
  /** Route-group tab currently being viewed ("default" = legacy behavior). */
  const [activeGroup, setActiveGroup] = useState("default");
  /** Inline tab editor: create a new group or rename an existing one. */
  const [groupDraft, setGroupDraft] = useState<{
    mode: "new" | "rename";
    from?: string;
    value: string;
    copyDefault?: boolean;
  } | null>(null);
  const [removeGroup, setRemoveGroup] = useState<string | null>(null);
  const [missingDismissed, setMissingDismissed] = useState(readMissingDismissed);
  const [contextMenu, setContextMenu] = useState<{
    routeId: number;
    top: number;
    left: number;
  } | null>(null);

  const pagination = useClientPagination(rows, 20, "models");
  const pageRows = pagination.pageItems;

  // Bulk selection over the routing table (current-page checkboxes); actions
  // resolve against the full filtered list so selections survive paging.
  // Bulk selection, owned by lib/useListSelection (shared with the connections
  // board); local names kept so the rest of this page reads as before.
  const selection = useListSelection();
  const bulkSelected = selection.selected;
  const bulkMode = selection.mode;
  const setBulkSelected = selection.setSelected;
  const setBulkMode = selection.setMode;
  const exitBulkMode = selection.exit;
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const bulkRoutes = useMemo(
    () => rows.filter((item) => bulkSelected.has(item.route.id)),
    [rows, bulkSelected],
  );
  const pageAllSelected =
    pageRows.length > 0 && pageRows.every((r) => bulkSelected.has(r.route.id));
  const selectAllPage = () => {
    setBulkSelected((prev) => {
      const next = new Set(prev);
      if (pageAllSelected) {
        pageRows.forEach((r) => next.delete(r.route.id));
      } else {
        pageRows.forEach((r) => next.add(r.route.id));
      }
      return next;
    });
  };
  // The hook's toggle is the same operation this page had inline.
  const toggleBulkSelected = selection.toggle;
  const bulkToggleRoutes = useAdminMutation({
    mutationFn: async (input: { ids: number[]; enabled: boolean }) => {
      const results = await Promise.allSettled(
        bulkRoutes
          .filter((item) => input.ids.includes(item.route.id))
          .map((item) =>
            service.updateRoute(item.route.id, {
              ...item.route,
              enabled: input.enabled,
            }),
          ),
      );
      return {
        ok: results.filter((r) => r.status === "fulfilled").length,
        total: input.ids.length,
      };
    },
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    onSuccess: ({ ok, total }) => {
      toast.push({
        tone: ok === total ? "success" : "error",
        message: t("modelsPage.bulkRouteDone", { ok, total }),
      });
      setBulkSelected(new Set());
    },
  });
  const bulkDeleteRoutes = useAdminMutation({
    mutationFn: async (ids: number[]) => {
      const results = await Promise.allSettled(ids.map((id) => service.deleteRoute(id)));
      return {
        ok: results.filter((r) => r.status === "fulfilled").length,
        total: ids.length,
      };
    },
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    onSuccess: ({ ok, total }) => {
      toast.push({
        tone: ok === total ? "success" : "error",
        message: t("modelsPage.bulkDeleteDone", { ok, total }),
      });
      setBulkSelected(new Set());
    },
  });
  const bulkBusy = bulkToggleRoutes.isPending || bulkDeleteRoutes.isPending;

  // URL → selection: restore the selected route when the URL changes (direct
  // links, back/forward, page refresh). Depends only on params/rows so a user
  // click (which changes only `selected`) can never be overwritten by the
  // stale URL captured before the click renders.
  useEffect(() => {
    const routeParam = positiveId(params.get("route"));
    if (routeParam && rows.some((item) => item.route.id === routeParam)) {
      setSelected(routeParam);
    }
  }, [params, rows]);

  // Selection fallback: when no valid selection exists (empty filter results,
  // deleted route, first visit), pick the first visible row. Does not read the
  // URL, so it can never fight the user's click with a stale route param.
  useEffect(() => {
    if (!rows.length) {
      if (selected !== null) setSelected(null);
      return;
    }
    if (selected && rows.some((item) => item.route.id === selected)) return;
    const first = rows[0];
    if (first) setSelected(first.route.id);
  }, [rows, selected]);

  // Selection → URL: keep the URL in sync so switching pages restores the
  // selection. Writes only when the URL differs; once written, the restore
  // effect above reads the same value and bails out.
  useEffect(() => {
    if (!selected) return;
    const next = new URLSearchParams(params);
    if (next.get("route") === String(selected)) return;
    next.set("route", String(selected));
    setSearchParams(next, { replace: true });
  }, [params, selected, setSearchParams]);

  useEffect(() => {
    if (!initialModel || !overviews.data?.length) return;
    const match = overviews.data.find((item) => item.route.model_pattern === initialModel);
    if (match) {
      setSelected(match.route.id);
      setQuery(initialModel);
    }
  }, [initialModel, overviews.data, setQuery]);

  const selectedOverview = overviews.data?.find((item) => item.route.id === selected) ?? null;
  const selectedRoute = selectedOverview?.route ?? null;
  const selectedMembers = useMemo(
    () => selectedOverview?.members ?? [],
    [selectedOverview?.members],
  );
  const selectedModel = selectedRoute?.model_pattern ?? "";

  useEffect(() => {
    setSelectedMemberIds(new Set());
    setBulkSelect(false);
    setActiveGroup("default");
    setGroupDraft(null);
    setRemoveGroup(null);
  }, [selected]);

  // Price-aware member ordering (cheapest first) when the toggle is on.
  const orderedMembers = useMemo(() => sortMembers(selectedMembers), [selectedMembers]);
  /** Normalized member groups; the active tab stays visible while empty. */
  const groupNames = useMemo(() => {
    const names = new Set<string>();
    for (const candidate of orderedMembers) {
      names.add((candidate.member.group_name || "").trim() || "default");
    }
    names.add("default");
    if (activeGroup) names.add(activeGroup);
    return [...names].sort((left, right) => {
      if (left === "default") return -1;
      if (right === "default") return 1;
      return left.localeCompare(right);
    });
  }, [activeGroup, orderedMembers]);
  const groupCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const candidate of orderedMembers) {
      const group = (candidate.member.group_name || "").trim() || "default";
      counts.set(group, (counts.get(group) ?? 0) + 1);
    }
    return counts;
  }, [orderedMembers]);
  const visibleMembers = useMemo(
    () =>
      orderedMembers.filter(
        (candidate) => ((candidate.member.group_name || "").trim() || "default") === activeGroup,
      ),
    [activeGroup, orderedMembers],
  );
  // `candidateState` reads the wall clock, so without this a member whose
  // cooldown has just elapsed keeps showing 冷却中 / offering 清除冷却 until the
  // 15s poll (or a page switch) lands. Re-render the row at the deadline
  // instead of waiting for unrelated state to change.
  useCooldownExpiry(visibleMembers.map((candidate) => candidate.member.cooldown_until));
  const explain = useQuery({
    queryKey: ["explain", selected, activeGroup],
    queryFn: ({ signal }) => service.explain(selectedRoute!.model_pattern, signal, activeGroup),
    enabled: Boolean(selectedRoute),
    refetchInterval: 15_000,
  });
  const primary = primaryMember(selectedMembers, selectedRoute ?? undefined);
  const selectedRoutingMode = selectedRoute?.routing_mode || "auto";
  const effectivePolicy = getEffectiveRoutingPolicy(
    selectedRoutingMode,
    runtimeSettings.data?.editable,
  );
  const effectiveRetryRounds =
    explain.data?.retry_times_override ??
    selectedRoute?.retry_times ??
    runtimeSettings.data?.editable.retry_times;
  const effectiveChannelRetries =
    selectedRoute?.channel_retry_times ?? runtimeSettings.data?.editable.channel_retry_times;
  const retryPolicyIsOverridden = selectedRoute?.retry_times != null;
  const channelRetryPolicyIsOverridden = selectedRoute?.channel_retry_times != null;
  /** The member pinned by routing_mode=single, when that mode is active. */
  const singleModePinned =
    selectedRoute?.routing_mode === "single"
      ? (selectedMembers.find(
          (candidate) => candidate.member.id === selectedRoute.single_member_id,
        ) ?? null)
      : null;
  const singleModeActive = selectedRoute?.routing_mode === "single";
  const singleModeApplies = Boolean(
    singleModePinned &&
    explain.data?.candidates.some(
      (item) => item.candidate.member.id === singleModePinned.member.id,
    ),
  );
  const pinOutsideGroup = Boolean(singleModePinned && explain.data && !singleModeApplies);
  /** Members of the route currently being edited (may differ from selection). */
  const editingOverview =
    edit?.id != null
      ? ((overviews.data ?? []).find((item) => item.route.id === edit.id) ?? null)
      : null;
  const editingMembers = editingOverview?.members ?? [];

  const save = useAdminMutation({
    mutationFn: (
      value: Partial<Route> & {
        auto_match_channel_ids?: number[];
        auto_match_mode?: ModelMatchMode;
      },
    ) => (value.id ? service.updateRoute(value.id, value) : service.createRoute(value)),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    toastOnError: false,
    onSuccess: (route) => {
      setSelected(route.id);
      setEdit(null);
    },
  });
  const del = useAdminMutation({
    mutationFn: service.deleteRoute,
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    toastOnError: false,
    onSuccess: () => {
      setSelected(null);
      setRemove(null);
    },
  });
  const saveMember = useAdminMutation({
    mutationFn: (value: Partial<RouteMember>) =>
      value.id ? service.updateMember(value.id, value) : service.createMember(selected!, value),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    toastOnError: false,
    onSuccess: () => setMember(null),
  });

  const delMember = useAdminMutation({
    mutationFn: (memberId: number) => service.deleteMember(memberId),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    toastOnError: false,
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
    mutationFn: ({
      route,
      mode,
      singleMemberId,
    }: {
      route: Route;
      mode: string;
      singleMemberId?: number | null;
    }) =>
      service.updateRoute(route.id, {
        ...route,
        routing_mode: mode,
        ...(singleMemberId !== undefined ? { single_member_id: singleMemberId } : {}),
      }),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    pendingIdOf: ({ route }) => route.id,
  });
  const toggleMember = useAdminMutation({
    mutationFn: (entry: RouteMember) => service.updateMember(entry.id, { enabled: !entry.enabled }),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    pendingIdOf: (entry) => entry.id,
  });
  /**
   * "Use only this channel": switch the route into routing_mode=single with
   * this member pinned. Non-destructive — every member keeps its enabled
   * flag, cross-channel retry reads as 0 at evaluation time, and restoring is
   * just switching the mode back (server-side, survives reloads).
   */
  const pinMember = useAdminMutation({
    mutationFn: (input: { route: Route; memberId: number | null }) =>
      service.updateRoute(input.route.id, {
        ...input.route,
        routing_mode: input.memberId != null ? "single" : "auto",
        single_member_id: input.memberId,
      }),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
  });
  /** Bulk enable/disable the members selected in bulk mode. */
  const bulkToggleMembers = useAdminMutation({
    mutationFn: async (input: { enabled: boolean }) => {
      const updates = (orderedMembers ?? [])
        .filter((candidate) => selectedMemberIds.has(candidate.member.id))
        .filter((candidate) => candidate.member.enabled !== input.enabled)
        .map((candidate) =>
          service.updateMember(candidate.member.id, {
            enabled: input.enabled,
          }),
        );
      await Promise.all(updates);
    },
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    onSuccess: () => {
      setSelectedMemberIds(new Set());
      setBulkSelect(false);
    },
  });
  const toggleMemberSelect = (memberId: number) => {
    setSelectedMemberIds((previous) => {
      const next = new Set(previous);
      if (next.has(memberId)) {
        next.delete(memberId);
      } else {
        next.add(memberId);
      }
      return next;
    });
  };
  const selectAllMembers = () => {
    setSelectedMemberIds(new Set((orderedMembers ?? []).map((c) => c.member.id)));
  };
  const clearMemberSelection = () => setSelectedMemberIds(new Set());
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
            priority: nextPriority,
            manual_override: true,
          });
        }),
      );
    },
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
  });
  const renameGroup = useAdminMutation({
    mutationFn: (input: { from: string; to: string }) =>
      service.renameMemberGroup(selected!, input.from, input.to),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    toastOnError: false,
    onSuccess: (_data, input) => {
      setActiveGroup(input.to);
      setGroupDraft(null);
    },
  });
  const copyDefaultGroup = useAdminMutation({
    mutationFn: (to: string) => service.copyMemberGroup(selected!, "default", to),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    toastOnError: false,
    onSuccess: (_data, to) => {
      setActiveGroup(to);
      setGroupDraft(null);
    },
  });
  const removeGroupMut = useAdminMutation({
    mutationFn: (name: string) => service.deleteMemberGroup(selected!, name),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    toastOnError: false,
    onSuccess: (_data, name) => {
      setRemoveGroup(null);
      setActiveGroup((current) => (current === name ? "default" : current));
    },
  });
  /** Blur cancels the inline group editor — unless focus is only moving to
   *  another part of it (the copy-default checkbox), which would unmount
   *  together with the draft and become impossible to click. */
  const groupEditorBlur = (event: FocusEvent) => {
    const scope = event.currentTarget.closest(".member-group-tabs");
    if (scope && event.relatedTarget instanceof Node && scope.contains(event.relatedTarget)) return;
    setGroupDraft(null);
  };
  /** Commits the inline tab editor; new groups are local until first member. */
  const submitGroupDraft = () => {
    if (!groupDraft || !selected) return;
    const name = groupDraft.value.trim();
    if (!name || name.length > 64) return;
    if (groupDraft.mode === "new") {
      if (groupNames.includes(name)) return;
      if (groupDraft.copyDefault) {
        copyDefaultGroup.mutate(name);
        return;
      }
      setActiveGroup(name);
      setGroupDraft(null);
      return;
    }
    const from = groupDraft.from!;
    if (name === from) {
      setGroupDraft(null);
      return;
    }
    if (groupNames.includes(name)) return;
    renameGroup.mutate({ from, to: name });
  };
  /** Opens the member dialog preset for the active group tab. */
  const openAddMember = () => {
    saveMember.reset();
    setMember({
      priority: (visibleMembers.length || 0) + 1,
      weight: 100,
      enabled: true,
      manual_override: true,
      group_name: activeGroup,
    });
  };
  /** Batch toggle "independent priority/weight" for every member of a model.
   *  Turning it off snaps members back to the channel's global values. */
  const pinAllMembers = useAdminMutation({
    mutationFn: async (input: { pinned: boolean; members: RoutingCandidate[] }) => {
      await Promise.all(
        input.members.map((candidate) => {
          const entry = candidate.member;
          const target = input.pinned
            ? { manual_override: true }
            : {
                manual_override: false,
                priority: candidate.channel.priority,
                weight: candidate.channel.weight,
              };
          if (
            entry.manual_override === target.manual_override &&
            (input.pinned || (entry.priority === target.priority && entry.weight === target.weight))
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
    const next = new URLSearchParams(params);
    next.set("route", String(routeId));
    setSearchParams(next, { replace: true });
  };

  const delMeta = useAdminMutation({
    mutationFn: (name: string) => service.deleteModelMetadata(name),
    invalidateKeys: [["model-metadata"], ["model-pricing"]],
    toastOnError: false,
    onSuccess: () => setEditMeta(null),
  });
  const saveMeta = useAdminMutation({
    mutationFn: (value: ModelMetadata) => service.upsertModelMetadata(value.model_name, value),
    invalidateKeys: [["model-metadata"], ["model-pricing"]],
    toastOnError: false,
    onSuccess: () => setEditMeta(null),
  });

  // Everything the route action menu needs, in one explicit hand-off: the menu
  // itself lives in models/modelActions.tsx (pure mapping, unit-testable).
  const modelActionDeps: ModelActionDeps = {
    t,
    navigate,
    metaByModel,
    selectRow,
    setContextMenu,
    setBulkMode,
    setBulkSelected,
    setTryOpen,
    setEditMeta,
    setEdit,
    setRemove,
    mutations: { toggleRoute, del, save },
  };

  const modelActions = (route: Route, options?: { closeContext?: boolean }) =>
    routeActions(route, modelActionDeps, options);

  const total = overviews.data?.length ?? 0;
  const enabledCount = (overviews.data ?? []).filter((o) => o.route.enabled).length;

  return (
    <div className="ops-canvas models-catalog">
      <TelemetryStrip
        items={[
          {
            label: t("modelsPage.stat.total"),
            value: overviews.isPending ? "—" : total,
            tone: "primary",
          },
          {
            label: t("modelsPage.stat.enabled"),
            value: overviews.isPending ? "—" : enabledCount,
            tone: "success",
          },
          {
            label: t("modelsPage.stat.multi"),
            value: overviews.isPending
              ? "—"
              : (overviews.data ?? []).filter((o) => (o.members ?? []).length > 1).length,
            tone: "info",
          },
        ]}
      />

      {/* Shown when affinity is on by default OR when something is actually
          bound: with the global switch off, a single model can still opt in
          per route (routes.sticky_session), and those live bindings are the
          only place that is visible. The explicit `sticky.data &&` is what
          narrows the payload for the reads below. */}
      {sticky.data && (sticky.data.enabled || sticky.data.stats.bound_sessions > 0) ? (
        <Panel
          className="sticky-panel"
          title={t("sticky.title")}
          titleHelp={t("sticky.hint")}
          collapsible
          defaultOpen={false}
          storageKey="models.sticky"
          summary={
            <>
              <strong>{sticky.data.stats.bound_sessions}</strong> {t("sticky.bound")}
              <span className="panel-summary-sep" aria-hidden="true">
                ·
              </span>
              <strong>{sticky.data.stats.hits}</strong> {t("sticky.hits")}
            </>
          }
        >
          <div className="sticky-stats">
            <span>
              <strong>{sticky.data.stats.bound_sessions}</strong> {t("sticky.bound")}
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
                        {(channels.data ?? []).find((channel) => channel.id === entry.channel_id)
                          ?.name ?? `#${entry.channel_id}`}
                      </td>
                      <td className="muted">{new Date(entry.expires_at).toLocaleString()}</td>
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
              storeMissingDismissed();
              setMissingDismissed(true);
            }}
          >
            <X size={12} />
          </button>
        </div>
      ) : null}

      <PageActions>
        <Button
          variant="secondary"
          icon={<Activity size={16} />}
          onClick={() => setProbeOpen(true)}
          title={t("modelsPage.probe.actionHint")}
        >
          {t("modelsPage.probe.action")}
        </Button>
        <ActionMenu
          label={t("modelsPage.tools")}
          items={[
            {
              key: "capabilities",
              label: t("workbench.cap.title"),
              icon: <SlidersHorizontal size={14} />,
              onSelect: () => setCapabilitiesOpen(true),
            },
            {
              key: "site-probe",
              label: t("modelsPage.siteProbe.title"),
              icon: <Activity size={14} />,
              onSelect: () => setSiteProbeOpen(true),
            },
            {
              key: "unify",
              label: t("modelsPage.unify.action"),
              icon: <Combine size={14} />,
              onSelect: () => setUnifyOpen(true),
            },
            {
              key: "history",
              label: t("modelsPage.unify.history.action"),
              icon: <History size={14} />,
              onSelect: () => setUnifyHistoryOpen(true),
            },
            {
              key: "upstream-changes",
              label: t("modelChanges.title"),
              icon: <RefreshCw size={14} />,
              onSelect: () => openChanges(),
            },
          ]}
        />
        <Button
          icon={<Plus size={16} />}
          title={t("modelsPage.addRouteHint")}
          onClick={() => {
            save.reset();
            setEdit({ enabled: true });
          }}
        >
          {t("routing.addRoute")}
        </Button>
      </PageActions>
      {/* The upstream-change summary sits above the workspace: it is a notice
          about the model list underneath it, and it was a footnote at the
          bottom of the page where nobody saw it before opening the list. */}
      <ModelChangesPanel openRequest={tools.changesOpenRequest} hideWhenQuiet />
      <ModelWorkspaceLayout
        directory={
          <RouteDirectory
            t={t}
            query={query}
            setQuery={setQuery}
            params={params}
            setSearchParams={setSearchParams}
            showModelFilters={showModelFilters}
            setShowModelFilters={setShowModelFilters}
            activeFilterCount={activeFilterCount}
            groupFilter={groupFilter}
            setGroupFilter={setGroupFilter}
            channelFilter={channelFilter}
            setChannelFilter={setChannelFilter}
            statusFilter={statusFilter}
            setStatusFilter={setStatusFilter}
            modelGroups={modelGroups}
            channels={channels.data ?? []}
            overviews={overviews}
            rows={rows}
            pageRows={pageRows}
            virtualModels={virtualModels}
            pluginPageOf={pluginPageOf}
            navigate={navigate}
            selected={selected}
            metaByModel={metaByModel}
            pagination={pagination}
            bulkMode={bulkMode}
            bulkSelected={bulkSelected}
            setBulkSelected={setBulkSelected}
            toggleBulkSelected={toggleBulkSelected}
            exitBulkMode={exitBulkMode}
            selectAllPage={selectAllPage}
            pageAllSelected={pageAllSelected}
            bulkBusy={bulkBusy}
            bulkToggleRoutes={bulkToggleRoutes}
            setBulkDeleteOpen={setBulkDeleteOpen}
            selectRow={selectRow}
            contextMenu={contextMenu}
            setContextMenu={setContextMenu}
            modelActions={modelActions}
            pending={{ toggleRoute, del }}
            resetRouteDraft={() => save.reset()}
            startNewRoute={() => setEdit({ enabled: true })}
          />
        }
        detail={
          !selectedRoute || !selectedOverview ? (
            <div className="detail-empty">{t("modelsPage.selectHint")}</div>
          ) : (
            <RouteDetailPanel
              t={t}
              route={selectedRoute}
              selectedModel={selectedModel}
              header={{
                t,
                route: selectedRoute,
                primary,
                memberCount: selectedMembers.length,
                singleModePinned: Boolean(singleModePinned),
                onOpenTry: () => setTryOpen(true),
                saveRoutingMode,
                toggleRoute,
                actions: modelActions(selectedRoute),
                candidates: selectedMembers,
              }}
              policy={{
                t,
                effectivePolicy,
                effectiveRetryRounds,
                effectiveChannelRetries,
                singleModeApplies,
                retryPolicyIsOverridden,
                channelRetryPolicyIsOverridden,
                crossChannelFailoverEnabled:
                  runtimeSettings.data?.editable.cross_channel_failover_enabled,
              }}
              singleModeActive={singleModeActive}
              singleModePinned={singleModePinned}
              pinOutsideGroup={pinOutsideGroup}
              pinPending={Boolean(pinMember.isPending)}
              onClearPin={() => pinMember.mutate({ route: selectedRoute, memberId: null })}
              showAdvanced={showAdvanced}
              setShowAdvanced={setShowAdvanced}
              openAddMember={openAddMember}
              setAutoMatchRoute={setAutoMatchRoute}
              bulkSelect={bulkSelect}
              setBulkSelect={setBulkSelect}
              selectedMemberIds={selectedMemberIds}
              setSelectedMemberIds={setSelectedMemberIds}
              bulkToggleMembers={bulkToggleMembers}
              selectAllMembers={selectAllMembers}
              clearMemberSelection={clearMemberSelection}
              groupNames={groupNames}
              activeGroup={activeGroup}
              setActiveGroup={setActiveGroup}
              groupCounts={groupCounts}
              groupDraft={groupDraft}
              setGroupDraft={setGroupDraft}
              submitGroupDraft={submitGroupDraft}
              groupEditorBlur={groupEditorBlur}
              setRemoveGroup={setRemoveGroup}
              groupFallback={
                explain.data?.group_fallback ? { routeGroup: explain.data.route_group } : null
              }
              totalMemberCount={selectedMembers.length}
              list={{
                t,
                route: selectedRoute,
                selectedModel,
                members: visibleMembers,
                explain,
                financeItems,
                dragMemberId,
                setDragMemberId,
                reorderMembers,
                bulkSelect,
                selectedMemberIds,
                toggleMemberSelect,
                navigate,
                enableChannel,
                mutations: { toggleMember, pinMember, clearHealth, saveMember },
                setMember,
                setRemoveMember,
              }}
            />
          )
        }
      />

      <ModelToolDialogs
        tools={tools}
        autoMatchRoute={autoMatchRoute}
        setAutoMatchRoute={setAutoMatchRoute}
        selectedRoute={selectedRoute}
        activeGroup={activeGroup}
        visibleMembers={visibleMembers}
      />
      {edit ? (
        <RouteDialog
          value={edit}
          members={editingMembers}
          pending={save.isPending}
          error={save.error}
          stickyGlobalDefault={sticky.data?.enabled ?? null}
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
          pending={saveMeta.isPending || delMeta.isPending}
          error={saveMeta.error ?? delMeta.error}
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
          groups={groupNames}
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
      {removeGroup ? (
        <ConfirmDialog
          title={t("routing.groupDelete")}
          message={t("routing.groupDeleteConfirm", {
            name: removeGroup,
            count: groupCounts.get(removeGroup) ?? 0,
          })}
          pending={removeGroupMut.isPending}
          error={removeGroupMut.error}
          onClose={() => setRemoveGroup(null)}
          onConfirm={() => removeGroupMut.mutate(removeGroup)}
        />
      ) : null}
      {tryOpen && selectedRoute ? (
        <Dialog title={t("try.title")} onClose={() => setTryOpen(false)}>
          <TryPanel
            defaultModel={selectedRoute.model_pattern}
            members={selectedMembers}
            route={selectedRoute}
            onClose={() => setTryOpen(false)}
          />
        </Dialog>
      ) : null}
      {bulkDeleteOpen ? (
        <ConfirmDialog
          title={t("modelsPage.bulkDeleteSelected")}
          confirmLabel={t("common.delete")}
          message={t("modelsPage.bulkDeleteConfirm", {
            n: bulkSelected.size,
          })}
          onClose={() => {
            if (!bulkDeleteRoutes.isPending) setBulkDeleteOpen(false);
          }}
          onConfirm={() =>
            bulkDeleteRoutes.mutate([...bulkSelected], {
              onSuccess: () => setBulkDeleteOpen(false),
            })
          }
          pending={bulkDeleteRoutes.isPending}
          error={bulkDeleteRoutes.error}
        />
      ) : null}
    </div>
  );
}
