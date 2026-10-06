import type { Channel, DownstreamKey, LatencyHistogram, ProxyLog } from "../../api/types";

/** Log filters, as the page builds them from the URL. */
export type LogFilters = {
  channel_id?: number;
  downstream_key_id?: number;
  model?: string;
  status?: "failed";
  upstream_request_id?: string;
  q?: string;
  since?: string;
  until?: string;
  limit?: number;
};

/**
 * Where the logs page gets its data.
 *
 * Same contract as the keys page: one renderer, two sources. The console reads
 * `/admin/proxy-logs` with channel names and routing-decision snapshots; the
 * member app reads its own `/me/requests`, where those operator views do not
 * exist — which is exactly what the capability flags express.
 */
export type LogsSource = {
  logs: (filters: LogFilters, signal?: AbortSignal) => Promise<ProxyLog[]>;
  /** Latency buckets; a source without a histogram returns nothing. */
  latencyHistogram?: (
    sample: number,
    signal?: AbortSignal,
    window?: { since?: string; until?: string },
  ) => Promise<LatencyHistogram>;
  /** Channel names for the upstream column. Absent = no upstream view. */
  channels?: (signal?: AbortSignal) => Promise<Channel[]>;
  keys: (signal?: AbortSignal) => Promise<DownstreamKey[]>;
  /** On-demand routing decision for one request (console only). */
  decisionSnapshot?: (requestId: string, attempt: number, signal?: AbortSignal) => Promise<unknown>;
};

export type LogsCapabilities = {
  /** Channel names, upstream URL, upstream request id, the routing chain. */
  upstream: boolean;
  /** The routing-decision audit view behind each row. */
  decision: boolean;
  /** Cost columns and the spend summary. */
  pricing: boolean;
  /** The audit-log tab (an operator surface). */
  audit: boolean;
};

export const ADMIN_LOG_CAPS: LogsCapabilities = {
  upstream: true,
  decision: true,
  pricing: true,
  audit: true,
};

export const MEMBER_LOG_CAPS: LogsCapabilities = {
  upstream: false,
  decision: false,
  pricing: true,
  audit: false,
};
