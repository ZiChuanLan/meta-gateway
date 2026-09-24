import {
  ListChecks,
  Activity,
  RefreshCw,
  ExternalLink,
  ChevronDown,
  Combine,
  GripVertical,
  History,
  Info,
  Pencil,
  Plus,
  Power,
  RotateCcw,
  Search,
  Shield,
  SlidersHorizontal,
  Sparkles,
  Target,
  Trash2,
  Wand2,
  X,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type FocusEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
  ModelMetadata,
  Route,
  RouteMember,
  RoutingCandidate,
} from "../api/types";
import { ActionMenu, type ActionMenuItem } from "../components/ActionMenu";
import { rowContextPoint, rowKeyboardContextPoint } from "../components/contextMenu";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { PaginationBar } from "../components/PaginationBar";
import { EntityState } from "../components/EntityState";
import { TelemetryStrip } from "../components/TelemetryStrip";
import {
  Button,
  ConfirmDialog,
  Dialog,
  Empty,
  Page,
  PageActions,
  Panel,
  InfoTip,
  StatusBadge,
} from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useToast } from "../toast";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { TryPanel } from "./TryPanel";
import { positiveId } from "../lib/positiveId";
import { formatTokens } from "../lib/format";
import { useCooldownExpiry } from "../lib/cooldownClock";
import { modelGroup } from "./models/modelGroups";
import { ModelMetadataDialog } from "./models/ModelMetadataDialog";
import { RouteDialog } from "./models/RouteDialog";
import { MemberDialog } from "./models/MemberDialog";
import { UnifyDialog } from "./models/UnifyDialog";
import { UnifyHistory } from "./models/UnifyHistory";
import { ProbeDialog } from "./models/ProbeDialog";
import { ModelChangesPanel } from "./models/ModelChangesPanel";
import { AutoMatchMembersDialog } from "./models/AutoMatchMembersDialog";
import { CapabilityRegistryDialog } from "./models/CapabilityRegistry";

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

// Tab-scoped persistence for the models workspace. The sidebar navigates to
// bare /models (no query params), so URL-only state is lost on page switches;
// these helpers keep the current tab's search/selection across navigation.
function readTabState<T>(key: string, fallback: T): T {
  try {
    const raw = sessionStorage.getItem(`models.${key}`);
    return raw != null ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

function writeTabState<T>(key: string, value: T) {
  try {
    sessionStorage.setItem(`models.${key}`, JSON.stringify(value));
  } catch {
    // Storage unavailable; state stays in memory for this render.
  }
}
import { CooldownHint } from "./models/CooldownHint";
import { countActiveModelFilters } from "./models/modelFilters";
import {
  primaryMember,
  sortMembers,
  isActiveCooldown,
  candidateState,
  memberFinance,
  getEffectiveRoutingPolicy,
  originModelOf,
} from "./models/routingPolicy";

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
  const groupParam = params.get("group")?.trim() ?? "";

  return (
    <Page
      kicker={t("modelsPage.kicker")}
      title={t("modelsPage.title")}
      description={t("modelsPage.description")}
    >
      <ModelCatalog
        initialModel={modelParam}
        channelId={channelId}
        initialGroup={groupParam}
      />
    </Page>
  );
}

function ModelCatalog({
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
  // Models a plugin answers for. They have no route of their own — a route hook
  // rewrites the request before selection — so they never appear in the route
  // list above, and without this the console gives no sign that a model the
  // downstream catalogue advertises exists at all.
  const pluginHooks = useQuery({
    queryKey: ["plugins-hooks"],
    queryFn: ({ signal }) => service.pluginHooks(signal),
    refetchInterval: 60_000,
  });
  const virtualModels = useMemo(() => {
    const hooks = pluginHooks.data?.hooks ?? [];
    const out: Array<{ model: string; pluginId: string; pluginName: string }> = [];
    const seen = new Set<string>();
    for (const hook of hooks) {
      if (hook.point !== "route") continue;
      for (const pattern of hook.match_models ?? []) {
        // A wildcard is a matcher, not a callable model name: same rule the
        // gateway applies when it builds the downstream catalogue.
        if (!pattern || /[*?]/.test(pattern) || seen.has(pattern)) continue;
        seen.add(pattern);
        out.push({
          model: pattern,
          pluginId: hook.plugin_id,
          pluginName: hook.plugin_name || hook.plugin_id,
        });
      }
    }
    return out.sort((a, b) => a.model.localeCompare(b.model));
  }, [pluginHooks.data]);
  const metaByModel = useMemo(() => {
    const map = new Map<string, ModelMetadata>();
    for (const item of metadata.data?.items ?? []) {
      map.set(item.model_name, item);
    }
    return map;
  }, [metadata.data]);

  // URL wins over tab-scoped state on first mount; tab state survives the
  // bare-path sidebar navigation that drops the query string. Read once, so the
  // states and the filter panel's default-open decision cannot disagree.
  const [initialFilters] = useState(() => ({
    query: initialModel || readTabState("query", ""),
    channel: channelIdFromUrl ?? readTabState("channel", 0),
    group: initialGroup || readTabState("group", ""),
    status: readTabState<"enabled" | "disabled" | "all">("status", "enabled"),
  }));
  const [selected, setSelected] = useState<number | null>(() =>
    readTabState<number | null>("selected", null),
  );
  const [query, setQuery] = useState(initialFilters.query);
  const [channelFilter, setChannelFilter] = useState(initialFilters.channel);
  const [groupFilter, setGroupFilter] = useState(initialFilters.group);
  const [statusFilter, setStatusFilter] = useState<"enabled" | "disabled" | "all">(
    initialFilters.status,
  );
  const [showAdvanced, setShowAdvanced] = useState(true);
  const [changesOpenRequest, setChangesOpenRequest] = useState(0);
  /**
   * These three filters remember themselves across navigation but live behind a
   * disclosure, so a restored filter used to narrow the list with nothing on
   * screen saying so — the model just looked missing. The panel therefore opens
   * when (and only when) a non-default filter is in force, which makes it
   * self-healing: clearing the filters closes it again on the next visit.
   */
  const [showModelFilters, setShowModelFilters] = useState(
    () => countActiveModelFilters(initialFilters) > 0,
  );
  /** Shown on the trigger so a collapsed panel still reports that the list is
   *  narrowed — see `countActiveModelFilters` for what counts. */
  const activeFilterCount = countActiveModelFilters({
    group: groupFilter,
    channel: channelFilter,
    status: statusFilter,
  });
  const [edit, setEdit] = useState<Partial<Route> | null>(null);
  const [editMeta, setEditMeta] = useState<ModelMetadata | null>(null);
  const [remove, setRemove] = useState<Route | null>(null);
  const [member, setMember] = useState<Partial<RouteMember> | null>(null);
  const [removeMember, setRemoveMember] = useState<RouteMember | null>(null);
  const [tryOpen, setTryOpen] = useState(false);
  const [unifyOpen, setUnifyOpen] = useState(false);
  const [unifyHistoryOpen, setUnifyHistoryOpen] = useState(false);
  const [probeOpen, setProbeOpen] = useState(false);
  const [capabilitiesOpen, setCapabilitiesOpen] = useState(false);
  /** Route whose "attach every channel serving this model" preview is open. */
  const [autoMatchRoute, setAutoMatchRoute] = useState<Route | null>(null);
  const [bulkSelect, setBulkSelect] = useState(false);
  const [selectedMemberIds, setSelectedMemberIds] = useState<Set<number>>(
    () => new Set(),
  );
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
  const [missingDismissed, setMissingDismissed] =
    useState(readMissingDismissed);
  const [contextMenu, setContextMenu] = useState<{
    routeId: number;
    top: number;
    left: number;
  } | null>(null);

  useEffect(() => {
    if (channelIdFromUrl) setChannelFilter(channelIdFromUrl);
  }, [channelIdFromUrl]);

  useEffect(() => writeTabState("query", query), [query]);
  useEffect(() => writeTabState("channel", channelFilter), [channelFilter]);
  useEffect(() => writeTabState("group", groupFilter), [groupFilter]);
  useEffect(() => writeTabState("status", statusFilter), [statusFilter]);
  useEffect(() => writeTabState("selected", selected), [selected]);

  const modelGroups = useMemo(() => {
    const groups = new Set<string>();
    for (const item of overviews.data ?? []) {
      const meta = metaByModel.get(item.route.model_pattern);
      groups.add(
        modelGroup(
          item.route.model_pattern,
          item.route.model_group,
          meta?.vendor,
        ),
      );
    }
    return [...groups].sort();
  }, [metaByModel, overviews.data]);

  const rows = useMemo(() => {
    const term = query.trim().toLowerCase();
    return (overviews.data ?? []).filter((item) => {
      const meta = metaByModel.get(item.route.model_pattern);
      if (
        groupFilter &&
        modelGroup(
          item.route.model_pattern,
          item.route.model_group,
          meta?.vendor,
        ) !== groupFilter
      )
        return false;
      if (statusFilter === "enabled" && !item.route.enabled) return false;
      if (statusFilter === "disabled" && item.route.enabled) return false;
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
  }, [channelFilter, groupFilter, metaByModel, overviews.data, query, statusFilter]);

  const pagination = useClientPagination(rows, 20, "models");
  const pageRows = pagination.pageItems;

  // Bulk selection over the routing table (current-page checkboxes); actions
  // resolve against the full filtered list so selections survive paging.
  const [bulkSelected, setBulkSelected] = useState<Set<number>>(new Set());
  const [bulkMode, setBulkMode] = useState(false);
  const exitBulkMode = () => {
    setBulkMode(false);
    setBulkSelected(new Set());
  };
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
  const toggleBulkSelected = (id: number) => {
    setBulkSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
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
      const results = await Promise.allSettled(
        ids.map((id) => service.deleteRoute(id)),
      );
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
  const financeItems = useMemo(
    () => finance.data?.items ?? [],
    [finance.data?.items],
  );
  const orderedMembers = useMemo(
    () => sortMembers(selectedMembers),
    [selectedMembers],
  );
  /** Normalized member groups; the active tab stays visible while empty. */
  const groupNames = useMemo(() => {
    const names = new Set<string>();
    for (const candidate of orderedMembers) {
      names.add(
        (candidate.member.group_name || "").trim() || "default",
      );
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
        (candidate) =>
          ((candidate.member.group_name || "").trim() || "default") ===
          activeGroup,
      ),
    [activeGroup, orderedMembers],
  );
  // `candidateState` reads the wall clock, so without this a member whose
  // cooldown has just elapsed keeps showing 冷却中 / offering 清除冷却 until the
  // 15s poll (or a page switch) lands. Re-render the row at the deadline
  // instead of waiting for unrelated state to change.
  useCooldownExpiry(
    visibleMembers.map((candidate) => candidate.member.cooldown_until),
  );
  const explain = useQuery({
    queryKey: ["explain", selected, activeGroup],
    queryFn: ({ signal }) =>
      service.explain(selectedRoute!.model_pattern, signal, activeGroup),
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
    explain.data?.retry_times_override ?? selectedRoute?.retry_times ?? runtimeSettings.data?.editable.retry_times;
  const effectiveChannelRetries =
    selectedRoute?.channel_retry_times ??
    runtimeSettings.data?.editable.channel_retry_times;
  const retryPolicyIsOverridden = selectedRoute?.retry_times != null;
  const channelRetryPolicyIsOverridden =
    selectedRoute?.channel_retry_times != null;
  /** The member pinned by routing_mode=single, when that mode is active. */
  const singleModePinned =
    selectedRoute?.routing_mode === "single"
      ? (selectedMembers.find(
          (candidate) => candidate.member.id === selectedRoute.single_member_id,
        ) ?? null)
      : null;
  const singleModeActive = selectedRoute?.routing_mode === "single";
  const singleModeApplies = Boolean(singleModePinned && explain.data?.candidates.some(
    (item) => item.candidate.member.id === singleModePinned.member.id,
  ));
  const pinOutsideGroup = Boolean(singleModePinned && explain.data && !singleModeApplies);
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
      value.id
        ? service.updateMember(value.id, value)
        : service.createMember(selected!, value),
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
        ...(singleMemberId !== undefined
          ? { single_member_id: singleMemberId }
          : {}),
      }),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    pendingIdOf: ({ route }) => route.id,
  });
  const toggleMember = useAdminMutation({
    mutationFn: (entry: RouteMember) =>
      service.updateMember(entry.id, { ...entry, enabled: !entry.enabled }),
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
            ...candidate.member,
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
    setSelectedMemberIds(
      new Set((orderedMembers ?? []).map((c) => c.member.id)),
    );
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
            ...entry,
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
    mutationFn: (to: string) =>
      service.copyMemberGroup(selected!, "default", to),
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
    if (
      scope &&
      event.relatedTarget instanceof Node &&
      scope.contains(event.relatedTarget)
    )
      return;
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
    const next = new URLSearchParams(params);
    next.set("route", String(routeId));
    setSearchParams(next, { replace: true });
  };

  const delMeta = useAdminMutation({
    mutationFn: (name: string) => service.deleteModelMetadata(name),
    invalidateKeys: [["model-metadata"]],
    toastOnError: false,
  });
  const saveMeta = useAdminMutation({
    mutationFn: (value: ModelMetadata) =>
      service.upsertModelMetadata(value.model_name, value),
    invalidateKeys: [["model-metadata"]],
    toastOnError: false,
  });

  const modelActions = (
    route: Route,
    options?: { closeContext?: boolean },
  ): ActionMenuItem[] => {
    const busy = toggleRoute.pendingId === route.id || del.isPending;
    const close = () => {
      if (options?.closeContext) setContextMenu(null);
    };
    const items: ActionMenuItem[] = [
      {
        key: "bulk",
        label: t("modelsPage.bulkMode"),
        icon: <ListChecks size={14} />,
        disabled: busy,
        onSelect: () => {
          close();
          setBulkMode(true);
          setBulkSelected((current) => new Set(current).add(route.id));
        },
      },
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
    const ranks: Record<string, number> = { try: 0, logs: 0, meta: 1, edit: 1, toggle: 1, bulk: 2, delete: 3 };
    const sections = ["actions.view", "actions.manage", "actions.selection", "actions.danger"];
    return items.sort((a, b) => (ranks[a.key] ?? 1) - (ranks[b.key] ?? 1)).map((item) => ({
      ...item, group: t(sections[ranks[item.key] ?? 1]!),
      disabledReason: item.disabled ? t("common.working") : undefined,
    }));
  };

  const total = overviews.data?.length ?? 0;
  const enabledCount = (overviews.data ?? []).filter(
    (o) => o.route.enabled,
  ).length;

  return (
    <div className="ops-canvas models-catalog">
      <ModelChangesPanel openRequest={changesOpenRequest} hideWhenQuiet />
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
              : (overviews.data ?? []).filter(
                  (o) => (o.members ?? []).length > 1,
                ).length,
            tone: "info",
          },
        ]}
      />

      {/* Shown when affinity is on by default OR when something is actually
          bound: with the global switch off, a single model can still opt in
          per route (routes.sticky_session), and those live bindings are the
          only place that is visible. The explicit `sticky.data &&` is what
          narrows the payload for the reads below. */}
      {sticky.data &&
      (sticky.data.enabled || sticky.data.stats.bound_sessions > 0) ? (
        <Panel
          className="sticky-panel"
          title={t("sticky.title")}
          titleHelp={t("sticky.hint")}
          collapsible
          defaultOpen={false}
          storageKey="models.sticky"
          summary={
            <>
              <strong>{sticky.data.stats.bound_sessions}</strong>{" "}
              {t("sticky.bound")}
              <span className="panel-summary-sep" aria-hidden="true">
                ·
              </span>
              <strong>{sticky.data.stats.hits}</strong> {t("sticky.hits")}
            </>
          }
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
              <ActionMenu label={t("modelsPage.tools")} items={[
                { key: "capabilities", label: t("workbench.cap.title"), icon: <SlidersHorizontal size={14} />, onSelect: () => setCapabilitiesOpen(true) },
                { key: "unify", label: t("modelsPage.unify.action"), icon: <Combine size={14} />, onSelect: () => setUnifyOpen(true) },
                { key: "history", label: t("modelsPage.unify.history.action"), icon: <History size={14} />, onSelect: () => setUnifyHistoryOpen(true) },
                { key: "upstream-changes", label: t("modelChanges.title"), icon: <RefreshCw size={14} />, onSelect: () => setChangesOpenRequest((value) => value + 1) },
              ]} />
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
      <div className="split models-split">
        <Panel
          className="ops-list-panel model-directory"
          title={t("modelsPage.listTitle")}
        >
          <div className="models-simple-toolbar">
            <label className="directory-search models-search">
              <Search size={14} aria-hidden="true" />
              <input
                value={query}
                onChange={(event) => {
                  const nextQuery = event.target.value;
                  setQuery(nextQuery);
                  const next = new URLSearchParams(params);
                  if (nextQuery) next.set("model", nextQuery);
                  else next.delete("model");
                  next.delete("route");
                  setSearchParams(next, { replace: true });
                }}
                placeholder={t("routing.searchPlaceholder")}
                aria-label={t("routing.searchPlaceholder")}
              />
            </label>
            <Button variant="quiet" aria-expanded={showModelFilters} onClick={() => setShowModelFilters(!showModelFilters)}>
              {activeFilterCount > 0
                ? t("modelsPage.filtersActive", { n: activeFilterCount })
                : t("modelsPage.filters")}
            </Button>
            <div className="models-filter-options" hidden={!showModelFilters}>
            <select
              aria-label={t("modelsPage.groupFilter")}
              value={groupFilter}
              onChange={(event) => {
                const nextGroup = event.target.value;
                setGroupFilter(nextGroup);
                const next = new URLSearchParams(params);
                if (nextGroup) next.set("group", nextGroup);
                else next.delete("group");
                next.delete("route");
                setSearchParams(next, { replace: true });
              }}
            >
              <option value="">{t("modelsPage.allGroups")}</option>
              {modelGroups.map((group) => (
                <option key={group} value={group}>
                  {group}
                </option>
              ))}
            </select>
            <select
              aria-label={t("ops.filterChannel")}
              value={channelFilter}
              onChange={(event) => {
                const next = Number(event.target.value) || 0;
                setChannelFilter(next);
                const nextParams = new URLSearchParams(params);
                if (next > 0) nextParams.set("channel_id", String(next));
                else nextParams.delete("channel_id");
                nextParams.delete("route");
                setSearchParams(nextParams, { replace: true });
              }}
            >
              <option value={0}>{t("ops.allChannels")}</option>
              {(channels.data ?? []).map((channel) => (
                <option key={channel.id} value={channel.id}>
                  {channel.name}
                </option>
              ))}
            </select>
            <select
              aria-label={t("modelsPage.statusFilter")}
              value={statusFilter}
              onChange={(event) => {
                const next = event.target.value as "enabled" | "disabled" | "all";
                setStatusFilter(next);
                const nextParams = new URLSearchParams(params);
                nextParams.delete("route");
                setSearchParams(nextParams, { replace: true });
              }}
            >
              <option value="enabled">{t("common.enabled")}</option>
              <option value="disabled">{t("common.disabled")}</option>
              <option value="all">{t("modelsPage.statusAll")}</option>
            </select>
            </div>
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
                    {/* The recommended path: adopt models from the channel's
                        model settings — routes are created automatically.
                        Manual route creation stays available but demoted. */}
                    <Link className="button" to="/channels">
                      {t("modelsPage.ctaConnections")}
                    </Link>
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
              {bulkMode && bulkSelected.size > 0 ? (
                <div className="toolbar bulk-bar">
                  <span className="live-trace-count">
                    {t("modelsPage.bulkSelected", { n: bulkSelected.size })}
                  </span>
                  <Button
                    variant="secondary"
                    disabled={bulkBusy}
                    onClick={() =>
                      bulkToggleRoutes.mutate({
                        ids: [...bulkSelected],
                        enabled: true,
                      })
                    }
                  >
                    {t("modelsPage.bulkEnableSelected")}
                  </Button>
                  <Button
                    variant="secondary"
                    disabled={bulkBusy}
                    onClick={() =>
                      bulkToggleRoutes.mutate({
                        ids: [...bulkSelected],
                        enabled: false,
                      })
                    }
                  >
                    {t("modelsPage.bulkDisableSelected")}
                  </Button>
                  {/* Irreversible, so it must not look identical to the two
                      reversible toggles beside it. */}
                  <Button
                    variant="danger"
                    disabled={bulkBusy}
                    onClick={() => setBulkDeleteOpen(true)}
                  >
                    {t("modelsPage.bulkDeleteSelected")}
                  </Button>
                  <Button
                    variant="quiet"
                    onClick={() => setBulkSelected(new Set())}
                  >
                    {t("modelsPage.bulkClear")}
                  </Button>
                  <Button variant="quiet" onClick={exitBulkMode}>
                    {t("modelsPage.bulkDone")}
                  </Button>
                </div>
              ) : null}
              <div className="table-wrap model-directory-wrap">
                <table className={bulkMode ? "model-directory-table is-bulk" : "model-directory-table"}>
                  <thead>
                    <tr>
                      {bulkMode ? (
                        <th className="bulk-cell">
                          <input
                            type="checkbox"
                            aria-label={t("modelsPage.bulkSelectPage")}
                            checked={pageAllSelected}
                            onChange={selectAllPage}
                          />
                        </th>
                      ) : null}
                      <th>{t("common.model")}</th>
                      <th className="model-upstream-header">{t("modelsPage.col.upstream")}</th>
                      <th className="status-col">{t("common.status")}</th>
                      <th className="actions">{t("common.actions")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {/* Plugin-answered models have no route and no members, so
                        they cannot be selected or opened like a route. They
                        are still real callable names — the downstream
                        catalogue advertises them — so they belong in this list
                        with the plugin named as their owner. */}
                    {(query.trim()
                      ? virtualModels.filter((item) =>
                          item.model.toLowerCase().includes(query.trim().toLowerCase()),
                        )
                      : virtualModels
                    ).map((item) => (
                      <tr key={`plugin-model-${item.model}`} className="is-plugin-model">
                        {bulkMode ? <td className="bulk-cell" /> : null}
                        <td className="model-row-name">
                          <strong className="mono" title={item.model}>
                            {item.model}
                          </strong>
                          <span className="model-meta-badge is-group">
                            {t("modelsPage.pluginModel")}
                          </span>
                        </td>
                        <td className="model-row-upstream">
                          <span className="plugin-model-owner">{item.pluginName}</span>
                        </td>
                        <td className="status-col model-row-status">
                          <StatusBadge value="enabled" />
                        </td>
                        <td className="actions">
                          <Button
                            variant="quiet"
                            onClick={() =>
                              navigate(`/console/plugins/${encodeURIComponent(item.pluginId)}`)
                            }
                          >
                            {t("modelsPage.pluginModelOpen")}
                          </Button>
                        </td>
                      </tr>
                    ))}
                    {pageRows.map((item) => {
                      const active = item.route.id === selected;
                      const meta = metaByModel.get(item.route.model_pattern);
                      const group = modelGroup(
                        item.route.model_pattern,
                        item.route.model_group,
                        meta?.vendor,
                      );
                      const head = primaryMember(item.members, item.route);
                      const ready = item.members.filter(
                        (entry) => candidateState(entry) === "ready",
                      ).length;
                      const rowBusy =
                        toggleRoute.pendingId === item.route.id ||
                        del.isPending;
                      return (
                        <tr
                          key={item.route.id}
                          tabIndex={0}
                          className={`is-clickable${active ? " is-selected" : ""}`}
                          onClick={() => selectRow(item.route.id)}
                          onContextMenu={(event) => {
                            const point = rowContextPoint(event);
                            if (!point) return;
                            selectRow(item.route.id);
                            setContextMenu({
                              routeId: item.route.id,
                              ...point,
                            });
                          }}
                          onKeyDown={(event) => {
                            const point = rowKeyboardContextPoint(event);
                            if (point) { selectRow(item.route.id); setContextMenu({ routeId: item.route.id, ...point }); }
                          }}
                        >
                          {bulkMode ? (
                            <td
                              className="bulk-cell"
                              onClick={(event) => event.stopPropagation()}
                            >
                              <input
                                type="checkbox"
                                aria-label={t("modelsPage.bulkSelectOne", {
                                  model: item.route.model_pattern,
                                })}
                                checked={bulkSelected.has(item.route.id)}
                                onChange={() => toggleBulkSelected(item.route.id)}
                              />
                            </td>
                          ) : null}
                          <td className="model-row-name">
                            <strong className="mono" title={item.route.model_pattern}>
                              {item.route.model_pattern}
                            </strong>
                            <small className="model-nav-provider">{head ? head.channel.name : t("modelsPage.noUpstream")}</small>
                            <span className="model-meta-badge is-group">
                              {group}
                            </span>
                            {item.route.image_edit_shim ? (
                              // Only meaningful on routes whose model has no
                              // images endpoint of its own, so it stays a hint
                              // rather than a status.
                              <span
                                className="model-meta-badge is-shim"
                                title={t("routing.imageEditShimHint")}
                              >
                                {t("modelsPage.metaImageEdit")}
                              </span>
                            ) : null}
                            {(() => {
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
                          <td className="model-row-upstream">
                            {head ? (
                              <>
                                <Link
                                  to={`/channels?channel=${head.channel.id}`}
                                  className="upstream-link"
                                  title={t("modelsPage.openChannelHint")}
                                  onClick={(event) => {
                                    // Don't trigger the row's selectRow when
                                    // jumping straight to the channel editor.
                                    event.stopPropagation();
                                  }}
                                >
                                  {head.channel.name}
                                </Link>
                                {item.members.length > 1
                                  ? ` +${item.members.length - 1}`
                                  : ""}
                              </>
                            ) : (
                              <span className="muted">
                                {t("modelsPage.noUpstream")}
                              </span>
                            )}
                          </td>
                          <td className="status-col model-row-status">
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
                              title={item.route.model_pattern}
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
                      key={overview.route.id}
                      label={t("common.moreActions")}
                      title={overview.route.model_pattern}
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
                  <small
                    title={`${t("modelsPage.memberSummaryHint")} ${t("modelsPage.scopeHint")}`}
                  >
                    {primary
                      ? t(singleModePinned ? "modelsPage.pinnedMember" : "modelsPage.servedBy", {
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

              {/* Two clusters, not five loose controls: the primary action
                  (run a call) on the left, the settings group — routing mode
                  and the overflow menu — pinned right. The two bare (i) icons
                  that used to float here now live on the elements they
                  explain: the mode hint on the mode control, the scope note on
                  the summary line under the title. */}
              <div className="detail-primary-bar">
                <Button
                  icon={<Sparkles size={14} />}
                  onClick={() => setTryOpen(true)}
                >
                  {t("try.open")}
                </Button>
                <span className="bar-spacer" />
                <div
                  className="routing-mode-control"
                  title={t("modelsPage.scopeHint")}
                >
                  <span>{t("routing.mode.label")}</span>
                  <InfoTip label={t("routing.modeHint")} />
                  <select
                    className="routing-mode-select"
                    aria-label={t("routing.mode.label")}
                    value={selectedRoute.routing_mode || "auto"}
                    disabled={saveRoutingMode.pendingId === selectedRoute.id}
                    onChange={(event) => {
                      const next = event.target.value;
                      if (next === "single") {
                        // Manual single selection pins the top member; the
                        // per-member menu pins a specific channel.
                        const top =
                          selectedMembers.find((c) => c.member.enabled) ??
                          selectedMembers[0];
                        saveRoutingMode.mutate({
                          route: selectedRoute,
                          mode: next,
                          singleMemberId:
                            selectedRoute.single_member_id ??
                            top?.member.id ??
                            null,
                        });
                        return;
                      }
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
                    <option value="single">{t("routing.mode.single")}</option>
                  </select>
                </div>
                <ActionMenu
                  compact
                  label={t("common.moreActions")}
                  disabled={toggleRoute.pendingId === selectedRoute.id}
                  items={modelActions(selectedRoute)}
                />
              </div>


              {singleModeActive && selectedRoute ? (
                <div className="single-mode-banner">
                  <Target size={15} />
                  <div className="single-mode-banner-body">
                    <strong>
                      {t("routing.singleModeBanner", {
                        name: singleModePinned
                          ? singleModePinned.channel.name
                          : t("routing.singleModeMissingName"),
                      })}
                    </strong>
                    <small>
                      {singleModePinned
                        ? t(pinOutsideGroup ? "routing.singleModeGroupMissing" : "routing.singleModeHint")
                        : t("routing.singleModeMissing")}
                      {singleModePinned && !pinOutsideGroup && !singleModePinned.member.enabled
                        ? ` ${t("routing.singleModeDisabledWarning")}`
                        : ""}
                    </small>
                  </div>
                  <Button
                    variant="secondary"
                    disabled={pinMember.isPending}
                    onClick={() =>
                      pinMember.mutate({
                        route: selectedRoute,
                        memberId: null,
                      })
                    }
                  >
                    {t("routing.singleModeRestore")}
                  </Button>
                </div>
              ) : null}

              <div className="member-section-heading">
                <button type="button" aria-expanded={showAdvanced} onClick={() => setShowAdvanced((value) => !value)}>
                  {t("modelsPage.members")}<ChevronDown size={14} className={showAdvanced ? "chevron-flip is-open" : "chevron-flip"} />
                </button>
                <InfoTip label={t("modelsPage.routingHint") + " " + t("routing.reorderHint") + " " + t("routing.groupTabsHint")} />
              </div>
              {showAdvanced ? (
                <section className="models-advanced">
                  <div className="models-advanced-bar">
                    <Button
                      variant="secondary"
                      icon={<Plus size={14} />}
                      onClick={openAddMember}
                    >
                      {t("routing.addMember")}
                    </Button>
                    {/* The bulk of what "add member" does over and over: one
                        click attaches every enabled channel that really
                        serves this model to the group being viewed. */}
                    <Button
                      variant="secondary"
                      icon={<Wand2 size={14} />}
                      title={t("modelsPage.autoMatchAddHint")}
                      onClick={() => setAutoMatchRoute(selectedRoute)}
                    >
                      {t("modelsPage.autoMatchAdd")}
                    </Button>
                    <span className="bar-spacer" />
                    <Button
                      variant={bulkSelect ? "primary" : "secondary"}
                      onClick={() => {
                        setBulkSelect((value) => !value);
                        setSelectedMemberIds(new Set());
                      }}
                    >
                      {t("routing.bulkSelect")}
                    </Button>
                  </div>
                  {bulkSelect && selectedMembers.length > 0 ? (
                    <div className="routing-bulk-bar">
                      <span className="routing-bulk-count">
                        {t("routing.bulkSelected", {
                          count: selectedMemberIds.size,
                        })}
                      </span>
                      <Button
                        variant="secondary"
                        disabled={selectedMemberIds.size === 0}
                        onClick={() =>
                          bulkToggleMembers.mutate({ enabled: true })
                        }
                      >
                        {t("routing.bulkEnable")}
                      </Button>
                      <Button
                        variant="secondary"
                        disabled={selectedMemberIds.size === 0}
                        onClick={() =>
                          bulkToggleMembers.mutate({ enabled: false })
                        }
                      >
                        {t("routing.bulkDisable")}
                      </Button>
                      <Button variant="secondary" onClick={selectAllMembers}>
                        {t("routing.bulkSelectAll")}
                      </Button>
                      <Button
                        variant="secondary"
                        onClick={clearMemberSelection}
                      >
                        {t("routing.bulkClear")}
                      </Button>
                    </div>
                  ) : null}
				  {reorderMembers.isPending ? (
				    <div className="routing-reorder-hint">
				      <span>
				        {reorderMembers.isPending
				          ? t("routing.savingOrder")
				          : t("routing.reorderHint")}
				      </span>
				    </div>
				  ) : null}
                  <div className="member-group-tabs">
                    <div
                      className="member-group-tablist"
                      role="tablist"
                      aria-label={t("routing.memberGroupLabel")}
                    >
                      {groupNames.map((name) => {
                        const active = name === activeGroup;
                        const count = groupCounts.get(name) ?? 0;
                        return (
                          <div
                            key={name}
                            className={`member-group-tab${active ? " is-active" : ""}`}
                          >
                            <button
                              type="button"
                              role="tab"
                              aria-selected={active}
                              onClick={() => setActiveGroup(name)}
                            >
                              <span className="member-group-tab-main">
                                {name === "default"
                                  ? t("routing.groupDefault")
                                  : name}
                                <span className="member-group-count">
                                  {count}
                                </span>
                              </span>
                            </button>
                            {name !== "default" ? (
                              <ActionMenu
                                compact
                                label={t("common.moreActions")}
                                items={[
                                  {
                                    key: "rename",
                                    icon: <Pencil size={14} />,
                                    label: t("routing.groupRename"),
                                    onSelect: () =>
                                      setGroupDraft({
                                        mode: "rename",
                                        from: name,
                                        value: name,
                                      }),
                                  },
                                  {
                                    key: "delete",
                                    icon: <Trash2 size={14} />,
                                    label: t("routing.groupDelete"),
                                    danger: true,
                                    onSelect: () => setRemoveGroup(name),
                                  },
                                ]}
                              />
                            ) : null}
                          </div>
                        );
                      })}
                      {groupDraft ? (
                        <input
                          className="member-group-input"
                          autoFocus
                          value={groupDraft.value}
                          maxLength={64}
                          placeholder={
                            groupDraft.mode === "new"
                              ? t("routing.groupNewPlaceholder")
                              : t("routing.groupRenamePlaceholder")
                          }
                          onChange={(event) =>
                            setGroupDraft({
                              ...groupDraft,
                              value: event.target.value,
                            })
                          }
                          onKeyDown={(event) => {
                            if (event.key === "Enter") submitGroupDraft();
                            if (event.key === "Escape") setGroupDraft(null);
                          }}
                          onBlur={groupEditorBlur}
                        />
                      ) : (
                        <button
                          type="button"
                          className="member-group-add"
                          onClick={() =>
                            setGroupDraft({ mode: "new", value: "" })
                          }
                        >
                          <Plus size={12} />
                          {t("routing.groupNew")}
                        </button>
                      )}
                    </div>
                    {groupDraft?.mode === "new" ? (
                      <label className="member-group-copy">
                        <input
                          type="checkbox"
                          checked={groupDraft.copyDefault ?? false}
                          onChange={(event) =>
                            setGroupDraft({
                              ...groupDraft,
                              copyDefault: event.target.checked,
                            })
                          }
                          onBlur={groupEditorBlur}
                        />
                        <span>{t("routing.groupCopyDefault")}</span>
                      </label>
                    ) : null}
                  </div>
                  {explain.data?.group_fallback ? (
                    <p className="member-group-hint" role="status">
                      {explain.data.route_group
                        ? t("routing.groupFallback", { name: activeGroup, target: explain.data.route_group })
                        : t("routing.groupFallbackAll", { name: activeGroup })}
                    </p>
                  ) : null}
                  {!selectedMembers.length ? (
                    <Empty>{t("routing.noMembers")}</Empty>
                  ) : visibleMembers.length === 0 ? (
                    <div className="routing-group-empty">
                      <span>
                        {t("routing.groupEmpty", {
                          name:
                            activeGroup === "default"
                              ? t("routing.groupDefault")
                              : activeGroup,
                        })}
                      </span>
                      <Button
                        variant="secondary"
                        icon={<Plus size={14} />}
                        onClick={openAddMember}
                      >
                        {t("routing.addMember")}
                      </Button>
                    </div>
                  ) : (
                    visibleMembers.map((candidate, rowIndex) => {
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
                      const ordered = visibleMembers;
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
                          className={`member-row${dragMemberId === entry.id ? " is-dragging" : ""}${autoDisabled ? " is-auto-disabled" : ""}${bulkSelect && selectedMemberIds.has(entry.id) ? " is-selected" : ""}`}
                          key={entry.id}
                          draggable={
                            !reorderMembers.isPending &&
                            !bulkSelect
                          }
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
                            const current = sortMembers(visibleMembers);
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
                          {bulkSelect ? (
                            <label
                              className="member-bulk-check"
                              title={t("routing.bulkToggleSelect")}
                            >
                              <input
                                type="checkbox"
                                checked={selectedMemberIds.has(entry.id)}
                                onChange={() => toggleMemberSelect(entry.id)}
                              />
                            </label>
                          ) : (
                            <button
                              type="button"
                              className="member-drag-handle"
                              aria-label={t("routing.orderLabel")}
                              title={t("routing.reorderHint")}
                            >
                              <GripVertical size={16} />
                            </button>
                          )}
                          <div className="member-row-main">
                            <strong>
                              <button
                                type="button"
                                className="member-channel-link"
                                title={t("routing.openChannelModels")}
                                onClick={() =>
                                  navigate(
                                    `/models/channel/${candidate.channel.id}?model=${encodeURIComponent(
                                      originModelOf(entry, selectedRoute) ||
                                        selectedModel,
                                    )}`,
                                  )
                                }
                              >
                                {candidate.channel.name}
                              </button>
                              {originModelOf(entry, selectedRoute) ? (
                                <span
                                  className="member-origin-badge"
                                  title={t("routing.memberOriginHint")}
                                >
                                  {t("routing.memberOrigin", {
                                    model: originModelOf(
                                      entry,
                                      selectedRoute,
                                    ),
                                  })}
                                </span>
                              ) : null}
                            </strong>
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
                                    <Shield size={12} />{" "}
                                    {t("routing.protectedLabel")}
                                  </span>
                                </>
                              ) : null}
                              {memberFinance(
                                entry,
                                selectedModel,
                                financeItems,
                              ) ? (
                                (() => {
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
									</>
                                  );
                                })()
                              ) : (
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
                            {singleModeActive &&
                            selectedRoute?.single_member_id === entry.id ? (
                              <span
                                className="member-pin-chip"
                                title={t("routing.singleModeBanner", {
                                  name: candidate.channel.name,
                                })}
                              >
                                <Target size={11} />
                                {t("routing.pinChip")}
                              </span>
                            ) : null}
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
                                busy ||
                                rowIndex >= ordered.length - 1
                              }
                              onClick={() => moveBy(1)}
                            >
                              ↓
                            </button>
                            <ActionMenu
                              compact
                              label={t("common.moreActions")}
                              disabled={busy || bulkSelect}
                              items={[
                                {
                                  key: "toggle",
                                  icon: <Power size={14} />,
                                  label: entry.enabled
                                    ? t("common.disableAction")
                                    : t("common.enableAction"),
                                  onSelect: () => toggleMember.mutate(entry),
                                },
                                ...(orderedMembers.length > 1
                                  ? [
                                      {
                                        key: "solo",
                                        icon: <Target size={14} />,
                                        label:
                                          selectedRoute?.routing_mode ===
                                            "single" &&
                                          selectedRoute.single_member_id ===
                                            entry.id
                                            ? t("routing.unsoloMember")
                                            : t("routing.soloMember"),
                                        disabled: pinMember.isPending,
                                        onSelect: () => {
                                          if (
                                            selectedRoute?.routing_mode ===
                                              "single" &&
                                            selectedRoute.single_member_id ===
                                              entry.id
                                          ) {
                                            pinMember.mutate({
                                              route: selectedRoute,
                                              memberId: null,
                                            });
                                          } else if (selectedRoute) {
                                            pinMember.mutate({
                                              route: selectedRoute,
                                              memberId: entry.id,
                                            });
                                          }
                                        },
                                      },
                                    ]
                                  : []),
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
              <details className="route-policy-disclosure">
                <summary>{t("routing.effectivePolicy")}<ChevronDown size={13} /></summary>
              <div className="routing-policy-card">
                <div className="routing-policy-summary">
                  <span className="routing-policy-title">
                    {t("routing.effectivePolicy")}
                  </span>
                  {effectivePolicy ? (
                    <>
                      <span
                        className={`routing-signal${effectivePolicy.latency ? " is-on" : " is-off"}`}
                      >
                        {t("routing.signal.latency")}:{" "}
                        {effectivePolicy.latency
                          ? t("routing.signal.on")
                          : t("routing.signal.off")}
                      </span>
                      <span
                        className={`routing-signal${effectivePolicy.error ? " is-on" : " is-off"}`}
                      >
                        {t("routing.signal.error")}:{" "}
                        {effectivePolicy.error
                          ? t("routing.signal.on")
                          : t("routing.signal.off")}
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
                        singleModeApplies ? "routing.policySource.single" : retryPolicyIsOverridden
                          ? "routing.policySource.model"
                          : "routing.policySource.global",
                      )}
                    </small>
                  </span>
                  <span className="routing-policy-value">
                    {t("routing.channelRetry")}:{" "}
                    {effectiveChannelRetries ?? "?"}
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
                    {t("routing.failover")}:{" "}
                    {runtimeSettings.data
                      ? runtimeSettings.data.editable
                          .cross_channel_failover_enabled
                        ? t("routing.signal.on")
                        : t("routing.signal.off")
                      : "?"}
                  </span>
                </div>
              </div>
              </details>
            </>
          )}
        </div>
      </div>

      {unifyOpen ? <UnifyDialog onClose={() => setUnifyOpen(false)} /> : null}
      {unifyHistoryOpen ? (
        <UnifyHistory onClose={() => setUnifyHistoryOpen(false)} />
      ) : null}
      {probeOpen ? <ProbeDialog onClose={() => setProbeOpen(false)} /> : null}
      {capabilitiesOpen ? (
        <CapabilityRegistryDialog onClose={() => setCapabilitiesOpen(false)} />
      ) : null}
      {autoMatchRoute && selectedRoute ? (
        <AutoMatchMembersDialog
          route={autoMatchRoute}
          group={activeGroup}
          attachedChannelIds={visibleMembers.map(
            (candidate) => candidate.channel.id,
          )}
          onClose={() => setAutoMatchRoute(null)}
        />
      ) : null}
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
