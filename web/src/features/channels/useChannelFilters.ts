import { useEffect, useState } from "react";
import type { SetURLSearchParams } from "react-router-dom";
import type { ConnectionHealthFilter } from "./helpers";
import { readChannelTab, writeChannelTab } from "./tabState";

/**
 * The board's four filters, and the two places they live.
 *
 * They were six useState declarations, seven effects and two helpers scattered
 * through the page component, with the URL↔state and state↔sessionStorage rules
 * interleaved among unrelated deep-link effects. The rules are worth stating once:
 *
 *  - **the URL wins when it carries a value** — a shared link with ?health=ready
 *    must apply, but a bare `/channels` navigation must not wipe what the tab
 *    remembered;
 *  - **the tab remembers** — the sidebar links to a bare path, so without
 *    sessionStorage every page switch would silently reset the filters;
 *  - **changing a filter drops the selection** (`id`) — the row the operator was
 *    looking at may not be in the new list, and every surface that shows "the
 *    selected channel" would otherwise keep pointing at a row that is filtered out.
 */
export type ChannelFilterState = {
  query: string;
  setQuery: (value: string) => void;
  healthFilter: ConnectionHealthFilter;
  setHealthFilter: (value: ConnectionHealthFilter) => void;
  typeFilter: string;
  setTypeFilter: (value: string) => void;
  groupFilter: string;
  setGroupFilter: (value: string) => void;
  /** Write a filter through to the URL (or remove it when it is the default). */
  updateFilterParam: (key: string, value: string) => void;
  /** Toolbar toggle: the same chip twice clears the filter. */
  toggleHealthFilter: (next: ConnectionHealthFilter) => void;
};

function isHealthFilter(value: string | null): value is ConnectionHealthFilter {
  return value === "ready" || value === "missing_key" || value === "attention";
}

export function useChannelFilters(
  params: URLSearchParams,
  setParams: SetURLSearchParams,
): ChannelFilterState {
  const searchParam = params.get("search") ?? "";
  const [query, setQuery] = useState(() => searchParam || readChannelTab("query", ""));
  const [healthFilter, setHealthFilter] = useState<ConnectionHealthFilter>(() => {
    const fromUrl = params.get("health");
    return isHealthFilter(fromUrl)
      ? fromUrl
      : readChannelTab<ConnectionHealthFilter>("health", "all");
  });
  const [typeFilter, setTypeFilter] = useState(() => {
    const value = params.get("type");
    return value && value !== "all" ? value : readChannelTab("type", "all");
  });
  const [groupFilter, setGroupFilter] = useState(() => {
    const value = params.get("group");
    return value && value !== "all" ? value : readChannelTab("group", "all");
  });

  // URL → state, only when the URL actually carries a value: a bare-path
  // navigation must never clear what the tab restored.
  useEffect(() => {
    if (searchParam) setQuery(searchParam);
  }, [searchParam]);
  useEffect(() => {
    const next = params.get("health");
    if (isHealthFilter(next)) setHealthFilter(next);
  }, [params]);
  useEffect(() => {
    const next = params.get("type");
    if (next && next !== "all") setTypeFilter(next);
  }, [params]);
  useEffect(() => {
    const next = params.get("group");
    if (next && next !== "all") setGroupFilter(next);
  }, [params]);

  // State → tab storage, so the next visit to this tab opens where it left off.
  useEffect(() => writeChannelTab("query", query), [query]);
  useEffect(() => writeChannelTab("health", healthFilter), [healthFilter]);
  useEffect(() => writeChannelTab("type", typeFilter), [typeFilter]);
  useEffect(() => writeChannelTab("group", groupFilter), [groupFilter]);

  const updateFilterParam = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (!value || value === "all") next.delete(key);
    else next.set(key, value);
    next.delete("id");
    setParams(next, { replace: true });
  };

  return {
    query,
    setQuery,
    healthFilter,
    setHealthFilter,
    typeFilter,
    setTypeFilter,
    groupFilter,
    setGroupFilter,
    updateFilterParam,
    toggleHealthFilter: (next: ConnectionHealthFilter) => {
      const value = healthFilter === next ? "all" : next;
      setHealthFilter(value);
      updateFilterParam("health", value);
    },
  };
}
