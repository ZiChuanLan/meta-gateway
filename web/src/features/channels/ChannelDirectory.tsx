import { Boxes, Plus, Search } from "lucide-react";
import type { ChannelOverview, Site } from "../../api/types";
import type { ActionMenuItem } from "../../components/ActionMenu";
import { ActionMenu } from "../../components/ActionMenu";
import { rowContextPoint, rowKeyboardContextPoint } from "../../components/contextMenu";
import { EmptyHero } from "../../components/EmptyHero";
import { EntityState } from "../../components/EntityState";
import { ListShell } from "../../components/ListShell";
import { PaginationBar } from "../../components/PaginationBar";
import { Button, DataTable, Panel } from "../../components/ui";
import { ChannelStatusBadges } from "./badges";
import { capabilityFlags } from "./helpers";
import type { ConnectionHealthFilter } from "./helpers";

/**
 * The connections directory: the search and filters, the table, its bulk bar and
 * the row context menu.
 *
 * This is the A02 split's point for the channels page — the list used to be 340
 * lines of JSX in the middle of a two-thousand line component, reading eighteen
 * locals straight out of the enclosing scope, so no part of it (which column shows
 * what, when a row is busy, how the context menu positions itself) could be read
 * or changed without the whole board on screen.
 *
 * The props are deliberately explicit and numerous: every one of those eighteen
 * captured values is now a stated dependency, which is exactly the cost of the
 * split, paid once and visibly rather than silently. The board remains the owner
 * of every query, mutation and piece of state; this component renders and calls
 * back.
 */
type Text = (key: string, vars?: Record<string, string | number>) => string;

/** The slice of a mutation/query hook this directory reads. */
type Pending = { pendingId?: number | string | null };

/* eslint-disable-next-line @typescript-eslint/no-explicit-any */
type Runner = { mutate: (...args: any[]) => unknown };

export type ChannelDirectoryProps = {
  t: Text;
  /* Filters. */
  query: string;
  setQuery: (value: string) => void;
  params: URLSearchParams;
  setParams: (next: URLSearchParams, options?: { replace: boolean }) => void;
  typeFilter: string;
  setTypeFilter: (value: string) => void;
  groupFilter: string;
  setGroupFilter: (value: string) => void;
  updateFilterParam: (key: string, value: string) => void;
  /** Group/type options come straight off the rows, so a channel with no
   *  type_hint contributes an undefined entry — rendered as an empty option. */
  filterOptions: { types: (string | undefined)[]; groups: string[] };
  healthFilter: ConnectionHealthFilter;
  setHealthFilter: (value: ConnectionHealthFilter) => void;
  /* Data. */
  overviews: {
    isPending: boolean;
    isError: boolean;
    error: unknown;
    data?: ChannelOverview[];
    refetch: () => void;
  };
  rows: ChannelOverview[];
  /**
   * Channels the same search term reached through their MODELS rather than
   * their name. They are listed under the table, never twice inside it.
   */
  modelMatches: Array<{ channelId: number; name: string; model: string; source: string }>;
  pageRows: ChannelOverview[];
  siteById: Map<number, Site>;
  selected: ChannelOverview | null;
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
  /* Bulk selection and its operations. */
  bulkMode: boolean;
  bulkSelected: Set<number>;
  toggleBulkSelected: (id: number) => void;
  setBulkSelected: (next: Set<number> | ((prev: Set<number>) => Set<number>)) => void;
  exitBulkMode: () => void;
  bulkBusy: boolean;
  bulkSync: Runner;
  bulkStatus: Runner;
  bulkHeader: React.ReactNode;
  /* Row interaction. */
  openAdd: () => void;
  selectRow: (id: number) => void;
  setInspectorOpen: (open: boolean) => void;
  setContextMenu: (value: { channelId: number; top: number; left: number } | null) => void;
  contextMenu: { channelId: number; top: number; left: number } | null;
  connectionActions: (
    overview: ChannelOverview,
    options?: { closeContext?: boolean },
  ) => ActionMenuItem[];
  /* Row busy state comes from five mutations' pending ids. */
  pending: {
    refresh: Pending;
    probe: Pending;
    accountProbe: Pending;
    syncKeys: Pending;
    toggle: Pending;
    del: Pending;
  };
};

export function ChannelDirectory(props: ChannelDirectoryProps) {
  const {
    t,
    query,
    setQuery,
    params,
    setParams,
    typeFilter,
    setTypeFilter,
    groupFilter,
    setGroupFilter,
    updateFilterParam,
    filterOptions,
    healthFilter,
    setHealthFilter,
    overviews,
    rows,
    modelMatches,
    pageRows,
    siteById,
    selected,
    pagination,
    bulkMode,
    bulkSelected,
    toggleBulkSelected,
    setBulkSelected,
    exitBulkMode,
    bulkBusy,
    bulkSync,
    bulkStatus,
    bulkHeader,
    openAdd,
    selectRow,
    setInspectorOpen,
    setContextMenu,
    contextMenu,
    connectionActions,
    pending,
  } = props;
  return (
    <Panel className="ops-list-panel channels-directory">
      <div className="workspace-filter-row">
        {" "}
        <label className="directory-search">
          <Search size={14} aria-hidden="true" />
          <input
            value={query}
            onChange={(e) => {
              const value = e.target.value;
              setQuery(value);
              const next = new URLSearchParams(params);
              if (value) next.set("search", value);
              else next.delete("search");
              next.delete("id");
              setParams(next, { replace: true });
            }}
            placeholder={t("channels.searchPlaceholder")}
            aria-label={t("channels.searchPlaceholder")}
          />
        </label>
        <select
          aria-label={t("channels.filterType")}
          value={typeFilter}
          onChange={(event) => {
            const v = event.target.value;
            setTypeFilter(v);
            updateFilterParam("type", v);
          }}
        >
          <option value="all">{t("channels.allTypes")}</option>
          {filterOptions.types.map((type) => (
            <option key={type} value={type}>
              {type}
            </option>
          ))}
        </select>
        <select
          aria-label={t("channels.filterGroup")}
          value={groupFilter}
          onChange={(event) => {
            const v = event.target.value;
            setGroupFilter(v);
            updateFilterParam("group", v);
          }}
        >
          <option value="all">{t("channels.allGroups")}</option>
          {filterOptions.groups.map((group) => (
            <option key={group} value={group}>
              {group}
            </option>
          ))}
        </select>
        <span className="workspace-list-caption">{t("channels.listHint")}</span>
      </div>
      <EntityState
        isLoading={overviews.isPending}
        isError={overviews.isError}
        error={overviews.error}
        isEmpty={!rows.length && modelMatches.length === 0}
        empty={
          <EmptyHero
            kicker={
              healthFilter === "missing_key"
                ? t("channels.filter.missingKeyKicker")
                : t("channels.emptyKicker")
            }
            title={
              healthFilter === "missing_key"
                ? t("channels.filter.missingKeyTitle")
                : healthFilter === "attention"
                  ? t("channels.filter.attentionTitle")
                  : healthFilter === "ready"
                    ? t("channels.filter.readyTitle")
                    : t("channels.emptyTitle")
            }
            body={healthFilter === "all" ? t("channels.empty") : t("channels.filter.clearHint")}
            actions={
              healthFilter === "all" ? (
                <Button icon={<Plus size={16} />} onClick={openAdd}>
                  {t("channels.add")}
                </Button>
              ) : (
                <Button variant="secondary" onClick={() => setHealthFilter("all")}>
                  {t("common.clearFilters")}
                </Button>
              )
            }
          />
        }
        retry={() => overviews.refetch()}
      >
        {bulkMode && bulkSelected.size > 0 ? (
          <div className="toolbar bulk-bar">
            <span className="live-trace-count">
              {t("channels.bulkSelected", { n: bulkSelected.size })}
            </span>
            <Button
              variant="secondary"
              disabled={bulkBusy}
              onClick={() => bulkSync.mutate([...bulkSelected])}
            >
              {t("channels.bulkSyncModels")}
            </Button>
            <Button
              variant="secondary"
              disabled={bulkBusy}
              onClick={() => bulkStatus.mutate({ ids: [...bulkSelected], status: "enabled" })}
            >
              {t("common.enableAction")}
            </Button>
            <Button
              variant="secondary"
              disabled={bulkBusy}
              onClick={() => bulkStatus.mutate({ ids: [...bulkSelected], status: "disabled" })}
            >
              {t("common.disableAction")}
            </Button>
            <Button variant="quiet" onClick={() => setBulkSelected(new Set())}>
              {t("channels.bulkClear")}
            </Button>
            <Button variant="quiet" onClick={exitBulkMode}>
              {t("channels.bulkDone")}
            </Button>
          </div>
        ) : null}
        <ListShell
          footer={
            rows.length > 0 ? (
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
            ) : null
          }
        >
          {/* The table only exists when the search matched a NAME: with no such
              row the model group below is the whole answer, and a lone header
              row above it would only suggest the list failed to load. */}
          {pageRows.length > 0 ? (
            <DataTable
              headers={[
                ...(bulkMode ? [bulkHeader] : []),
                t("common.name"),
                t("common.status"),
                t("common.models"),
                t("channels.modelsSelectedCol"),
                t("common.latency"),
                t("common.actions"),
              ]}
            >
              {pageRows.map((overview) => {
                const ch = overview.channel;
                const site = ch.site_id != null ? siteById.get(ch.site_id) : undefined;
                const displayBase = ch.base_url || site?.base_url || "";
                const caps = capabilityFlags(overview);
                const active = selected?.channel.id === ch.id;
                const rowBusy =
                  pending.refresh.pendingId === ch.id ||
                  pending.probe.pendingId === ch.id ||
                  pending.accountProbe.pendingId === ch.id ||
                  pending.syncKeys.pendingId === ch.id ||
                  pending.toggle.pendingId === ch.id ||
                  pending.del.pendingId === ch.id;
                return (
                  <tr
                    key={ch.id}
                    tabIndex={0}
                    className={`is-clickable${active ? " is-selected" : ""}`}
                    onClick={() => {
                      selectRow(ch.id);
                      setInspectorOpen(true);
                    }}
                    onContextMenu={(event) => {
                      const point = rowContextPoint(event);
                      if (!point) return;
                      selectRow(ch.id);
                      setContextMenu({ channelId: ch.id, ...point });
                    }}
                    onKeyDown={(event) => {
                      const point = rowKeyboardContextPoint(event);
                      if (point) {
                        selectRow(ch.id);
                        setContextMenu({ channelId: ch.id, ...point });
                      }
                    }}
                  >
                    {bulkMode ? (
                      <td className="bulk-cell" onClick={(event) => event.stopPropagation()}>
                        <input
                          type="checkbox"
                          aria-label={t("channels.bulkSelectOne", { name: ch.name })}
                          checked={bulkSelected.has(ch.id)}
                          onChange={() => toggleBulkSelected(ch.id)}
                        />
                      </td>
                    ) : null}
                    <td>
                      <strong>{ch.name}</strong>
                      {ch.group_name ? (
                        <span className="capability-chip is-group">{ch.group_name}</span>
                      ) : null}
                      {displayBase ? (
                        <a
                          className="mono truncate base-url-link"
                          href={displayBase}
                          target="_blank"
                          rel="noopener noreferrer"
                          title={displayBase}
                          onClick={(event) => event.stopPropagation()}
                        >
                          {displayBase}
                        </a>
                      ) : (
                        <small className="mono truncate">{t("channels.inheritsSite")}</small>
                      )}
                    </td>
                    <td className="status-col">
                      <div className="capability-stack is-compact">
                        <ChannelStatusBadges overview={overview} />
                        {caps.tokenProblem ? (
                          <span className="capability-chip is-warn">
                            {t("channels.badge.tokenProblem")}
                          </span>
                        ) : null}
                        {caps.checkinScheduled ? (
                          <span className="capability-chip is-checkin">
                            {t("channels.badge.checkinOn")}
                          </span>
                        ) : caps.checkinNeedsUserID ? (
                          <span className="capability-chip is-warn">
                            {t("channels.badge.needsUserId")}
                          </span>
                        ) : null}
                        {caps.modelsReady ? (
                          <span className="capability-chip is-models">
                            {t("channels.badge.models")}
                          </span>
                        ) : null}
                      </div>
                    </td>
                    <td title={t("channels.modelsTotalHint")}>
                      {overview.last_checked_at ? (
                        overview.discovered_model_count > 0 ? (
                          <strong>{overview.discovered_model_count}</strong>
                        ) : (
                          <span className="muted">0</span>
                        )
                      ) : (
                        <span className="muted">—</span>
                      )}
                    </td>
                    <td
                      title={
                        overview.model_count > 0
                          ? t("channels.modelsAdoptedHint")
                          : t("channels.modelsNoneAdoptedHint")
                      }
                    >
                      {overview.model_count > 0 ? (
                        <strong>{overview.model_count}</strong>
                      ) : (
                        <span className="muted">0</span>
                      )}
                    </td>
                    <td>
                      {overview.last_checked_at
                        ? t("common.ms", { n: overview.last_latency_ms })
                        : "—"}
                    </td>
                    <td
                      className="actions row-actions"
                      onClick={(event) => event.stopPropagation()}
                    >
                      <ActionMenu
                        compact
                        label={t("common.moreActions")}
                        title={ch.name}
                        disabled={rowBusy}
                        onOpenChange={(open) => {
                          // Ensure credentials for this row's site are loaded so the
                          // check-in toggle label matches the overview badge.
                          if (open) selectRow(ch.id);
                        }}
                        items={connectionActions(overview)}
                      />
                    </td>
                  </tr>
                );
              })}
            </DataTable>
          ) : null}
          {modelMatches.length > 0 ? (
            <div className="model-match-group">
              <div className="model-match-head">
                <Boxes size={13} aria-hidden="true" />
                <span>
                  {t("channels.modelMatchHead", { n: modelMatches.length, term: query.trim() })}
                </span>
              </div>
              <ul className="model-match-list">
                {modelMatches.map((match) => (
                  <li key={match.channelId}>
                    <button
                      type="button"
                      className={`model-match-row${
                        selected?.channel.id === match.channelId ? " is-selected" : ""
                      }`}
                      onClick={() => {
                        selectRow(match.channelId);
                        setInspectorOpen(true);
                      }}
                    >
                      <span className="model-match-name">{match.name}</span>
                      {match.model ? (
                        <span className="pg-chip mono">
                          {t("channels.modelMatchHit", { model: match.model })}
                        </span>
                      ) : null}
                      <span className="muted model-match-source">
                        {t(`channels.modelMatchSource.${match.source}`)}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </ListShell>
        {contextMenu
          ? (() => {
              const overview =
                rows.find((row) => row.channel.id === contextMenu.channelId) ??
                (overviews.data ?? []).find((row) => row.channel.id === contextMenu.channelId);
              if (!overview) return null;
              return (
                <ActionMenu
                  key={overview.channel.id}
                  label={t("common.moreActions")}
                  title={overview.channel.name}
                  open
                  onOpenChange={(open) => {
                    if (!open) setContextMenu(null);
                  }}
                  position={{ top: contextMenu.top, left: contextMenu.left }}
                  items={connectionActions(overview, { closeContext: true })}
                />
              );
            })()
          : null}
      </EntityState>
    </Panel>
  );
}
