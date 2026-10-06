import { useEffect, useMemo, useState } from "react";
import type { ModelMetadata, RouteOverview } from "../../api/types";
import { readScopedTabState, writeScopedTabState } from "../../lib/tabState";
import { countActiveModelFilters } from "./modelFilters";
import { modelGroup } from "./modelGroups";

/**
 * The models directory's search and its three filters, the rows they narrow, and
 * the page's slice of them.
 *
 * This is the same shape as the connections board's `useChannelFilters`, with the
 * extra piece this page needs: the filtered rows themselves and their pagination,
 * because four filters, the group list and the paging all describe one question
 * ("which rows am I looking at?") and answering it in three different places is how
 * the header count and the table body came to disagree.
 *
 * Two rules it encodes, both learned the hard way elsewhere in this console:
 *
 *  - **the URL wins on first mount, the tab remembers after** — the filters live
 *    behind a disclosure, so a restored filter used to narrow the list with nothing
 *    on screen saying so; `showModelFilters` therefore opens exactly when a
 *    non-default filter is in force, which makes it self-healing;
 *  - **`activeFilterCount` drives the trigger's badge** so a collapsed panel still
 *    reports that the list is narrowed.
 */
type StatusFilter = "enabled" | "disabled" | "all";

/** Tab-scoped persistence, namespaced; see lib/tabState.ts. */
const SCOPE = "models";

export type ModelFilterState = {
  query: string;
  setQuery: (value: string) => void;
  channelFilter: number;
  setChannelFilter: (value: number) => void;
  groupFilter: string;
  setGroupFilter: (value: string) => void;
  statusFilter: StatusFilter;
  setStatusFilter: (value: StatusFilter) => void;
  showModelFilters: boolean;
  setShowModelFilters: (show: boolean) => void;
  activeFilterCount: number;
  /** Every group any route belongs to, for the filter's options. */
  modelGroups: string[];
  /** The rows that survive the filters. */
  rows: RouteOverview[];
};

export function useModelFilters({
  overviews,
  metaByModel,
  initial,
}: {
  overviews: RouteOverview[];
  metaByModel: Map<string, ModelMetadata>;
  /** Values the URL supplies, which beat the tab's memory on first mount. */
  initial: { model?: string; group?: string; channel?: number };
}): ModelFilterState {
  // Read once, so the states and the filter panel's default-open decision cannot
  // disagree.
  const [initialFilters] = useState(() => ({
    query: initial.model || readScopedTabState(SCOPE, "query", ""),
    channel: initial.channel ?? readScopedTabState(SCOPE, "channel", 0),
    group: initial.group || readScopedTabState(SCOPE, "group", ""),
    status: readScopedTabState<StatusFilter>(SCOPE, "status", "all"),
  }));
  const [query, setQuery] = useState(initialFilters.query);
  const [channelFilter, setChannelFilter] = useState(initialFilters.channel);
  const [groupFilter, setGroupFilter] = useState(initialFilters.group);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(initialFilters.status);
  const [showModelFilters, setShowModelFilters] = useState(
    () => countActiveModelFilters(initialFilters) > 0,
  );

  // A `?channel_id=` deep link (from the connections page) wins over the tab.
  useEffect(() => {
    if (initial.channel) setChannelFilter(initial.channel);
  }, [initial.channel]);

  useEffect(() => writeScopedTabState(SCOPE, "query", query), [query]);
  useEffect(() => writeScopedTabState(SCOPE, "channel", channelFilter), [channelFilter]);
  useEffect(() => writeScopedTabState(SCOPE, "group", groupFilter), [groupFilter]);
  useEffect(() => writeScopedTabState(SCOPE, "status", statusFilter), [statusFilter]);

  const modelGroups = useMemo(() => {
    const groups = new Set<string>();
    for (const item of overviews) {
      const meta = metaByModel.get(item.route.model_pattern);
      groups.add(modelGroup(item.route.model_pattern, item.route.model_group, meta?.vendor));
    }
    return [...groups].sort();
  }, [metaByModel, overviews]);

  const rows = useMemo(() => {
    const term = query.trim().toLowerCase();
    return overviews.filter((item) => {
      const meta = metaByModel.get(item.route.model_pattern);
      if (
        groupFilter &&
        modelGroup(item.route.model_pattern, item.route.model_group, meta?.vendor) !== groupFilter
      )
        return false;
      if (statusFilter === "enabled" && !item.route.enabled) return false;
      if (statusFilter === "disabled" && item.route.enabled) return false;
      const members = item.members ?? [];
      if (channelFilter > 0) {
        if (!members.some((m) => m.channel.id === channelFilter)) return false;
      }
      if (!term) return true;
      if (item.route.model_pattern.toLowerCase().includes(term)) return true;
      return members.some((m) => m.channel.name.toLowerCase().includes(term));
    });
  }, [channelFilter, groupFilter, metaByModel, overviews, query, statusFilter]);

  // Pagination stays with the page: it is a presentation choice about the rows
  // this hook returns, not another fact about the filters.
  return {
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
    activeFilterCount: countActiveModelFilters({
      group: groupFilter,
      channel: channelFilter,
      status: statusFilter,
    }),
    modelGroups,
    rows,
  };
}
