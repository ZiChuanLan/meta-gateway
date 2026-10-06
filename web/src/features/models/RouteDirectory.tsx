import type React from "react";
import { Link } from "react-router-dom";
import { Plus } from "lucide-react";
import type { Channel, Route, RouteOverview } from "../../api/types";
import { ActionMenu, type ActionMenuItem } from "../../components/ActionMenu";
import { rowContextPoint, rowKeyboardContextPoint } from "../../components/contextMenu";
import { EmptyHero } from "../../components/EmptyHero";
import { EntityState } from "../../components/EntityState";
import { ListShell } from "../../components/ListShell";
import { ModelDirectoryToolbar } from "../../components/ModelDirectoryToolbar";
import { PaginationBar } from "../../components/PaginationBar";
import { Button, Panel } from "../../components/ui";
import { ModelDirectoryTable } from "./ModelDirectoryTable";
import { modelGroup } from "./modelGroups";
import { primaryMember, candidateState } from "./routingPolicy";

/**
 * The models directory: the search and its three filters, the table rows (routing
 * rows plus the plugin-answered models that have no route), the bulk bar and the
 * row context menu.
 *
 * The A02 cut for the catalogue. It was the `directory` slot of the workspace
 * layout — 354 lines of JSX inside a 2,300-line component reading about forty
 * locals out of the enclosing scope, which is why the row slots (upstream links,
 * badges, per-route menus, bulk checkboxes) could not be read or changed in
 * isolation.
 *
 * The props are explicit and numerous on purpose; every captured value is now a
 * stated dependency. The board keeps the queries, mutations and state.
 */
type Text = (key: string, vars?: Record<string, string | number>) => string;

/** Where the row's own click handlers live, for the context menu. */
type ContextTarget = { routeId: number; top: number; left: number } | null;

export type RouteDirectoryProps = {
  t: Text;
  /* Search and filters. */
  query: string;
  setQuery: (value: string) => void;
  params: URLSearchParams;
  setSearchParams: (next: URLSearchParams, options?: { replace: boolean }) => void;
  showModelFilters: boolean;
  setShowModelFilters: (show: boolean) => void;
  activeFilterCount: number;
  groupFilter: string;
  setGroupFilter: (value: string) => void;
  channelFilter: number;
  setChannelFilter: (value: number) => void;
  statusFilter: "enabled" | "disabled" | "all";
  setStatusFilter: (value: "enabled" | "disabled" | "all") => void;
  modelGroups: string[];
  channels: Channel[];
  /* Data. */
  overviews: {
    isPending: boolean;
    isError: boolean;
    error: unknown;
    data?: RouteOverview[];
    refetch: () => void;
  };
  rows: RouteOverview[];
  pageRows: RouteOverview[];
  virtualModels: Array<{ model: string; pluginId: string; pluginName: string }>;
  pluginPageOf: (pluginId: string) => string;
  navigate: (to: string) => void;
  selected: number | null | undefined;
  metaByModel: Map<
    string,
    { context_window?: number; supports_thinking?: number; vendor?: string }
  >;
  pagination: {
    page: number;
    totalPages: number;
    total: number;
    pageSize: number;
    rangeStart: number;
    rangeEnd: number;
    hasPrev: boolean;
    hasNext: boolean;
    setPage: (page: number) => void;
    setPageSize: (size: number) => void;
  };
  /* Bulk selection. */
  bulkMode: boolean;
  bulkSelected: Set<number>;
  setBulkSelected: (next: Set<number> | ((prev: Set<number>) => Set<number>)) => void;
  toggleBulkSelected: (id: number) => void;
  exitBulkMode: () => void;
  selectAllPage: () => void;
  pageAllSelected: boolean;
  bulkBusy: boolean;
  bulkToggleRoutes: { mutate: (input: { ids: number[]; enabled: boolean }) => unknown };
  setBulkDeleteOpen: (open: boolean) => void;
  /* Row interaction. */
  selectRow: (routeId: number) => void;
  contextMenu: ContextTarget;
  setContextMenu: (value: ContextTarget) => void;
  modelActions: (route: Route, options?: { closeContext?: boolean }) => ActionMenuItem[];
  /* Row busy state. */
  pending: { toggleRoute: { pendingId?: number | string | null }; del: { isPending: boolean } };
  /* Creating a route from the empty state: reset the draft, then open it. */
  resetRouteDraft: () => void;
  startNewRoute: () => void;
};

export function RouteDirectory(props: RouteDirectoryProps) {
  const {
    t,
    query,
    setQuery,
    params,
    setSearchParams,
    showModelFilters,
    setShowModelFilters,
    activeFilterCount,
    groupFilter,
    setGroupFilter,
    channelFilter,
    setChannelFilter,
    statusFilter,
    setStatusFilter,
    modelGroups,
    channels,
    overviews,
    rows,
    pageRows,
    virtualModels,
    pluginPageOf,
    navigate,
    selected,
    metaByModel,
    pagination,
    bulkMode,
    bulkSelected,
    setBulkSelected,
    toggleBulkSelected,
    exitBulkMode,
    selectAllPage,
    pageAllSelected,
    bulkBusy,
    bulkToggleRoutes,
    setBulkDeleteOpen,
    selectRow,
    contextMenu,
    setContextMenu,
    modelActions,
    pending,
    resetRouteDraft,
    startNewRoute,
  } = props;
  return (
    <Panel className="ops-list-panel model-directory" title={t("modelsPage.listTitle")}>
      <ModelDirectoryToolbar
        value={query}
        label={t("routing.searchPlaceholder")}
        onChange={(nextQuery) => {
          setQuery(nextQuery);
          const next = new URLSearchParams(params);
          if (nextQuery) next.set("model", nextQuery);
          else next.delete("model");
          next.delete("route");
          setSearchParams(next, { replace: true });
        }}
      >
        <Button
          variant="quiet"
          aria-expanded={showModelFilters}
          onClick={() => setShowModelFilters(!showModelFilters)}
        >
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
            {channels.map((channel) => (
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
            <option value="all">{t("modelsPage.statusAll")}</option>
            <option value="enabled">{t("common.enabled")}</option>
            <option value="disabled">{t("common.disabled")}</option>
          </select>
        </div>
      </ModelDirectoryToolbar>

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
                {/* The recommended path: adopt models from the channel's model
                    settings — routes are created automatically. Manual route
                    creation stays available but demoted. */}
                <Link className="button" to="/channels">
                  {t("modelsPage.ctaConnections")}
                </Link>
                <Button
                  variant="secondary"
                  icon={<Plus size={16} />}
                  onClick={() => {
                    resetRouteDraft();
                    startNewRoute();
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
                onClick={() => bulkToggleRoutes.mutate({ ids: [...bulkSelected], enabled: true })}
              >
                {t("modelsPage.bulkEnableSelected")}
              </Button>
              <Button
                variant="secondary"
                disabled={bulkBusy}
                onClick={() => bulkToggleRoutes.mutate({ ids: [...bulkSelected], enabled: false })}
              >
                {t("modelsPage.bulkDisableSelected")}
              </Button>
              {/* Irreversible, so it must not look identical to the two reversible
                  toggles beside it. */}
              <Button variant="danger" disabled={bulkBusy} onClick={() => setBulkDeleteOpen(true)}>
                {t("modelsPage.bulkDeleteSelected")}
              </Button>
              <Button variant="quiet" onClick={() => setBulkSelected(new Set())}>
                {t("modelsPage.bulkClear")}
              </Button>
              <Button variant="quiet" onClick={exitBulkMode}>
                {t("modelsPage.bulkDone")}
              </Button>
            </div>
          ) : null}
          <div className="table-wrap model-directory-wrap">
            {/* The directory rows themselves are shared with the member app's
                catalogue (ModelDirectoryTable): same name cell, same badges, same
                column semantics. The console supplies the operator cells —
                upstream links, per-route menus, bulk checkboxes — through the row
                slots. */}
            <ModelDirectoryTable
              className={bulkMode ? "model-directory-table is-bulk" : "model-directory-table"}
              showUpstream
              bulkMode={bulkMode}
              bulkHeader={
                <input
                  type="checkbox"
                  aria-label={t("modelsPage.bulkSelectPage")}
                  checked={pageAllSelected}
                  onChange={selectAllPage}
                />
              }
              onBulkCellClick={(event) => event.stopPropagation()}
              rows={[
                // Plugin-answered models have no route and no members, so they
                // cannot be selected or opened like a route. They are still real
                // callable names — the downstream catalogue advertises them — so
                // they belong in this list with the plugin named as their owner.
                ...(query.trim()
                  ? virtualModels.filter((item) =>
                      item.model.toLowerCase().includes(query.trim().toLowerCase()),
                    )
                  : virtualModels
                ).map((item) => ({
                  name: item.model,
                  group: t("modelsPage.pluginModel"),
                  upstream: <span className="plugin-model-owner">{item.pluginName}</span>,
                  status: "enabled" as const,
                  actions: (
                    <Button variant="quiet" onClick={() => navigate(pluginPageOf(item.pluginId))}>
                      {t("modelsPage.pluginModelOpen")}
                    </Button>
                  ),
                })),
                ...pageRows.map((item) => {
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
                    pending.toggleRoute.pendingId === item.route.id || pending.del.isPending;
                  return {
                    name: item.route.model_pattern,
                    group,
                    provider: (
                      <small className="model-nav-provider">
                        {head ? head.channel.name : t("modelsPage.noUpstream")}
                      </small>
                    ),
                    badges: item.route.image_edit_shim ? (
                      // Only meaningful on routes whose model has no images
                      // endpoint of its own, so it stays a hint rather than a
                      // status.
                      <span
                        className="model-meta-badge is-shim"
                        title={t("routing.imageEditShimHint")}
                      >
                        {t("modelsPage.metaImageEdit")}
                      </span>
                    ) : undefined,
                    contextWindow: meta?.context_window,
                    supportsThinking: (meta?.supports_thinking ?? 0) > 0,
                    vendor: meta?.vendor || undefined,
                    upstream: head ? (
                      <>
                        <Link
                          to={`/channels?channel=${head.channel.id}`}
                          className="upstream-link"
                          title={t("modelsPage.openChannelHint")}
                          onClick={(event) => {
                            // Don't trigger the row's selectRow when jumping
                            // straight to the channel editor.
                            event.stopPropagation();
                          }}
                        >
                          {head.channel.name}
                        </Link>
                        {item.members.length > 1 ? ` +${item.members.length - 1}` : ""}
                      </>
                    ) : (
                      <span className="muted">{t("modelsPage.noUpstream")}</span>
                    ),
                    status: !item.route.enabled
                      ? ("disabled" as const)
                      : ready > 0
                        ? ("ready" as const)
                        : ("unavailable" as const),
                    actions: (
                      <ActionMenu
                        compact
                        label={t("common.moreActions")}
                        title={item.route.model_pattern}
                        disabled={rowBusy}
                        items={modelActions(item.route)}
                      />
                    ),
                    tabIndex: 0,
                    className: `is-clickable${active ? " is-selected" : ""}`,
                    bulkCell: bulkMode ? (
                      <input
                        type="checkbox"
                        aria-label={t("modelsPage.bulkSelectOne", {
                          model: item.route.model_pattern,
                        })}
                        checked={bulkSelected.has(item.route.id)}
                        onChange={() => toggleBulkSelected(item.route.id)}
                      />
                    ) : null,
                    onClick: () => selectRow(item.route.id),
                    onContextMenu: (event: React.MouseEvent<HTMLElement>) => {
                      const point = rowContextPoint(event);
                      if (!point) return;
                      selectRow(item.route.id);
                      setContextMenu({ routeId: item.route.id, ...point });
                    },
                    onKeyDown: (event: React.KeyboardEvent<HTMLElement>) => {
                      const point = rowKeyboardContextPoint(event);
                      if (point) {
                        selectRow(item.route.id);
                        setContextMenu({ routeId: item.route.id, ...point });
                      }
                    },
                  };
                }),
              ]}
            />
          </div>
        </ListShell>
        {contextMenu
          ? (() => {
              const overview =
                rows.find((row) => row.route.id === contextMenu.routeId) ??
                (overviews.data ?? []).find((row) => row.route.id === contextMenu.routeId);
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
                  position={{ top: contextMenu.top, left: contextMenu.left }}
                  items={modelActions(overview.route, { closeContext: true })}
                />
              );
            })()
          : null}
      </EntityState>
    </Panel>
  );
}
