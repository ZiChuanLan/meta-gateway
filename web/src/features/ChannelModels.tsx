import { ArrowLeft, Plus, Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useEffect, useRef, useState, type ReactNode } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "../api/client";
import { useToast } from "../toast";
import type {
  Channel,
  DiscoveredModel,
  Route,
  RouteMember,
  RouteOverview,
} from "../api/types";
import { EntityState } from "../components/EntityState";
import { StatGrid } from "../components/StatGrid";
import { Button, ErrorState, Page, Panel, StatusBadge } from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useI18n } from "../i18n";
import { useSession } from "../session";

const INVALIDATE = [
  ["channel-overviews"],
  ["channels"],
  ["routes"],
  ["route-overviews"],
  ["discovered-models"],
  ["members"],
  ["models"],
] as const;

function positiveId(value: string | undefined) {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
}

/**
 * Per-channel model management UI: enable/disable + outward aliases.
 * Reusable inside the edit drawer or on a standalone page.
 */
export function ChannelModelsPanel({
  channelId,
  header,
}: {
  channelId: number;
  header?: ReactNode;
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
  const [query, setQuery] = useState("");
  const [selectedIds, setSelectedIds] = useState<Set<string>>(() => new Set());
  const [bulkMode, setBulkMode] = useState(false);
  const selectAllRef = useRef<HTMLInputElement>(null);

  const aliasRouteFor = (realModel: string): RouteOverview | undefined =>
    (routeOverviews.data ?? []).find((overview) => {
      if (!overview.route.mapping_json) return false;
      try {
        const parsed = JSON.parse(overview.route.mapping_json) as {
          real?: string;
        };
        return (
          parsed.real === realModel &&
          (overview.members ?? []).some(
            (member) => member.member.channel_id === channelId,
          )
        );
      } catch {
        return false;
      }
    });

  const memberFor = (realModel: string): RouteMember | undefined => {
    // When an alias is active the channel's binding lives on the alias
    // route (the original-name member was retired when the alias was
    // saved), so the toggle must control that member.
    const aliasOverview = aliasRouteFor(realModel);
    if (aliasOverview) {
      return aliasOverview.members.find(
        (candidate) => candidate.member.channel_id === channelId,
      )?.member;
    }
    const route = (routeOverviews.data ?? []).find(
      (overview) => overview.route.model_pattern === realModel,
    );
    return route?.members.find(
      (candidate) => candidate.member.channel_id === channelId,
    )?.member;
  };

  const toggleMember = useAdminMutation({
    mutationFn: (member: RouteMember) =>
      service.updateMember(member.id, { ...member, enabled: !member.enabled }),
    invalidateKeys: [...INVALIDATE],
    pendingIdOf: (member) => member.id,
  });

  const bulkToggle = useAdminMutation({
    mutationFn: async (input: {
      updates: { member: RouteMember; enabled: boolean }[];
    }) => {
      await Promise.all(
        input.updates.map(({ member, enabled }) =>
          service.updateMember(member.id, {
            ...member,
            enabled,
          }),
        ),
      );
    },
    invalidateKeys: [...INVALIDATE],
  });

  const saveAlias = useAdminMutation({
    mutationFn: async (input: { realModel: string; alias: string }) => {
      const alias = input.alias.trim();
      if (!alias || channelId == null) return;
      const mapping = JSON.stringify({ real: input.realModel });
      const existing = aliasRouteFor(input.realModel);
      let aliasRoute: Route;
      if (existing) {
        aliasRoute = await service.updateRoute(existing.route.id, {
          ...existing.route,
          model_pattern: alias,
          mapping_json: mapping,
        });
      } else {
        aliasRoute = await service.createRoute({
          model_pattern: alias,
          enabled: true,
          mapping_json: mapping,
        });
        await service.createMember(aliasRoute.id, {
          channel_id: channelId,
          priority: 0,
          weight: 100,
          enabled: true,
          auto: true,
          manual_override: true,
        });
      }
      // Retire the original model name for this channel: drop its member
      // on the original route; delete the route if it became empty.
      const original = (routeOverviews.data ?? []).find(
        (overview) =>
          overview.route.model_pattern === input.realModel &&
          overview.route.id !== aliasRoute.id,
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
    mutationFn: async (routeId: number) => {
      await service.deleteRoute(routeId);
      // Re-create the original route binding so the model stays reachable
      // under its real name again (the original member was dropped when
      // the alias was saved).
      const overview = (routeOverviews.data ?? []).find(
        (entry) => entry.route.id === routeId,
      );
      if (overview && channelId != null) {
        try {
          const parsed = JSON.parse(overview.route.mapping_json ?? "") as {
            real?: string;
          };
          const real = parsed.real;
          if (real) {
            const originalRoute = (routeOverviews.data ?? []).find(
              (entry) => entry.route.model_pattern === real,
            );
            if (originalRoute) {
              // The original route still exists (other channels keep it):
              // re-attach this channel's member that the alias save dropped.
              const member = (originalRoute.members ?? []).find(
                (candidate) => candidate.member.channel_id === channelId,
              );
              if (!member) {
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
                model_pattern: real,
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
          }
        } catch {
          // Non-mapping route: nothing to restore.
        }
      }
    },
    invalidateKeys: [...INVALIDATE],
  });

  // Custom models: routes with a member on this channel that are not in the
  // discovered list (manually added names, or upstream models the channel
  // does not advertise).
  const customModels = useMemo(() => {
    const discoveredNames = new Set(models.map((m) => m.model_name));
    const out: { name: string; routeId: number; member: RouteMember }[] = [];
    for (const overview of routeOverviews.data ?? []) {
      if (overview.route.mapping_json) continue;
      if (discoveredNames.has(overview.route.model_pattern)) continue;
      const candidate = (overview.members ?? []).find(
        (c) => c.member.channel_id === channelId,
      );
      if (candidate) {
        out.push({
          name: overview.route.model_pattern,
          routeId: overview.route.id,
          member: candidate.member,
        });
      }
    }
    return out;
  }, [models, routeOverviews.data, channelId]);

  const toast = useToast();

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
    if (!needle) return base;
    return base.filter(
      (item) =>
        item.name.toLowerCase().includes(needle) ||
        (aliasRouteFor(item.name)?.route.model_pattern ?? "")
          .toLowerCase()
          .includes(needle),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [models, customModels, query, routeOverviews.data]);

  const selectable = filtered.filter((item) => memberFor(item.name) != null);
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
    // Enable-selected acts as a whitelist: checked rows are enabled and every
    // other selectable row is disabled in the same pass, so the saved state
    // matches exactly what the operator checked. Disable-selected only touches
    // the checked rows.
    const updates = selectable
      .map((item) => {
        const member = memberFor(item.name);
        if (!member) return null;
        const next = enabled ? selectedIds.has(item.key) : member.enabled;
        return member.enabled === next ? null : { member, enabled: next };
      })
      .filter(
        (u): u is { member: RouteMember; enabled: boolean } => u != null,
      );
    if (updates.length === 0) return;
    bulkToggle.mutate({ updates });
  };

  const selectedCount = selectable.filter((item) =>
    selectedIds.has(item.key),
  ).length;

  const enabledCount =
    models.filter((model) => memberFor(model.model_name)?.enabled ?? true)
      .length + customModels.filter((custom) => custom.member.enabled).length;
  const aliasedCount = models.filter((model) =>
    Boolean(aliasRouteFor(model.model_name)),
  ).length;

  return (
    <>
      {header ?? (
        <div className="channel-models-panel-head">
          <div>
            <p className="page-kicker">{channel?.name ?? `#${channelId}`}</p>
            <p className="detail-section-empty is-quiet">
              {t("channels.modelsManageHint")}
            </p>
          </div>
        </div>
      )}
      <StatGrid
        items={[
          {
            label: t("channels.stat.total"),
            value: models.length + customModels.length,
          },
          { label: t("common.enabled"), value: enabledCount },
          { label: t("channels.aliasStat"), value: aliasedCount },
        ]}
      />

      <Panel className="ops-list-panel">
        <div className="models-simple-toolbar">
          <input
            className="models-search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("routing.searchPlaceholder")}
          />
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
          <ul className="channel-model-list is-page">
            {filtered.map((item) => {
              const discoveredModel = models.find(
                (m) => m.model_name === item.name,
              );
              const isCustom = !discoveredModel;
              const custom = isCustom
                ? customModels.find((c) => c.name === item.name)
                : undefined;
              const member = memberFor(item.name);
              const enabled = member ? member.enabled : true;
              const aliasOverview = aliasRouteFor(item.name);
              const alias = aliasOverview?.route.model_pattern ?? "";
              const inputValue = discoveredModel
                ? (aliasInputs[discoveredModel.id] ?? alias)
                : alias;
              return (
                <li key={item.key} className="channel-model-row is-alias">
                  {bulkMode ? (
                    <label
                      className="channel-model-select"
                      title={t("channels.modelsSelectAll")}
                    >
                      <input
                        type="checkbox"
                        checked={selectedIds.has(item.key)}
                        disabled={!member || bulkToggle.isPending}
                        onChange={() => toggleSelect(item.key)}
                      />
                    </label>
                  ) : (
                    <label
                      className="channel-model-toggle"
                      title={t("channels.modelEnabledHint")}
                    >
                      <input
                        type="checkbox"
                        checked={enabled}
                        disabled={
                          !member || toggleMember.pendingId === member.id
                        }
                        onChange={() => {
                          if (member) toggleMember.mutate(member);
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
                              [discoveredModel.id]: event.target.value,
                            }))
                          }
                        />
                        {aliasOverview && inputValue === alias ? (
                          <button
                            type="button"
                            className="icon-button"
                            aria-label={t("channels.aliasRemove")}
                            title={t("channels.aliasRemove")}
                            disabled={removeAlias.isPending}
                            onClick={() =>
                              removeAlias.mutate(aliasOverview.route.id)
                            }
                          >
                            <Trash2 size={13} />
                          </button>
                        ) : inputValue.trim() !== alias ? (
                          <Button
                            variant="secondary"
                            className="channel-alias-save"
                            disabled={saveAlias.isPending || !inputValue.trim()}
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
                  <StatusBadge value={enabled ? "enabled" : "disabled"} />
                </li>
              );
            })}
          </ul>
        </EntityState>
      </Panel>
    </>
  );
}

/** Standalone route page (/models/channel/:id) wrapper. */
export function ChannelModels() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const params = useParams();
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
      <ChannelModelsPanel channelId={channelId} />
    </Page>
  );
}
