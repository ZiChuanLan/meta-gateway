import { ArrowLeft, Plus, Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  Fragment,
  useMemo,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { useToast } from "../toast";
import type {
  Channel,
  DiscoveredModel,
  RouteMember,
  RouteOverview,
} from "../api/types";
import { EntityState } from "../components/EntityState";
import { TelemetryStrip } from "../components/TelemetryStrip";
import {
  Button,
  ConfirmDialog,
  ErrorState,
  Page,
  Panel,
  StatusBadge,
} from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { MODEL_GROUP_ORDER, autoModelGroup } from "./models/modelGroups";
import { positiveId } from "../lib/positiveId";
import type { ModelSyncMode } from "./channels/SyncModePicker";

const INVALIDATE = [
  ["channel-overviews"],
  ["channels"],
  ["routes"],
  ["route-overviews"],
  ["discovered-models"],
  ["members"],
  ["models"],
] as const;

/**
 * Per-channel model management UI: enable/disable + outward aliases.
 * Reusable inside the edit drawer or on a standalone page.
 */
export function ChannelModelsPanel({
  channelId,
  header,
  initialQuery,
}: {
  channelId: number;
  header?: ReactNode;
  // Seeds the search box so a deep link (?model=…) lands filtered on that
  // model — e.g. arriving from a routing member row.
  initialQuery?: string;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const service = api(client!);

  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: ({ signal }) => service.channels(signal),
  });
  const channel: Channel | undefined = (channels.data ?? []).find(
    (entry) => entry.id === channelId,
  );

  const discovered = useQuery({
    queryKey: ["discovered-models", channelId],
    queryFn: ({ signal }) => service.discoveredModels(channelId, signal),
  });
  const routeOverviews = useQuery({
    queryKey: ["route-overviews"],
    queryFn: ({ signal }) => service.routeOverviews(signal),
  });

  const models = useMemo<DiscoveredModel[]>(
    () => discovered.data ?? [],
    [discovered.data],
  );
  const [customName, setCustomName] = useState("");
  const [aliasInputs, setAliasInputs] = useState<Record<number, string>>({});
  const [query, setQuery] = useState(initialQuery ?? "");
  // Narrow the list by adoption state: enabled rows (member on and on),
  // disabled rows (parked/never adopted), or everything.
  const [statusFilter, setStatusFilter] = useState<
    "all" | "enabled" | "disabled"
  >("all");
  const [selectedIds, setSelectedIds] = useState<Set<string>>(() => new Set());
  const [bulkMode, setBulkMode] = useState(false);
  const selectAllRef = useRef<HTMLInputElement>(null);

  // Vendor groups start collapsed so a long candidate list stays scannable.
  // Search or bulk mode forces them open, otherwise hidden rows would read
  // as empty results.
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(
    () => new Set(MODEL_GROUP_ORDER),
  );
  const toggleGroup = (group: string) =>
    setCollapsedGroups((previous) => {
      const next = new Set(previous);
      if (next.has(group)) next.delete(group);
      else next.add(group);
      return next;
    });
  const forcedOpen = query.trim() !== "" || bulkMode;

  // mappingReal parses a {"real":"…"} mapping value; empty when absent.
  const mappingReal = (raw: string | undefined): string => {
    if (!raw) return "";
    try {
      const parsed = JSON.parse(raw) as { real?: string };
      return parsed.real ?? "";
    } catch {
      return "";
    }
  };

  // aliasFor returns the alias binding for a real model on this channel:
  // the (route, member) pair whose member carries {"real": realModel}. New
  // aliases store the mapping on the member (shared aliases: several models/
  // channels may reuse one route name and rewrite to their own real model);
  // legacy aliases stored it on the route, so that form is read as a fallback.
  const aliasFor = (
    realModel: string,
  ): { overview: RouteOverview; member: RouteMember } | undefined => {
    for (const overview of routeOverviews.data ?? []) {
      const mapped = (overview.members ?? []).find(
        (candidate) =>
          candidate.member.channel_id === channelId &&
          mappingReal(candidate.member.mapping_json) === realModel,
      );
      if (mapped) return { overview, member: mapped.member };
      if (
        mappingReal(overview.route.mapping_json) === realModel &&
        (overview.members ?? []).some(
          (candidate) => candidate.member.channel_id === channelId,
        )
      ) {
        const legacy = (overview.members ?? []).find(
          (candidate) => candidate.member.channel_id === channelId,
        );
        if (legacy) return { overview, member: legacy.member };
      }
    }
    return undefined;
  };

  const memberFor = (realModel: string): RouteMember | undefined => {
    // When an alias is active the channel's binding lives on the alias
    // route (the original-name member was retired when the alias was
    // saved), so the toggle must control that member.
    const alias = aliasFor(realModel);
    if (alias) {
      return alias.member;
    }
    const route = (routeOverviews.data ?? []).find(
      (overview) => overview.route.model_pattern === realModel,
    );
    return route?.members.find(
      (candidate) => candidate.member.channel_id === channelId,
    )?.member;
  };

  // Shared route cleanup after a binding removal: a route only disappears
  // when the deleted member was its last one.
  const deleteMemberAndEmptyRoute = async (member: RouteMember) => {
    await service.deleteMember(member.id);
    const overview = (routeOverviews.data ?? []).find((e) =>
      (e.members ?? []).some((c) => c.member.id === member.id),
    );
    const remaining = (overview?.members ?? []).filter(
      (c) => c.member.id !== member.id,
    );
    if ((overview?.members ?? []).length > 0 && remaining.length === 0) {
      await service.deleteRoute(overview!.route.id);
    }
  };

  const toggleMember = useAdminMutation({
    mutationFn: async (member: RouteMember) => {
      // Manual-sync channels: unchecking removes the binding outright —
      // the checklist IS the routing state, and parking would pile up
      // disabled rows forever. Plain bindings only: alias mappings are
      // deliberate configuration, they park like on auto channels
      // (reconcile respects parked members; a deleted one would just be
      // re-adopted on the next sync anyway).
      if (
        channel?.model_sync_mode === "manual" &&
        member.enabled &&
        !member.mapping_json
      ) {
        await deleteMemberAndEmptyRoute(member);
        return;
      }
      await service.updateMember(member.id, {
        ...member,
        enabled: !member.enabled,
      });
    },
    invalidateKeys: [...INVALIDATE],
    pendingIdOf: (member) => member.id,
  });

  // adoptModel wires one snapshot model into routing: reuse the route when its
  // name already exists (routes are global), otherwise create it, then attach
  // this channel's member. Adopted members share the exact parameters of
  // Reconcile-created ones (auto=1, manual_override=0), so upstream removals
  // recycle them through the same stale cleanup as auto-sync members.
  const adoptModel = async (realModel: string) => {
    if (channelId == null) return;
    const target = (routeOverviews.data ?? []).find(
      (entry) => entry.route.model_pattern === realModel,
    );
    const routeId =
      target?.route.id ??
      (await service.createRoute({ model_pattern: realModel, enabled: true }))
        .id;
    await service.createMember(routeId, {
      channel_id: channelId,
      priority: 0,
      weight: 100,
      enabled: true,
      auto: true,
      manual_override: false,
    });
  };

  const bulkToggle = useAdminMutation({
    mutationFn: async (input: {
      updates: { member: RouteMember; enabled: boolean }[];
      removals?: RouteMember[];
      adoptions?: string[];
    }) => {
      await Promise.all([
        ...input.updates.map(({ member, enabled }) =>
          service.updateMember(member.id, {
            ...member,
            enabled,
          }),
        ),
        ...(input.removals ?? []).map((member) =>
          deleteMemberAndEmptyRoute(member),
        ),
      ]);
      for (const name of input.adoptions ?? []) {
        await adoptModel(name);
      }
    },
    invalidateKeys: [...INVALIDATE],
  });

  const adopt = useAdminMutation({
    mutationFn: adoptModel,
    invalidateKeys: [...INVALIDATE],
  });

  // Parked bindings: disabled members on this channel that carry no alias
  // mapping. Cleanup removes them (and routes that become empty); alias
  // bindings stay because they are deliberate operator configuration.
  const parkedMembers = useMemo(() => {
    const out: { member: RouteMember; routeId: number }[] = [];
    for (const overview of routeOverviews.data ?? []) {
      if (overview.route.mapping_json) continue;
      for (const candidate of overview.members ?? []) {
        if (candidate.member.channel_id !== channelId) continue;
        if (candidate.member.enabled) continue;
        if (candidate.member.mapping_json) continue;
        out.push({ member: candidate.member, routeId: overview.route.id });
      }
    }
    return out;
  }, [routeOverviews.data, channelId]);

  const [cleanupOpen, setCleanupOpen] = useState(false);
  const [cleanupNote, setCleanupNote] = useState("");
  const cleanupParked = useAdminMutation({
    mutationFn: async () => {
      const targets = parkedMembers;
      const deleted = new Set(targets.map((t) => t.member.id));
      for (const { member } of targets) {
        await service.deleteMember(member.id);
      }
      // A route only disappears when this cleanup emptied it; routes with
      // any remaining member (other channels, aliases, enabled rows) stay.
      let routes = 0;
      for (const routeId of new Set(targets.map((t) => t.routeId))) {
        const overview = (routeOverviews.data ?? []).find(
          (e) => e.route.id === routeId,
        );
        const remaining = (overview?.members ?? []).filter(
          (c) => !deleted.has(c.member.id),
        );
        if ((overview?.members ?? []).length > 0 && remaining.length === 0) {
          await service.deleteRoute(routeId);
          routes += 1;
        }
      }
      return { members: targets.length, routes };
    },
    invalidateKeys: [...INVALIDATE],
    onSuccess: ({ members, routes }) => {
      setCleanupNote(t("channels.modelsCleanupDone", { members, routes }));
    },
  });

  // Single-field PATCH: the admin API preserves every key the body omits, so
  // flipping the sync mode from here cannot touch the rest of the channel.
  const setSyncMode = useAdminMutation({
    mutationFn: (mode: ModelSyncMode) =>
      service.updateChannel(channelId, { model_sync_mode: mode }),
    invalidateKeys: [...INVALIDATE],
  });

  const toast = useToast();

  const saveAlias = useAdminMutation({
    mutationFn: async (input: { realModel: string; alias: string }) => {
      const alias = input.alias.trim();
      if (!alias || channelId == null) return;
      const mapping = JSON.stringify({ real: input.realModel });
      const existing = aliasFor(input.realModel);
      // No existing alias and the alias equals the real name: nothing to do.
      if (alias === input.realModel && !existing) return;
      let aliasRouteId: number;

      // The alias name changed: drop this channel's mapped member from the
      // old route. The alias route may be shared by other channels/models, so
      // only this member is removed (the route itself is deleted only when it
      // has no members left).
      if (existing && existing.overview.route.model_pattern !== alias) {
        const remaining = (existing.overview.members ?? []).filter(
          (candidate) => candidate.member.id !== existing.member.id,
        );
        await service.deleteMember(existing.member.id);
        if (remaining.length === 0) {
          await service.deleteRoute(existing.overview.route.id);
        }
      }

      // Upsert this channel's alias member on the target route. The mapping
      // lives on the member, so several real models/channels may share one
      // alias name while the proxy rewrites to each member's own real model.
      const upsertMember = async (routeId: number) => {
        const overview = (routeOverviews.data ?? []).find(
          (entry) => entry.route.id === routeId,
        );
        const mine = (overview?.members ?? []).find(
          (candidate) =>
            candidate.member.channel_id === channelId &&
            mappingReal(candidate.member.mapping_json) === input.realModel,
        );
        if (mine) {
          if (mine.member.mapping_json !== mapping) {
            await service.updateMember(mine.member.id, {
              ...mine.member,
              mapping_json: mapping,
            });
          }
        } else {
          await service.createMember(routeId, {
            channel_id: channelId,
            priority: 0,
            weight: 100,
            enabled: true,
            auto: true,
            manual_override: true,
            mapping_json: mapping,
          });
        }
      };

      const target = (routeOverviews.data ?? []).find(
        (entry) => entry.route.model_pattern === alias,
      );
      if (target) {
        // Reuse the existing route (custom model name, or another real
        // model's alias): attach this channel as an additional member instead
        // of hitting the global UNIQUE(model_pattern) constraint.
        aliasRouteId = target.route.id;
        await upsertMember(aliasRouteId);
      } else {
        const created = await service.createRoute({
          model_pattern: alias,
          enabled: true,
        });
        aliasRouteId = created.id;
        await upsertMember(aliasRouteId);
      }

      // Retire the original model name for this channel: drop its member
      // on the original route; delete the route if it became empty.
      const original = (routeOverviews.data ?? []).find(
        (overview) =>
          overview.route.model_pattern === input.realModel &&
          overview.route.id !== aliasRouteId,
      );
      if (original) {
        const originalMember = original.members?.find(
          (candidate) => candidate.member.channel_id === channelId,
        );
        if (originalMember) {
          await service.deleteMember(originalMember.member.id);
        }
        const remaining = (original.members ?? []).filter(
          (candidate) => candidate.member.channel_id !== channelId,
        );
        if (remaining.length === 0) {
          await service.deleteRoute(original.route.id);
        }
      }
    },
    invalidateKeys: [...INVALIDATE],
  });

  const removeAlias = useAdminMutation({
    mutationFn: async (realModel: string) => {
      const alias = aliasFor(realModel);
      if (!alias || channelId == null) return;
      const { overview, member } = alias;
      // Remove only this channel's mapped member; the alias route stays when
      // other channels/models still share the alias name, and disappears once
      // it has no members left.
      const remaining = (overview.members ?? []).filter(
        (candidate) => candidate.member.id !== member.id,
      );
      await service.deleteMember(member.id);
      if (remaining.length === 0) {
        await service.deleteRoute(overview.route.id);
      }
      // Restore the real-name binding so the model stays reachable under its
      // real name again (the original member was dropped when the alias was
      // saved).
      const originalRoute = (routeOverviews.data ?? []).find(
        (entry) =>
          entry.route.model_pattern === realModel &&
          entry.route.id !== overview.route.id,
      );
      if (originalRoute) {
        const existingMember = (originalRoute.members ?? []).find(
          (candidate) => candidate.member.channel_id === channelId,
        );
        if (!existingMember) {
          await service.createMember(originalRoute.route.id, {
            channel_id: channelId,
            priority: 0,
            weight: 100,
            enabled: true,
            auto: true,
            manual_override: true,
          });
        }
      } else {
        const created = await service.createRoute({
          model_pattern: realModel,
          enabled: true,
        });
        await service.createMember(created.id, {
          channel_id: channelId,
          priority: 0,
          weight: 100,
          enabled: true,
          auto: true,
          manual_override: true,
        });
      }
    },
    invalidateKeys: [...INVALIDATE],
  });

  // Custom models: routes with a member on this channel that are not in the
  // discovered list (manually added names, or upstream models the channel
  // does not advertise). Members that carry an alias mapping are excluded
  // (they are the alias bindings rendered on the real-model rows).
  const customModels = useMemo(() => {
    const discoveredNames = new Set(models.map((m) => m.model_name));
    const out: { name: string; routeId: number; member: RouteMember }[] = [];
    for (const overview of routeOverviews.data ?? []) {
      if (overview.route.mapping_json) continue;
      if (discoveredNames.has(overview.route.model_pattern)) continue;
      for (const candidate of overview.members ?? []) {
        if (candidate.member.channel_id !== channelId) continue;
        if (candidate.member.mapping_json) continue;
        out.push({
          name: overview.route.model_pattern,
          routeId: overview.route.id,
          member: candidate.member,
        });
      }
    }
    return out;
  }, [models, routeOverviews.data, channelId]);

  const addCustom = useAdminMutation({
    mutationFn: async (name: string) => {
      const trimmed = name.trim();
      if (!trimmed || channelId == null) return;
      // Route names are global (unique model_pattern), while members are
      // channel-scoped. Reuse an existing route instead of failing with
      // a unique-constraint conflict when the name already exists.
      const existing = (routeOverviews.data ?? []).find(
        (overview) => overview.route.model_pattern === trimmed,
      );
      let routeId = existing?.route.id;
      const existingMember = existing?.members.find(
        (c) => c.member.channel_id === channelId,
      )?.member;
      if (routeId == null) {
        const created = await service.createRoute({
          model_pattern: trimmed,
          enabled: true,
        });
        routeId = created.id;
      } else if (existingMember) {
        // Already attached to this channel: either re-enable it or just
        // tell the user it exists (no silent no-op).
        if (!existingMember.enabled) {
          await service.updateMember(existingMember.id, {
            ...existingMember,
            enabled: true,
          });
          toast.push({
            tone: "success",
            message: t("channels.customModelReenabled"),
          });
        } else {
          toast.push({
            tone: "info",
            message: t("channels.customModelExists", { name: trimmed }),
          });
        }
        return;
      }
      await service.createMember(routeId, {
        channel_id: channelId,
        priority: 0,
        weight: 100,
        enabled: true,
        auto: true,
        manual_override: true,
      });
      toast.push({
        tone: "success",
        message: t("channels.customModelAdded", { name: trimmed }),
      });
    },
    invalidateKeys: [...INVALIDATE],
  });

  const removeCustom = useAdminMutation({
    mutationFn: async (input: { routeId: number; memberId: number }) => {
      await service.deleteMember(input.memberId);
      const overview = (routeOverviews.data ?? []).find(
        (entry) => entry.route.id === input.routeId,
      );
      const remaining = (overview?.members ?? []).filter(
        (c) => c.member.channel_id !== channelId,
      );
      if ((overview?.members ?? []).length > 0 && remaining.length === 0) {
        await service.deleteRoute(input.routeId);
      }
    },
    invalidateKeys: [...INVALIDATE],
  });

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const base: { key: string; name: string }[] = models.map((m) => ({
      key: m.model_name,
      name: m.model_name,
    }));
    const seen = new Set(base.map((item) => item.name));
    for (const custom of customModels) {
      if (!seen.has(custom.name)) {
        base.push({ key: custom.name, name: custom.name });
        seen.add(custom.name);
      }
    }
    const byNeedle = !needle
      ? base
      : base.filter(
          (item) =>
            item.name.toLowerCase().includes(needle) ||
            (aliasFor(item.name)?.overview.route.model_pattern ?? "")
              .toLowerCase()
              .includes(needle),
        );
    if (statusFilter === "all") return byNeedle;
    return byNeedle.filter((item) => {
      const member = memberFor(item.name);
      const enabled = member ? member.enabled : false;
      return statusFilter === "enabled" ? enabled : !enabled;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [models, customModels, query, routeOverviews.data, statusFilter]);

  const selectable = filtered;
  const allSelected =
    selectable.length > 0 && selectable.every((m) => selectedIds.has(m.key));
  const someSelected =
    selectable.length > 0 && selectable.some((m) => selectedIds.has(m.key));
  useEffect(() => {
    if (selectAllRef.current) {
      selectAllRef.current.indeterminate = someSelected && !allSelected;
    }
  }, [someSelected, allSelected]);

  const toggleSelect = (key: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  const toggleSelectAll = () => {
    if (!bulkMode) setBulkMode(true);
    setSelectedIds(
      allSelected ? new Set() : new Set(selectable.map((item) => item.key)),
    );
  };

  const enterBulkMode = () => {
    // Entering batch mode is intentionally non-destructive: start with every
    // checkbox clear and let the operator choose the target models.
    setSelectedIds(new Set());
    setBulkMode(true);
  };

  const exitBulkMode = () => {
    setBulkMode(false);
    setSelectedIds(new Set());
  };

  const runBulk = (enabled: boolean) => {
    // Enable-selected acts as a whitelist: checked rows are enabled (or
    // adopted when not wired yet) and every other wired row is disabled in
    // the same pass, so the saved state matches exactly what the operator
    // checked. Disable-selected only touches wired, checked rows.
    const manual = channel?.model_sync_mode === "manual";
    const updates: { member: RouteMember; enabled: boolean }[] = [];
    const removals: RouteMember[] = [];
    selectable.forEach((item) => {
      const member = memberFor(item.name);
      if (!member) return;
      const isSelected = selectedIds.has(item.key);
      const next = enabled ? isSelected : isSelected ? false : member.enabled;
      if (member.enabled === next) return;
      if (!next && manual && !member.mapping_json) {
        removals.push(member);
        return;
      }
      updates.push({ member, enabled: next });
    });
    const adoptions = enabled
      ? selectable
          .filter(
            (item) => selectedIds.has(item.key) && memberFor(item.name) == null,
          )
          .map((item) => item.name)
      : [];
    if (updates.length === 0 && removals.length === 0 && adoptions.length === 0)
      return;
    bulkToggle.mutate({ updates, removals, adoptions });
  };

  const selectedCount = selectable.filter((item) =>
    selectedIds.has(item.key),
  ).length;

  const enabledCount =
    models.filter((model) => memberFor(model.model_name)?.enabled ?? false)
      .length + customModels.filter((custom) => custom.member.enabled).length;
  const aliasedCount = models.filter((model) =>
    Boolean(aliasFor(model.model_name)),
  ).length;
  // "Adopted" = wired into routing (enabled or parked); "enabled" = currently
  // serving. Splitting them is what makes manual mode readable: a fresh
  // channel shows N models / 0 adopted by design, not because sync failed.
  const adoptedCount =
    models.filter((model) => memberFor(model.model_name) != null).length +
    customModels.length;
  const syncMode: ModelSyncMode =
    channel?.model_sync_mode === "auto" ? "auto" : "manual";

  // Group the filtered rows by vendor family for scannable bulk selection;
  // known groups keep their defined order, anything else lands in "Other".
  const grouped = useMemo(() => {
    const buckets = new Map<string, { key: string; name: string }[]>();
    for (const item of filtered) {
      const group = autoModelGroup(item.name);
      const bucket = buckets.get(group);
      if (bucket) bucket.push(item);
      else buckets.set(group, [item]);
    }
    const known = MODEL_GROUP_ORDER.filter((group) => buckets.has(group)).map(
      (group) => ({ group, items: buckets.get(group)! }),
    );
    const extra = [...buckets.keys()]
      .filter((group) => !MODEL_GROUP_ORDER.includes(group))
      .map((group) => ({ group, items: buckets.get(group)! }));
    return [...known, ...extra];
  }, [filtered]);

  return (
    <>
      {header ?? (
        <div className="channel-models-panel-head">
          <div>
            <p className="page-kicker">{channel?.name ?? `#${channelId}`}</p>
          </div>
        </div>
      )}

      {/* Sync mode is the rule that decides everything below, so it is stated
          up front (and switchable) instead of being buried in the edit drawer. */}
      <div className={`channel-models-syncbar is-${syncMode}`}>
        <div className="channel-models-syncbar-copy">
          <strong>{t("channels.syncMode")}</strong>
          <span>
            {syncMode === "auto"
              ? t("channels.modelsSyncBannerAuto")
              : t("channels.modelsSyncBannerManual")}
          </span>
        </div>
        <div
          className="channel-models-syncbar-switch"
          role="radiogroup"
          aria-label={t("channels.syncMode")}
        >
          {(["manual", "auto"] as const).map((mode) => (
            <button
              key={mode}
              type="button"
              className={`channel-models-syncbar-option${syncMode === mode ? " is-active" : ""}`}
              aria-pressed={syncMode === mode}
              disabled={setSyncMode.isPending || !channel}
              title={
                mode === "auto"
                  ? t("channels.syncModeAutoHint")
                  : t("channels.syncModeManualHint")
              }
              onClick={() => setSyncMode.mutate(mode)}
            >
              {mode === "auto"
                ? t("channels.syncModeAuto")
                : t("channels.syncModeManual")}
            </button>
          ))}
        </div>
      </div>

      <TelemetryStrip
        items={[
          {
            label: t("channels.modelsTotalStat"),
            value: models.length + customModels.length,
            tone: "primary",
            hint: t("channels.modelsTotalHint"),
          },
          {
            label: t("channels.modelsAdoptedStat"),
            value: adoptedCount,
            tone: "info",
            hint: t("channels.modelsAdoptedHint"),
          },
          { label: t("common.enabled"), value: enabledCount, tone: "success" },
          { label: t("channels.aliasStat"), value: aliasedCount },
        ]}
      />
      {syncMode === "manual" && models.length > 0 ? (
        <p className="detail-section-empty is-quiet channel-models-tip">
          {t("channels.adoptHint")}
        </p>
      ) : null}
      {cleanupNote ? (
        <p className="detail-section-empty is-quiet" role="status">
          {cleanupNote}
        </p>
      ) : null}

      <Panel className="ops-list-panel">
        <div className="models-simple-toolbar">
          <input
            className="models-search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("routing.searchPlaceholder")}
          />
          <div
            className="channel-model-status-filter"
            role="radiogroup"
            aria-label={t("channels.modelsFilterLabel")}
          >
            {(
              [
                [
                  "all",
                  t("channels.modelsFilterAll"),
                  models.length + customModels.length,
                ],
                ["enabled", t("channels.modelsFilterEnabled"), enabledCount],
                [
                  "disabled",
                  t("channels.modelsFilterDisabled"),
                  models.length + customModels.length - enabledCount,
                ],
              ] as const
            ).map(([mode, label, count]) => (
              <button
                key={mode}
                type="button"
                className={statusFilter === mode ? "is-active" : ""}
                aria-pressed={statusFilter === mode}
                title={t("channels.modelsFilterHint")}
                onClick={() => setStatusFilter(mode)}
              >
                {label}
                <span className="channel-model-status-count">{count}</span>
              </button>
            ))}
          </div>
          <div className="channel-model-add">
            <input
              value={customName}
              placeholder={t("channels.customModelPlaceholder")}
              onChange={(event) => setCustomName(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && customName.trim()) {
                  addCustom.mutate(customName);
                  setCustomName("");
                }
              }}
            />
            <Button
              variant="secondary"
              icon={<Plus size={14} />}
              disabled={addCustom.isPending || !customName.trim()}
              onClick={() => {
                addCustom.mutate(customName);
                setCustomName("");
              }}
            >
              {t("channels.customModelAdd")}
            </Button>
          </div>
          <Button
            variant="secondary"
            icon={<Trash2 size={14} />}
            disabled={cleanupParked.isPending || parkedMembers.length === 0}
            title={t("channels.modelsCleanupHint")}
            onClick={() => setCleanupOpen(true)}
          >
            {t("channels.modelsCleanup", { n: parkedMembers.length })}
          </Button>
          {bulkMode ? (
            <label
              className="channel-model-select-all"
              title={t("channels.modelsBulkHint")}
            >
              <input
                ref={selectAllRef}
                type="checkbox"
                checked={allSelected}
                onChange={toggleSelectAll}
                disabled={bulkToggle.isPending}
              />
              <span>{t("channels.modelsSelectAll")}</span>
            </label>
          ) : (
            <Button
              variant="secondary"
              className="channel-alias-save"
              onClick={enterBulkMode}
            >
              {t("channels.modelsBulkSelect")}
            </Button>
          )}
          {bulkMode && selectedCount > 0 ? (
            <span className="channel-model-selected-count">
              {t("channels.modelsSelectedCount", { n: selectedCount })}
            </span>
          ) : null}
          {bulkMode && selectedCount > 0 ? (
            <div className="channel-model-bulk-actions">
              <Button
                variant="secondary"
                className="channel-alias-save"
                disabled={bulkToggle.isPending}
                onClick={() => runBulk(true)}
              >
                {t("channels.modelsEnableSelected")}
              </Button>
              <Button
                variant="secondary"
                className="channel-alias-save"
                disabled={bulkToggle.isPending}
                onClick={() => runBulk(false)}
              >
                {t("channels.modelsDisableSelected")}
              </Button>
              <Button
                variant="quiet"
                className="channel-alias-save"
                disabled={bulkToggle.isPending}
                onClick={() => setSelectedIds(new Set())}
              >
                {t("channels.modelsClearSelection")}
              </Button>
            </div>
          ) : null}
          {bulkMode ? (
            <Button
              variant="quiet"
              className="channel-alias-save"
              disabled={bulkToggle.isPending}
              onClick={exitBulkMode}
            >
              {t("channels.modelsBulkDone")}
            </Button>
          ) : null}
        </div>
        <EntityState
          isLoading={discovered.isLoading}
          isError={discovered.isError}
          error={discovered.error}
          isEmpty={models.length === 0 && customModels.length === 0}
          empty={
            <p className="detail-section-empty is-quiet">
              {t("channels.modelsEmpty")}
            </p>
          }
          retry={() => discovered.refetch()}
        >
          {filtered.length === 0 ? (
            <p className="detail-section-empty is-quiet">
              {t("channels.modelsFilterEmpty")}
            </p>
          ) : (
            <ul className="channel-model-list is-page">
              {grouped.map(({ group, items }) => {
                const isCollapsed = !forcedOpen && collapsedGroups.has(group);
                return (
                  <Fragment key={group}>
                    <li
                      className={`channel-model-group-head${isCollapsed ? " is-collapsed" : ""}`}
                      onClick={() => toggleGroup(group)}
                      title={
                        isCollapsed
                          ? t("channels.groupExpand")
                          : t("channels.groupCollapse")
                      }
                    >
                      <span className="channel-model-group-name">{group}</span>
                      <span className="channel-model-group-count">
                        {items.length}
                      </span>
                    </li>
                    {!isCollapsed &&
                      items.map((item) => {
                        const discoveredModel = models.find(
                          (m) => m.model_name === item.name,
                        );
                        const isCustom = !discoveredModel;
                        const custom = isCustom
                          ? customModels.find((c) => c.name === item.name)
                          : undefined;
                        const member = memberFor(item.name);
                        const enabled = member ? member.enabled : false;
                        const aliasInfo = aliasFor(item.name);
                        const alias =
                          aliasInfo?.overview.route.model_pattern ?? "";
                        const inputValue = discoveredModel
                          ? (aliasInputs[discoveredModel.id] ?? alias)
                          : alias;
                        return (
                          <li
                            key={item.key}
                            className="channel-model-row is-alias"
                          >
                            {bulkMode ? (
                              <label
                                className="channel-model-select"
                                title={t("channels.modelsSelectAll")}
                              >
                                <input
                                  type="checkbox"
                                  checked={selectedIds.has(item.key)}
                                  disabled={bulkToggle.isPending}
                                  onChange={() => toggleSelect(item.key)}
                                />
                              </label>
                            ) : (
                              <label
                                className="channel-model-toggle"
                                title={
                                  member
                                    ? t("channels.modelEnabledHint")
                                    : t("channels.modelAdoptHint")
                                }
                              >
                                <input
                                  type="checkbox"
                                  checked={enabled}
                                  disabled={
                                    member
                                      ? toggleMember.pendingId === member.id
                                      : adopt.isPending
                                  }
                                  onChange={() => {
                                    if (member) toggleMember.mutate(member);
                                    else adopt.mutate(item.name);
                                  }}
                                />
                              </label>
                            )}
                            <span className="mono truncate" title={item.name}>
                              {item.name}
                            </span>
                            <span className="channel-model-alias">
                              {discoveredModel ? (
                                <>
                                  <input
                                    value={inputValue}
                                    placeholder={t("channels.aliasPlaceholder")}
                                    onChange={(event) =>
                                      setAliasInputs((previous) => ({
                                        ...previous,
                                        [discoveredModel.id]:
                                          event.target.value,
                                      }))
                                    }
                                  />
                                  {aliasInfo && inputValue === alias ? (
                                    <button
                                      type="button"
                                      className="icon-button"
                                      aria-label={t("channels.aliasRemove")}
                                      title={t("channels.aliasRemove")}
                                      disabled={removeAlias.isPending}
                                      onClick={() =>
                                        removeAlias.mutate(item.name)
                                      }
                                    >
                                      <Trash2 size={13} />
                                    </button>
                                  ) : inputValue.trim() !== alias ? (
                                    <Button
                                      variant="secondary"
                                      className="channel-alias-save"
                                      disabled={
                                        saveAlias.isPending ||
                                        !inputValue.trim()
                                      }
                                      onClick={() =>
                                        saveAlias.mutate({
                                          realModel: item.name,
                                          alias: inputValue,
                                        })
                                      }
                                    >
                                      {t("channels.aliasSave")}
                                    </Button>
                                  ) : null}
                                </>
                              ) : custom ? (
                                <Button
                                  variant="quiet"
                                  className="channel-alias-save"
                                  icon={<Trash2 size={13} />}
                                  disabled={removeCustom.isPending}
                                  onClick={() =>
                                    removeCustom.mutate({
                                      routeId: custom.routeId,
                                      memberId: custom.member.id,
                                    })
                                  }
                                >
                                  {t("channels.customModelRemove")}
                                </Button>
                              ) : null}
                            </span>
                            <StatusBadge
                              value={enabled ? "enabled" : "disabled"}
                            />
                          </li>
                        );
                      })}
                  </Fragment>
                );
              })}
            </ul>
          )}
        </EntityState>
      </Panel>
      {cleanupOpen ? (
        <ConfirmDialog
          title={t("channels.modelsCleanup", { n: parkedMembers.length })}
          confirmLabel={t("channels.modelsCleanupConfirmLabel")}
          message={t("channels.modelsCleanupConfirm", {
            n: parkedMembers.length,
          })}
          onClose={() => {
            if (!cleanupParked.isPending) setCleanupOpen(false);
          }}
          onConfirm={() =>
            cleanupParked.mutate(undefined, {
              onSuccess: () => setCleanupOpen(false),
            })
          }
          pending={cleanupParked.isPending}
          error={cleanupParked.error}
        />
      ) : null}
    </>
  );
}

/** Standalone route page (/models/channel/:id) wrapper. */
export function ChannelModels() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const params = useParams();
  const [searchParams] = useSearchParams();
  const channelId = positiveId(params.channelId);

  if (channelId == null) {
    return (
      <Page
        kicker={t("channels.detailKicker")}
        title={t("channels.modelsSection")}
        description=""
      >
        <ErrorState error={new Error("invalid channel id")} />
      </Page>
    );
  }

  return (
    <Page
      kicker={t("channels.modelsSection")}
      title={t("channels.modelsSection")}
      description={t("channels.modelsManageHint")}
      actions={
        <Button
          variant="secondary"
          icon={<ArrowLeft size={14} />}
          onClick={() => navigate("/")}
        >
          {t("channels.backToChannels")}
        </Button>
      }
    >
      <ChannelModelsPanel
        channelId={channelId}
        initialQuery={searchParams.get("model") ?? undefined}
      />
    </Page>
  );
}
