import type { LogsSource } from "../features/logs/LogsSource";
import type { DownstreamKey, ProxyLog, LatencyHistogram } from "../api/types";
import { accountRequest } from "../team/transport";
import type { RequestRow, UserKey } from "../team/types";

type MemberKey = UserKey & { models?: string; used_tokens?: number; cost?: number };

/**
 * The member app's logs source.
 *
 * `/me/requests` already returns only this account's own rows — the isolation
 * is the server's, not a filter here. What this mapping adds is the shape the
 * shared page expects, with every upstream field left empty: a member's row
 * does not carry a channel, an upstream URL or a routing decision, and the
 * page's capability flags keep those columns out entirely.
 */
export const memberLogsSource: LogsSource = {
  latencyHistogram: (sample, signal, window) => {
    const params = new URLSearchParams({ sample: String(sample) });
    if (window?.since) params.set("since", window.since);
    if (window?.until) params.set("until", window.until);
    return accountRequest<LatencyHistogram>(`/me/requests/latency-histogram?${params}`, { signal });
  },
  logs: async (filters, signal) => {
    const params = new URLSearchParams();
    if (filters.model) params.set("model", filters.model);
    if (filters.downstream_key_id) params.set("key_id", String(filters.downstream_key_id));
    if (filters.status === "failed") params.set("status", "error");
    if (filters.q) params.set("q", filters.q);
    if (filters.since) params.set("since", filters.since);
    if (filters.until) params.set("until", filters.until);
    // The member endpoint caps its own page size; asking for more than it
    // serves would silently truncate, so the page size stays at the default.
    const query = params.toString();
    const rows = await accountRequest<RequestRow[]>(
      `/me/requests${query ? `?${query}` : ""}`,
      { signal },
    );
    return rows.map((row, index) => toProxyLog(row, index));
  },
  keys: async (signal) => {
    const rows = await accountRequest<MemberKey[]>("/me/keys", { signal });
    return rows.map((key) => ({ id: key.id, name: key.name }) as DownstreamKey);
  },
};

/** One member request row, dressed as the page's log shape. */
function toProxyLog(row: RequestRow, index: number): ProxyLog {
  return {
    // A member's row has no numeric id; the request id is what identifies it,
    // so a stable per-render index stands in for the React key and the
    // expand/detail bookkeeping.
    id: index,
    request_id: row.request_id,
    created_at: row.created_at,
    model: row.model ?? "",
    status: row.status,
    latency_ms: row.latency_ms ?? 0,
    total_tokens: row.tokens ?? 0,
    prompt_tokens: row.prompt_tokens ?? 0,
    completion_tokens: row.completion_tokens ?? 0,
    downstream_key_id: row.key_id,
    cache_read_tokens: row.cache_read_tokens ?? 0,
    cache_creation_tokens: row.cache_creation_tokens ?? 0,
    cost: row.cost ?? 0,
    attempt: row.attempts ?? 1,
    // Upstream detail is not the member's to see, and the capability flags
    // remove the columns that would show it.
    channel_id: 0,
    path: row.path,
    upstream_url: "",
    upstream_model: "",
    client_family: row.client_family ?? "",
  };
}
