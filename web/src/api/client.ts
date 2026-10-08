import { ApiError } from "../lib/apiError";
export { ApiError } from "../lib/apiError";
import type {
  AuditEvent,
  BackupRecord,
  CatalogPolicyInput,
  CatalogPreview,
  CatalogStatus,
  CatalogSyncState,
  Channel,
  ChannelOverview,
  ConnectionCreateResponse,
  CheckinLog,
  CreatedDownstreamKey,
  KeyCreateInput,
  KeyUpdateInput,
  Credential,
  UsageRecord,
  UsageSummary,
  UsageSeries,
  ModelUsage,
  LatencyHistogram,
  DiscoveredModel,
  DownstreamKey,
  ExchangeEnvelope,
  ExternalCheckin,
  ImportResult,
  ModuleStatus,
  PluginConfigResponse,
  PluginRecord,
  AccountProbeResult,
  ChannelPingResult,
  FinanceItem,
  SiteProbeAction,
  SiteProbeAdoptResult,
  SiteProbeCatalogEntry,
  SiteProbeDetection,
  SiteProbePolicy,
  SiteProbeReport,
  KeepaliveEvent,
  KeepaliveRound,
  KeepaliveStatus,
  SiteKeepaliveInput,
  ModelMetadata,
  ModelCapability,
  ModelChangesResponse,
  ModelDiscardPreview,
  ModelDiscardRequest,
  ModelReplacementRequest,
  ModelReplacementPreview,
  ErrorPassRule,
  DBGCResult,
  AlertRule,
  PromptGuardRule,
  HealthPoint,
  HealthSummaryItem,
  ProbeResult,
  ProxyLog,
  SyncKeysResult,
  CreateUpstreamKeyResult,
  WebDAVStatus,
  WebDAVSyncDirection,
  WebDAVSyncMode,
  WebDAVSyncResult,
  WebDAVSettings,
  WebDAVSettingsUpdate,
  RefreshResult,
  RefreshSummary,
  Route,
  RouteExplanation,
  SearchHits,
  RouteMember,
  RouteOverview,
  ModelChannelMatch,
  ModelMatchMode,
  RunResult,
  RunSummary,
  StickySnapshot,
  RuntimeEditableSettings,
  RuntimeSettings,
  SelfUpdateStatus,
  UpdateCheckStatus,
  Site,
  UnifyApplyResult,
  UnifyGroup,
  UnifyPreview,
  UnifyBatch,
  UnifyOp,
  UnifyRule,
  DeletedRoute,
  ProbeTask,
  ModelProbeResult,
  ModelHealth,
  ProbeStartRequest,
  ChannelModelTestResult,
  PluginHookStatus,
} from "./types";

import { teamCSRF, teamSessionGeneration } from "../team/transport";

export class ApiClient {
  constructor(
    private readonly token: string,
    private readonly onUnauthorized?: () => void,
    private readonly authMode: "bearer" | "cookie" = "bearer",
  ) {}

  private checkSession(generation: number) {
    if (this.authMode === "cookie" && generation !== teamSessionGeneration()) {
      throw new DOMException("Session changed", "AbortError");
    }
  }

  /** Raw bearer token, needed for iframe plugin embedding (?t=). */
  getToken(): string {
    return this.authMode === "cookie" ? "" : this.token;
  }

  async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const generation = teamSessionGeneration();
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (this.authMode === "bearer") headers.set("Authorization", `Bearer ${this.token}`);
    else if (init.method && !["GET", "HEAD"].includes(init.method))
      headers.set("X-Meta-CSRF", teamCSRF());
    if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    let response: Response;
    try {
      response = await fetch(path, { ...init, headers, credentials: "same-origin" });
    } catch (error) {
      if (
        init.signal?.aborted ||
        (typeof error === "object" &&
          error !== null &&
          "name" in error &&
          error.name === "AbortError")
      )
        throw error;
      throw new ApiError(0, "Unable to reach Meta Gateway");
    }
    this.checkSession(generation);
    if (!response.ok) {
      if (response.status === 401) this.onUnauthorized?.();
      let message = `Request failed (${response.status})`;
      try {
        const body: unknown = await response.json();
        if (isErrorBody(body)) message = body.error;
        else if (isRecord(body)) {
          if (typeof body.message === "string" && body.message.trim()) {
            message = body.message;
          } else if (typeof body.category === "string" && body.category.trim()) {
            message = body.category;
          }
        }
      } catch {
        /* Stable status fallback. */
      }
      const retry = Number.parseInt(response.headers.get("Retry-After") ?? "", 10);
      throw new ApiError(response.status, message, Number.isFinite(retry) ? retry : undefined);
    }
    if (response.status === 204) return undefined as T;
    const data = (await response.json()) as T;
    this.checkSession(generation);
    return data;
  }

  get<T>(path: string, signal?: AbortSignal) {
    return this.request<T>(path, { signal });
  }

  /**
   * Opens an SSE endpoint. SSE needs a long-lived response body the caller
   * reads itself, so this bypasses request()'s JSON decode and returns the raw
   * fetch Response (body unread). Callers read resp.body as text and cancel via
   * the signal. A non-2xx rejects with ApiError.
   */
  private async openStream(
    path: string,
    init: RequestInit,
    signal?: AbortSignal,
  ): Promise<Response> {
    const generation = teamSessionGeneration();
    const headers = new Headers(init.headers);
    headers.set("Accept", "text/event-stream");
    if (this.authMode === "bearer") headers.set("Authorization", `Bearer ${this.token}`);
    else if (init.method && !["GET", "HEAD"].includes(init.method))
      headers.set("X-Meta-CSRF", teamCSRF());
    if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    let response: Response;
    try {
      response = await fetch(path, {
        ...init,
        signal,
        headers,
        credentials: "same-origin",
      });
    } catch (error) {
      if (
        signal?.aborted ||
        (typeof error === "object" &&
          error !== null &&
          "name" in error &&
          error.name === "AbortError")
      )
        throw error;
      throw new ApiError(0, "Unable to reach Meta Gateway");
    }
    if (this.authMode === "cookie" && generation !== teamSessionGeneration()) {
      void response.body?.cancel().catch(() => {});
      this.checkSession(generation);
    }
    if (!response.ok) {
      if (response.status === 401) this.onUnauthorized?.();
      let message = `Request failed (${response.status})`;
      try {
        const body: unknown = await response.json();
        if (isErrorBody(body)) message = body.error;
      } catch {
        /* Stable status fallback. */
      }
      throw new ApiError(response.status, message);
    }
    return response;
  }

  /** Opens the admin live-trace SSE stream. */
  openLiveTrace(signal?: AbortSignal): Promise<Response> {
    return this.openStream("/admin/relay/live", { method: "GET" }, signal);
  }

  /**
   * Streams one admin chat probe for the workbench playground. Same contract as
   * openLiveTrace: the caller reads the SSE body and aborts via the signal.
   */
  streamTryChat(
    body: {
      model: string;
      messages?: { role: string; content: string }[];
      system?: string;
      max_tokens?: number;
      temperature?: number;
      top_p?: number;
      channel_id?: number;
      /**
       * Pins one route MEMBER, not a channel: a unified alias can hold several
       * upstream 原模型 names on a single channel, and only a member pin reaches
       * the row the picker listed. Wins over channel_id.
       */
      member_id?: number;
    },
    signal?: AbortSignal,
  ): Promise<Response> {
    return this.openStream(
      "/admin/try/chat",
      { method: "POST", body: JSON.stringify({ ...body, stream: true }) },
      signal,
    );
  }

  /**
   * Asks the gateway to cancel an in-flight relay attempt. The request must
   * currently be running; 404 means it already settled or is unknown.
   */
  interruptLiveRequest(requestId: string) {
    return this.request<{ status: string; request_id: string }>(
      `/admin/relay/live/${encodeURIComponent(requestId)}/interrupt`,
      { method: "POST" },
    );
  }

  async getList<T>(path: string, signal?: AbortSignal) {
    return (await this.get<T[] | null>(path, signal)) ?? [];
  }
  post<T>(path: string, body?: unknown) {
    return this.request<T>(path, {
      method: "POST",
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  }
  put<T>(path: string, body: unknown) {
    return this.request<T>(path, { method: "PUT", body: JSON.stringify(body) });
  }
  delete(path: string) {
    return this.request<{ status: string }>(path, { method: "DELETE" });
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isErrorBody(value: unknown): value is { error: string } {
  return isRecord(value) && typeof value.error === "string";
}

export const api = (client: ApiClient) => ({
  setChannelModelAlias: (id: number, model: string, alias: string) =>
    client.post<{ route_id: number }>(`/admin/channels/${id}/model-alias`, { model, alias }),
  sites: (signal?: AbortSignal) => client.getList<Site>("/admin/sites", signal),
  createSite: (body: Partial<Site>) => client.post<Site>("/admin/sites", body),
  detectSiteType: (url: string, signal?: AbortSignal) =>
    client.get<{
      family?: string;
      site_type?: string;
      title_matched?: boolean;
      evidence?: string;
      title?: string;
    }>(`/admin/site-type?url=${encodeURIComponent(url)}`, signal),
  updateSite: (id: number, body: Partial<Site>) => client.put<Site>(`/admin/sites/${id}`, body),
  /** What URL the gateway would actually call for a base URL, so a wrong join is
   *  visible in the connection editor instead of surfacing as a 404 later. */
  endpointPreview: (url: string, signal?: AbortSignal) =>
    client.get<{
      base_url?: string;
      chat_url?: string;
      models_url?: string;
      endpoint_override?: string;
    }>(`/admin/endpoint-preview?url=${encodeURIComponent(url)}`, signal),
  credentials: (siteId: number, signal?: AbortSignal) =>
    client.getList<Credential>(`/admin/sites/${siteId}/credentials`, signal),
  createCredential: (
    siteId: number,
    body: {
      kind: string;
      auth_mode?: "access_token" | "cookie" | "auto" | string;
      secret?: string;
      cookie?: string;
      meta_json?: string;
      status: string;
      models_csv?: string;
      /** Pool tier: -10 backup, 0 balanced (default), 10 preferred. */
      priority?: number;
    },
  ) => client.post<Credential>(`/admin/sites/${siteId}/credentials`, body),
  updateCredential: (
    id: number,
    body: {
      kind?: string;
      auth_mode?: "access_token" | "cookie" | "auto" | string;
      secret?: string;
      cookie?: string;
      clear_secret?: boolean;
      clear_cookie?: boolean;
      meta_json?: string;
      status?: string;
      models_csv?: string;
      /** Pool tier: -10 backup, 0 balanced, 10 preferred. Omitted = keep. */
      priority?: number;
    },
  ) => client.put<Credential>(`/admin/credentials/${id}`, body),
  deleteCredential: (id: number) => client.delete(`/admin/credentials/${id}`),
  revealCredential: (siteId: number, id: number) =>
    client.post<{ secret: string }>(`/admin/sites/${siteId}/credentials/${id}/reveal`, {}),
  setCheckin: (id: number, enabled: boolean) =>
    client.put<{ credential_id: number; checkin_enabled: boolean }>(
      `/admin/credentials/${id}/checkin`,
      { enabled },
    ),
  runCredential: (id: number) => client.post<RunResult>(`/admin/checkin/credentials/${id}/run`),
  externalCheckins: (signal?: AbortSignal) =>
    client.getList<ExternalCheckin>("/admin/checkin/external", signal),
  createExternalCheckin: (body: {
    name?: string;
    base_url: string;
    checkin_path?: string;
    checkin_method?: string;
    headers?: Record<string, string>;
    cookie: string;
    enabled?: boolean;
  }) => client.post<ExternalCheckin>("/admin/checkin/external", body),
  updateExternalCheckin: (
    siteId: number,
    body: {
      name?: string;
      base_url?: string;
      checkin_path?: string;
      checkin_method?: string;
      headers?: Record<string, string>;
      cookie?: string;
      clear_cookie?: boolean;
      enabled?: boolean;
    },
  ) => client.put<ExternalCheckin>(`/admin/checkin/external/${siteId}`, body),
  deleteExternalCheckin: (siteId: number) => client.delete(`/admin/checkin/external/${siteId}`),
  channels: (signal?: AbortSignal) => client.getList<Channel>("/admin/channels", signal),
  createConnection: (body: {
    name?: string;
    base_url: string;
    secret: string;
    type_hint?: string;
    platform?: string;
    status?: string;
    models_csv?: string;
    group_name?: string;
    /** Omit to inherit runtime_settings.default_model_sync_mode. */
    model_sync_mode?: "auto" | "manual";
  }) => client.post<ConnectionCreateResponse>("/admin/connections", body),
  channelOverviews: (signal?: AbortSignal) =>
    client.getList<ChannelOverview>("/admin/channels/overview", signal),
  createChannel: (body: Partial<Channel>) => client.post<Channel>("/admin/channels", body),
  updateChannel: (id: number, body: Partial<Channel>) =>
    client.put<Channel>(`/admin/channels/${id}`, body),
  deleteChannel: (id: number) => client.delete(`/admin/channels/${id}`),
  duplicateChannel: (id: number) => client.post<Channel>(`/admin/channels/${id}/duplicate`, {}),
  factoryReset: (confirm: string) =>
    client.post<{ deleted: Record<string, number> }>("/admin/reset", {
      confirm,
    }),
  lastDBGC: (signal?: AbortSignal) =>
    client.get<{ result: DBGCResult | null; ran_at?: string }>("/admin/db/gc", signal),
  runDBGC: () => client.post<DBGCResult>("/admin/db/gc", {}),
  globalSearch: (q: string, signal?: AbortSignal) =>
    client.get<SearchHits>(`/admin/search?q=${encodeURIComponent(q)}`, signal),
  routeOverviews: (signal?: AbortSignal) =>
    client.getList<RouteOverview>("/admin/routes/overview", signal),
  // auto_match_channel_ids is create-only: the server attaches one member per
  // listed channel that verifiably serves the pattern (intersection), then
  // drops the flag (not route state). auto_match_mode "related" widens the
  // check to the pattern's -sibling models and rewrites the member's upstream
  // name to whichever of them the channel actually serves.
  createRoute: (
    body: Partial<Route> & {
      auto_match_channel_ids?: number[];
      auto_match_mode?: ModelMatchMode;
    },
  ) => client.post<Route>("/admin/routes", body),
  updateRoute: (id: number, body: Partial<Route>) => client.put<Route>(`/admin/routes/${id}`, body),
  deleteRoute: (id: number) => client.delete(`/admin/routes/${id}`),
  // "Add every channel that serves this model" on an existing route. The list
  // is intersected server-side with the enabled matches, so a stale console
  // selection can never invent a member; group_name empty = the default group.
  // The server treats an *empty list* as "all current matches", so a console
  // that means "none" must not send the request at all.
  autoMatchRouteMembers: (
    routeId: number,
    channelIds: number[],
    groupName?: string,
    match?: ModelMatchMode,
  ) =>
    client.post<{ added: number; skipped: number }>(`/admin/routes/${routeId}/auto-match`, {
      channel_ids: channelIds,
      group_name: groupName ?? "",
      match,
    }),
  createMember: (routeId: number, body: Partial<RouteMember>) =>
    client.post<RouteMember>(`/admin/routes/${routeId}/members`, body),
  updateMember: (id: number, body: Partial<RouteMember>) =>
    client.put<RouteMember>(`/admin/route-members/${id}`, body),
  clearMemberHealth: (id: number) =>
    client.post<RouteMember>(`/admin/route-members/${id}/clear-health`),
  deleteMember: (id: number) => client.delete(`/admin/route-members/${id}`),
  renameMemberGroup: (routeId: number, from: string, to: string) =>
    client.post<{ renamed: number }>(`/admin/routes/${routeId}/groups/rename`, { from, to }),
  copyMemberGroup: (routeId: number, from: string, to: string) =>
    client.post<{ copied: number }>(`/admin/routes/${routeId}/groups/copy`, { from, to }),
  routeGroups: (signal?: AbortSignal) =>
    client.get<{ groups: string[] }>("/admin/route-groups", signal),
  // Tenant groups — the quota/rate-limit container a client token can be bound
  // to. Distinct from routeGroups above, which name model groups inside a
  // route; the two are unrelated despite the shared word.
  keyGroups: (signal?: AbortSignal) => client.get<Array<{ name: string }>>("/admin/groups", signal),
  deleteMemberGroup: (routeId: number, name: string) =>
    client.delete(`/admin/routes/${routeId}/groups/${encodeURIComponent(name)}`),
  explain: (model: string, signal?: AbortSignal, routeGroup?: string) =>
    client.get<RouteExplanation>(
      `/admin/routes/explain?model=${encodeURIComponent(model)}${routeGroup ? `&route_group=${encodeURIComponent(routeGroup)}` : ""}`,
      signal,
    ),
  sticky: (signal?: AbortSignal) => client.get<StickySnapshot>("/admin/sticky", signal),
  keys: (signal?: AbortSignal) => client.getList<DownstreamKey>("/admin/downstream-keys", signal),
  createKey: (body: KeyCreateInput) =>
    client.post<CreatedDownstreamKey>("/admin/downstream-keys", body),
  updateKey: (id: number, body: KeyUpdateInput) =>
    client.put<DownstreamKey>(`/admin/downstream-keys/${id}`, body),
  deleteKey: (id: number) => client.delete(`/admin/downstream-keys/${id}`),
  revealKey: (id: number) =>
    client.post<{ token: string }>(`/admin/downstream-keys/${id}/reveal`, {}),
  rotateKey: (id: number) =>
    client.post<{ id: number; token: string }>(`/admin/downstream-keys/${id}/rotate`, {}),
  usageSummary: (
    downstreamKeyId?: number,
    signal?: AbortSignal,
    since?: string,
    until?: string,
  ) => {
    const query = new URLSearchParams();
    if (downstreamKeyId != null) query.set("downstream_key_id", String(downstreamKeyId));
    if (since) query.set("since", since);
    if (until) query.set("until", until);
    const suffix = query.size ? `?${query.toString()}` : "";
    return client.get<UsageSummary>(`/admin/usage/summary${suffix}`, signal);
  },
  /**
   * Bucketed request/token/cost series over a window. Aggregated in SQL, so a
   * wide window is not distorted by the newest-500 row cap on `usageRecords`.
   */
  usageSeries: (
    filters?: { since?: string; until?: string; buckets?: number },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams();
    if (filters?.since) query.set("since", filters.since);
    if (filters?.until) query.set("until", filters.until);
    if (filters?.buckets != null) query.set("buckets", String(filters.buckets));
    const suffix = query.size ? `?${query.toString()}` : "";
    return client.get<UsageSeries>(`/admin/usage/series${suffix}`, signal);
  },
  usageTopModels: (
    filters?: { since?: string; until?: string; limit?: number },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams();
    if (filters?.since) query.set("since", filters.since);
    if (filters?.until) query.set("until", filters.until);
    if (filters?.limit != null) query.set("limit", String(filters.limit));
    const suffix = query.size ? `?${query.toString()}` : "";
    return client.get<ModelUsage[]>(`/admin/usage/top-models${suffix}`, signal);
  },
  usageRecords: (
    filters?: {
      downstream_key_id?: number;
      channel_id?: number;
      model?: string;
      limit?: number;
      since?: string;
      until?: string;
    },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams();
    if (filters?.downstream_key_id != null)
      query.set("downstream_key_id", String(filters.downstream_key_id));
    if (filters?.channel_id != null) query.set("channel_id", String(filters.channel_id));
    if (filters?.model) query.set("model", filters.model);
    if (filters?.limit != null) query.set("limit", String(filters.limit));
    if (filters?.since) query.set("since", filters.since);
    if (filters?.until) query.set("until", filters.until);
    const suffix = query.size ? `?${query.toString()}` : "";
    return client.getList<UsageRecord>(`/admin/usage${suffix}`, signal);
  },
  proxyLogs: (
    filters?: {
      site_id?: number;
      channel_id?: number;
      /** Restrict to the rows one client token produced. */
      downstream_key_id?: number;
      model?: string;
      status?: number | "failed";
      upstream_request_id?: string;
      before_id?: number;
      limit?: number;
      since?: string;
      until?: string;
      /** Free text across model, error text, path and request ids (FTS5). */
      q?: string;
    },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams();
    if (filters?.q) query.set("q", filters.q);
    if (filters?.site_id != null) query.set("site_id", String(filters.site_id));
    if (filters?.channel_id != null) query.set("channel_id", String(filters.channel_id));
    if (filters?.downstream_key_id != null)
      query.set("downstream_key_id", String(filters.downstream_key_id));
    if (filters?.model) query.set("model", filters.model);
    if (filters?.status != null) query.set("status", String(filters.status));
    if (filters?.upstream_request_id) query.set("upstream_request_id", filters.upstream_request_id);
    if (filters?.before_id != null) query.set("before_id", String(filters.before_id));
    if (filters?.since) query.set("since", filters.since);
    if (filters?.until) query.set("until", filters.until);
    if (filters?.limit != null) query.set("limit", String(filters.limit));
    const suffix = query.size ? `?${query.toString()}` : "";
    return client.getList<ProxyLog>(`/admin/proxy-logs${suffix}`, signal);
  },
  proxyLogLatencyHistogram: (
    sample = 1000,
    signal?: AbortSignal,
    filters?: { since?: string; until?: string },
  ) => {
    const query = new URLSearchParams({ sample: String(sample) });
    if (filters?.since) query.set("since", filters.since);
    if (filters?.until) query.set("until", filters.until);
    return client.get<LatencyHistogram>(
      `/admin/proxy-logs/latency-histogram?${query.toString()}`,
      signal,
    );
  },
  discoveredModels: (channelId?: number, signal?: AbortSignal) =>
    client.getList<DiscoveredModel>(
      `/admin/discovery/models${channelId ? `?channel_id=${channelId}` : ""}`,
      signal,
    ),
  modelChanges: (signal?: AbortSignal) =>
    client.get<ModelChangesResponse>("/admin/models/changes", signal),
  ignoreModelChanges: (ids: number[]) =>
    client.post<{ updated: number }>("/admin/models/changes/ignore", { ids }),
  previewModelReplacement: (input: ModelReplacementRequest) =>
    client.post<ModelReplacementPreview>("/admin/models/changes/preview", input),
  applyModelReplacement: (input: ModelReplacementRequest) =>
    client.post<{ updated: number }>("/admin/models/changes/apply", input),
  previewModelDiscard: (input: ModelDiscardRequest) =>
    client.post<ModelDiscardPreview>("/admin/models/changes/discard-preview", input),
  applyModelDiscard: (input: ModelDiscardRequest) =>
    client.post<{ removed: number; routes: number }>("/admin/models/changes/discard-apply", input),
  missingModels: (signal?: AbortSignal) =>
    client.get<{
      items: Array<{
        model: string;
        channel_id: number;
        channel_name: string;
        source: "models_csv" | "discovered";
      }>;
    }>("/admin/discovery/missing-models", signal),
  // Live preview for the add-route dialog's auto-match: enabled channels whose
  // models.csv or discovery snapshot matches the pattern. "related" also
  // accepts the pattern's -sibling models.
  modelChannels: (model: string, match: ModelMatchMode = "exact", signal?: AbortSignal) =>
    client.get<{ items: ModelChannelMatch[] }>(
      `/admin/discovery/model-channels?model=${encodeURIComponent(model)}&match=${match}`,
      signal,
    ),
  // Omitting rules asks the server for every rule, which yields the simplest
  // canonical form; groups that then need a risky rule come back flagged.
  unifyPreview: (rules?: UnifyRule[]) =>
    client.post<UnifyPreview>("/admin/models/unify/preview", { rules }),
  // Model probing. A probe is a real upstream call with a tiny max_tokens,
  // so it is always an explicit, cancellable task.
  probeStart: (request: ProbeStartRequest) => client.post<ProbeTask>("/admin/probes", request),
  probeTasks: () => client.get<ProbeTask[]>("/admin/probes"),
  probeTask: (id: number, signal?: AbortSignal) =>
    client.get<ProbeTask>(`/admin/probes/${id}`, signal),
  probeResults: (id: number, signal?: AbortSignal) =>
    client.get<ModelProbeResult[]>(`/admin/probes/${id}/results`, signal),
  probeCancel: (id: number) => client.post<{ status: string }>(`/admin/probes/${id}/cancel`, {}),
  modelHealth: (signal?: AbortSignal) => client.get<ModelHealth[]>("/admin/model-health", signal),

  /**
   * Keepalive state, resolved per channel (window, idle age, and whether the
   * next round would call it). The same resolution the scheduler uses, so the
   * page cannot promise something the runner will not do.
   */
  keepalive: (signal?: AbortSignal) => client.get<KeepaliveStatus>("/admin/keepalive", signal),
  keepaliveEvents: (limit = 50, signal?: AbortSignal) =>
    client.get<KeepaliveEvent[]>(`/admin/keepalive/events?limit=${limit}`, signal),
  /** Run one round now: it still only calls what is actually due. */
  keepaliveRun: () => client.post<KeepaliveRound>("/admin/keepalive/run", {}),
  /** Call one channel now, whatever the schedule says (the operator's override). */
  keepaliveSend: (channelId: number) =>
    client.post<KeepaliveEvent>(`/admin/keepalive/channels/${channelId}/send`, {}),
  /**
   * Write one site's call policy and keepalive window. A dedicated endpoint: the
   * site form does not show these columns, so a save from it must not blank them.
   */
  keepaliveSaveSite: (siteId: number, input: SiteKeepaliveInput) =>
    client.put<Site>(`/admin/keepalive/sites/${siteId}`, input),
  /**
   * External site probe data joined with our routes. The policy travels as
   * query parameters so the dialog can preview a different threshold before
   * anything is saved or applied.
   */
  siteProbeReport: (policy: SiteProbePolicy, signal?: AbortSignal) => {
    const query = new URLSearchParams({
      ratio_threshold: String(policy.ratio_threshold),
      min_samples: String(policy.min_samples),
      low_rounds: String(policy.low_rounds),
      high_rounds: String(policy.high_rounds),
    });
    return client.get<SiteProbeReport>(`/admin/site-probe/report?${query}`, signal);
  },
  /** Collect now: one site when ids are given, every enabled site otherwise. */
  siteProbeCollect: (siteIds?: number[]) =>
    client.post<{ collected?: number; failed: number }>(
      "/admin/site-probe/collect",
      siteIds && siteIds.length > 0 ? { site_ids: siteIds } : {},
    ),
  /** dry_run lists what would change; the caller confirms before applying. */
  siteProbeApply: (body: {
    routes?: string[];
    site_ids?: number[];
    policy: SiteProbePolicy;
    dry_run: boolean;
  }) =>
    client.post<{ actions: SiteProbeAction[]; dry_run: boolean }>("/admin/site-probe/apply", body),
  /**
   * Write a site's published price into the members' billing columns. The
   * quote comes from the collected sample, never from the request; the store
   * fills only fields that are still empty.
   */
  siteProbeAdoptPrice: (memberIds: number[]) =>
    client.post<{ results: SiteProbeAdoptResult[] }>("/admin/site-probe/adopt-price", {
      member_ids: memberIds,
    }),
  siteProbeDetect: (url: string) =>
    client.post<SiteProbeDetection>("/admin/site-probe/detect", { url }),
  /**
   * Read a public monitoring directory and match every entry against our
   * sites. Preview only — nothing is written until catalogImport.
   */
  siteProbeCatalogPreview: (catalogUrl?: string) => {
    const query = catalogUrl ? `?url=${encodeURIComponent(catalogUrl)}` : "";
    return client.get<{
      entries: SiteProbeCatalogEntry[];
      catalog_url: string;
    }>(`/admin/site-probe/catalog/preview${query}`);
  },
  /**
   * Apply the previewed plan. The catalog url is required on POST: an import
   * must never silently fall back to the real directory the preview did not
   * use.
   */
  siteProbeCatalogImport: (body: {
    url: string;
    names?: string[];
    collect_now?: boolean;
    create_missing?: boolean;
  }) =>
    client.post<{
      /** Sites that now have a probe source. */
      matched: number;
      /** Sites left alone because they already carry a custom source. */
      skipped: number;
      /** Directory entries we route nothing through (not imported). */
      unmatched: number;
      created: number;
      collected?: number;
      failed?: number;
    }>("/admin/site-probe/catalog/import", body),
  /**
   * Remove the sites nothing routes through — cleanup for the debris an import
   * that created missing sites leaves behind. Sites with a channel or a
   * credential are never touched.
   */
  siteProbeCatalogPrune: () =>
    client.post<{ removed: number }>("/admin/site-probe/catalog/prune", {}),
  saveSiteProbeSource: (body: {
    site_id: number;
    kind: string;
    url: string;
    auto?: boolean;
    enabled: boolean;
    config?: string;
  }) => client.put<Site>("/admin/site-probe/source", body),
  clearSiteProbeSource: (siteId: number) => client.delete(`/admin/site-probe/source/${siteId}`),
  unifyApply: (groups: UnifyGroup[], deleteOriginals = true) =>
    client.post<UnifyApplyResult>("/admin/models/unify/apply", {
      // Removing the originals is what actually unifies a name: a parked
      // (disabled) duplicate still shows up as a dead model row. The server
      // deletes only routes the group provably covers, and snapshots each one
      // so a batch undo can rebuild it.
      delete_originals: deleteOriginals,
      // Every variant travels, covered or not: the server skips the ones that
      // already reach the canonical name (matching on the real upstream model),
      // and a group whose variants are all covered is still the only way to
      // delete an original that an earlier apply left behind. Filtering mapped
      // variants out here made exactly that cleanup impossible.
      groups: groups
        .map((group) => ({
          canonical: group.canonical,
          variants: group.variants.map((variant) => ({
            channel_id: variant.channel_id,
            model_name: variant.model_name,
          })),
        }))
        .filter((group) => group.variants.length > 0),
    }),
  unifyBatches: (signal?: AbortSignal) =>
    client.get<{
      batches: UnifyBatch[];
      deleted: DeletedRoute[];
    }>("/admin/models/unify/batches", signal),
  unifyBatchOps: (id: number, signal?: AbortSignal) =>
    client.get<UnifyOp[]>(`/admin/models/unify/batches/${id}/ops`, signal),
  unifyUndo: (id: number) =>
    client.post<{ status: string }>(`/admin/models/unify/batches/${id}/undo`),
  unifyRestoreRoute: (id: number) =>
    client.post<{ status: string }>(`/admin/models/unify/deleted/${id}/restore`),
  probeChannel: (id: number) => client.post<ProbeResult>(`/admin/discovery/channels/${id}/probe`),
  tryChat: (body: {
    model: string;
    prompt?: string;
    messages?: { role: string; content: string }[];
    system?: string;
    max_tokens?: number;
    temperature?: number;
    top_p?: number;
    channel_id?: number;
    /** Pins one route member (channel × 原模型); see the client method above. */
    member_id?: number;
  }) =>
    client.post<{
      status: number;
      latency_ms: number;
      model: string;
      body: unknown;
      channel_id?: number;
      channel_name?: string;
      member_id?: number;
      priority?: number;
      weight?: number;
      /** The upstream name this attempt actually sent; "" when unchanged. */
      upstream_model?: string;
    }>("/admin/try/chat", body),
  /**
   * Route-free single-model check: "does this channel serve this model at
   * all?". Because it skips route selection it can test models that are not
   * adopted yet, which is exactly the question the connection drawer asks
   * before committing candidates to a route.
   *
   * The upstream verdict is the payload, so a refused model comes back as a
   * 200 with `ok: false`; only a malformed request throws.
   */
  tryChannelModel: (
    body: {
      channel_id: number;
      model: string;
      prompt?: string;
      max_tokens?: number;
    },
    signal?: AbortSignal,
  ) =>
    client.request<ChannelModelTestResult>("/admin/try/channel-model", {
      method: "POST",
      body: JSON.stringify(body),
      signal,
    }),
  /**
   * Streams one playground turn. Returns the raw SSE response; the caller
   * reads `body` and aborts via the signal. First frame is always `meta`.
   */
  streamTryChat: (
    body: {
      model: string;
      messages?: { role: string; content: string }[];
      system?: string;
      max_tokens?: number;
      temperature?: number;
      top_p?: number;
      channel_id?: number;
      /**
       * Pins one route MEMBER, not a channel: a unified alias can hold several
       * upstream 原模型 names on a single channel, and only a member pin reaches
       * the row the picker listed. Wins over channel_id.
       */
      member_id?: number;
    },
    signal?: AbortSignal,
  ) => client.streamTryChat(body, signal),
  tryImage: (body: {
    model: string;
    prompt?: string;
    mode?: "generate" | "edit" | "auto";
    format?: "json" | "multipart";
    size?: string;
    n?: number;
    images?: Array<{ data_url: string; name?: string }>;
    channel_id?: number;
    /** Pins one route member (channel × 原模型); wins over channel_id. */
    member_id?: number;
    include_raw_response?: boolean;
  }) =>
    client.post<{
      status: number;
      latency_ms: number;
      model: string;
      plan: {
        endpoint: string;
        format: string;
        mode: string;
        max_images: number;
        uses_chat_protocol: boolean;
        source: string;
        notes: string;
      };
      images: Array<{ data_url?: string; url?: string; revised_prompt?: string }>;
      body?: unknown;
      channel_id?: number;
      channel_name?: string;
      member_id?: number;
      /** The upstream name this attempt actually sent; "" when unchanged. */
      upstream_model?: string;
    }>("/admin/try/image", body),
  probeAccount: (id: number) =>
    client.post<AccountProbeResult>(`/admin/channels/${id}/account/probe`),
  probeAllAccounts: () =>
    client.post<{
      items: Array<{
        channel_id: number;
        channel_name: string;
        ok: boolean;
        username?: string;
        error?: string;
      }>;
    }>("/admin/channels/account/probe-all"),
  finance: (signal?: AbortSignal) =>
    client.get<{ items: FinanceItem[] }>("/admin/channels/account/finance", signal),
  modelBlocks: (signal?: AbortSignal) =>
    client.get<{
      items: Array<{
        id: number;
        channel_id: number;
        model: string;
        reason: string;
        created_at: string;
      }>;
    }>("/admin/model-blocks", signal),
  unblockModel: (channelId: number, model: string) =>
    client.delete(`/admin/model-blocks?channel_id=${channelId}&model=${encodeURIComponent(model)}`),
  createRedemptionCodes: (body: { count: number; quota_tokens: number; expires_at?: string }) =>
    client.post<{
      items: Array<{ id: number; code: string; quota_tokens: number }>;
    }>("/admin/redemption-codes", body),
  listRedemptionCodes: (signal?: AbortSignal) =>
    client.get<{
      items: Array<{
        id: number;
        code: string;
        quota_tokens: number;
        created_at: string;
        expires_at?: string;
        redeemed_by_key_id: number;
        redeemed_at?: string;
      }>;
    }>("/admin/redemption-codes", signal),
  deleteRedemptionCode: (id: number) => client.delete(`/admin/redemption-codes/${id}`),
  totpStatus: (signal?: AbortSignal) =>
    client.get<{ enabled: boolean }>("/admin/totp/status", signal),
  totpSetup: () => client.post<{ secret: string; otpauth_uri: string }>("/admin/totp/setup", {}),
  totpEnable: (code: string) => client.post<{ enabled: boolean }>("/admin/totp/enable", { code }),
  totpDisable: (code: string) => client.post<{ enabled: boolean }>("/admin/totp/disable", { code }),
  modelMetadata: (signal?: AbortSignal) =>
    client.get<{ items: ModelMetadata[] }>("/admin/model-metadata", signal),
  upsertModelMetadata: (name: string, body: Partial<ModelMetadata>) =>
    client.put<ModelMetadata>(`/admin/model-metadata/${encodeURIComponent(name)}`, body),
  deleteModelMetadata: (name: string) =>
    client.delete(`/admin/model-metadata/${encodeURIComponent(name)}`),
  modelCapabilities: (signal?: AbortSignal) =>
    client.get<{ items: ModelCapability[]; kinds: string[] }>("/admin/model-capabilities", signal),
  upsertModelCapability: (name: string, body: Partial<ModelCapability>) =>
    client.put<ModelCapability>(`/admin/model-capabilities/${encodeURIComponent(name)}`, body),
  deleteModelCapability: (name: string) =>
    client.delete(`/admin/model-capabilities/${encodeURIComponent(name)}`),
  resolveModelCapabilities: (models: string[]) =>
    client.post<{ items: Record<string, ModelCapability> }>("/admin/model-capabilities/resolve", {
      models,
    }),
  autoTagModelCapabilities: (models: string[]) =>
    client.post<{ ok: boolean; requested: number }>("/admin/model-capabilities/auto-tag", {
      models,
    }),
  /** Last sync outcome plus which catalogs are wired in. Never hits the network. */
  catalogStatus: (signal?: AbortSignal) =>
    client.get<CatalogStatus>("/admin/model-capabilities/catalog", signal),
  /**
   * Dry run of a catalog sync. Fetches the indexes, so it can be slow and can
   * fail on a network error; nothing is written.
   */
  previewCatalog: (body: CatalogPolicyInput = {}) =>
    client.post<CatalogPreview>("/admin/model-capabilities/catalog/preview", body),
  /** Applies a sync and records it on the status board. */
  syncCatalog: (body: CatalogPolicyInput = {}) =>
    client.post<{ state: CatalogSyncState; sources: string[] }>(
      "/admin/model-capabilities/catalog/sync",
      body,
    ),
  errorRules: (signal?: AbortSignal) =>
    client.get<{ items: ErrorPassRule[] }>("/admin/error-rules", signal),
  createErrorRule: (body: Partial<ErrorPassRule>) =>
    client.post<ErrorPassRule>("/admin/error-rules", body),
  updateErrorRule: (id: number, body: Partial<ErrorPassRule>) =>
    client.put<ErrorPassRule>(`/admin/error-rules/${id}`, body),
  deleteErrorRule: (id: number) => client.delete(`/admin/error-rules/${id}`),
  alertRules: (signal?: AbortSignal) =>
    client.get<{ items: AlertRule[]; metrics: Record<string, string> }>(
      "/admin/alert-rules",
      signal,
    ),
  createAlertRule: (body: Partial<AlertRule>) => client.post<AlertRule>("/admin/alert-rules", body),
  updateAlertRule: (id: number, body: Partial<AlertRule>) =>
    client.put<AlertRule>(`/admin/alert-rules/${id}`, body),
  deleteAlertRule: (id: number) => client.delete(`/admin/alert-rules/${id}`),
  promptGuards: (signal?: AbortSignal) =>
    client.get<{ items: PromptGuardRule[] }>("/admin/prompt-guards", signal),
  createPromptGuard: (body: Partial<PromptGuardRule>) =>
    client.post<PromptGuardRule>("/admin/prompt-guards", body),
  updatePromptGuard: (id: number, body: Partial<PromptGuardRule>) =>
    client.put<PromptGuardRule>(`/admin/prompt-guards/${id}`, body),
  deletePromptGuard: (id: number) => client.delete(`/admin/prompt-guards/${id}`),
  healthHistory: (channelId: number, signal?: AbortSignal) =>
    client.get<{ items: HealthPoint[] }>(`/admin/health-history?channel_id=${channelId}`, signal),
  healthSummary: (hours = 24, signal?: AbortSignal) =>
    client.get<{ items: HealthSummaryItem[] }>(
      `/admin/health-history/summary?hours=${hours}`,
      signal,
    ),
  decisionSnapshot: (requestId: string, attempt?: number, signal?: AbortSignal) =>
    client.get<{
      id: number;
      request_id: string;
      model: string;
      route_id: number;
      selected_channel_id: number;
      attempt?: number;
      payload: {
        model?: string;
        route_id?: number;
        routing_mode?: string;
        selected_priority?: number | null;
        session_key?: string;
        sticky_channel_id?: number | null;
        sticky_hit?: boolean;
        sticky_reason?: string;
        stable_first_hit?: boolean;
        candidates?: Array<{
          eligible: boolean;
          reasons?: string[];
          score?: number;
          candidate?: { channel?: { id?: number; name?: string } };
        }>;
      };
      created_at: string;
    }>(
      `/admin/decision-snapshot?request_id=${encodeURIComponent(requestId)}${
        attempt && attempt > 0 ? `&attempt=${attempt}` : ""
      }`,
      signal,
    ),
  syncKeys: (
    id: number,
    body?: {
      attach_to_channel?: boolean;
      split_by_group?: boolean;
      max_keys?: number;
    },
  ) =>
    client.post<SyncKeysResult>(`/admin/channels/${id}/account/sync-keys`, {
      // Default: keep keys in one site pool (aggregation). Pass split_by_group: true to opt in.
      split_by_group: false,
      ...(body ?? {}),
    }),
  createUpstreamKey: (
    id: number,
    body: { name?: string; group?: string; unlimited_quota?: boolean },
  ) => client.post<CreateUpstreamKeyResult>(`/admin/channels/${id}/account/create-key`, body),
  tokenGroups: (id: number, signal?: AbortSignal) =>
    client.get<{ groups: string[] }>(`/admin/channels/${id}/account/token-groups`, signal),
  refreshChannel: (id: number) =>
    client.post<RefreshResult>(`/admin/discovery/channels/${id}/refresh`),
  refreshAll: () => client.post<RefreshSummary>("/admin/discovery/refresh"),
  pingChannel: (id: number) => client.post<ChannelPingResult>(`/admin/channels/${id}/ping`),
  checkinLogs: (query: string, signal?: AbortSignal) =>
    client.getList<CheckinLog>(`/admin/checkin/logs${query}`, signal),
  runAllCheckins: () => client.post<RunSummary>("/admin/checkin/run"),
  auditEvents: (beforeId?: number, signal?: AbortSignal) =>
    client.getList<AuditEvent>(
      `/admin/audit-events?limit=100${beforeId ? `&before_id=${beforeId}` : ""}`,
      signal,
    ),
  cleanupAudit: () => client.post<{ removed: number }>("/admin/audit-events/cleanup"),
  backups: (signal?: AbortSignal) => client.getList<BackupRecord>("/admin/backups", signal),
  createBackup: () => client.post<BackupRecord>("/admin/backups"),
  runtimeSettings: (signal?: AbortSignal) =>
    client.get<RuntimeSettings>("/admin/runtime-settings", signal),
  /** The site's money presentation: symbol + rate applied to every amount. */
  displaySettings: (signal?: AbortSignal) =>
    client.get<{ symbol: string; rate: number }>("/admin/display-settings", signal),
  saveDisplaySettings: (body: { symbol: string; rate: number }) =>
    client.put<{ symbol: string; rate: number }>("/admin/display-settings", body),
  updateCheck: (signal?: AbortSignal) =>
    client.get<UpdateCheckStatus>("/admin/update-check", signal),
  refreshUpdateCheck: () => client.post<UpdateCheckStatus>("/admin/update-check/refresh"),
  selfUpdateStatus: (signal?: AbortSignal) =>
    client.get<SelfUpdateStatus>("/admin/self-update", signal),
  applySelfUpdate: (target: string) =>
    client.post<{ started: boolean; target: string; backup?: string }>("/admin/self-update/apply", {
      target,
    }),
  updateRuntimeSettings: (body: RuntimeEditableSettings) =>
    client.put<RuntimeSettings>("/admin/runtime-settings", body),
  resetRuntimeSettings: () => client.post<RuntimeSettings>("/admin/runtime-settings/reset"),
  exportData: (includeSecrets: boolean, channelIds: number[]) =>
    client.post<ExchangeEnvelope>("/admin/exchange/export", {
      include_secrets: includeSecrets,
      channel_ids: channelIds,
    }),
  importData: (document: unknown) => client.post<ImportResult>("/admin/exchange/import", document),
  /**
   * Imports an AAH-encrypted backup. The server decrypts with the same
   * PBKDF2/AES-GCM envelope implementation the WebDAV pull uses.
   */
  importEncryptedData: (document: unknown, password: string) =>
    client.post<ImportResult>("/admin/exchange/import-encrypted", {
      document,
      password,
    }),
  webdavStatus: (signal?: AbortSignal) => client.get<WebDAVStatus>("/admin/webdav/status", signal),
  webdavSettings: (signal?: AbortSignal) =>
    client.get<WebDAVSettings>("/admin/webdav/settings", signal),
  updateWebdavSettings: (body: WebDAVSettingsUpdate) =>
    client.put<WebDAVSettings>("/admin/webdav/settings", body),
  webdavTest: (direction: WebDAVSyncDirection = "download") =>
    client.post<WebDAVSyncResult>("/admin/webdav/test", { direction }),
  webdavSync: (direction: WebDAVSyncDirection = "download", mode: WebDAVSyncMode = "incremental") =>
    client.post<WebDAVSyncResult>("/admin/webdav/sync", { direction, mode }),
  pluginsMarket: (signal?: AbortSignal) =>
    client.get<{
      sources: Array<{ id: string; name: string; url: string }>;
      plugins: Array<{
        id: string;
        name: string;
        description?: string;
        author?: string;
        version?: string;
        logo?: string;
        homepage?: string;
        license?: string;
        tags?: string[];
        url: string;
        install?: { type?: string; artifact_pattern?: string };
        repository?: string;
        source: { id: string; name: string; url: string };
      }>;
    }>("/admin/plugins/market", signal),
  installMarketPlugin: (id: string, options?: { source?: string; version?: string }) => {
    const params = new URLSearchParams();
    if (options?.source) params.set("source", options.source);
    if (options?.version) params.set("version", options.version);
    const qs = params.toString();
    return client.post<PluginRecord>(
      `/admin/plugins/market/${encodeURIComponent(id)}/install${qs ? `?${qs}` : ""}`,
      {},
    );
  },
  pluginsStatus: (signal?: AbortSignal) =>
    client.getList<ModuleStatus>("/admin/plugins/status", signal),
  /**
   * Intercept hooks currently loaded, keyed by plugin. A hook is how a plugin
   * takes part in routing or rewrites requests and answers, so the console
   * must be able to say which plugin sees which models.
   */
  pluginHooks: (signal?: AbortSignal) =>
    client.get<{ hooks: PluginHookStatus[] }>("/admin/plugins/hooks", signal),
  plugins: (signal?: AbortSignal) => client.getList<PluginRecord>("/admin/plugins", signal),
  activatePlugin: (id: string) =>
    client.post<PluginRecord>(`/admin/plugins/${encodeURIComponent(id)}/activate`),
  disablePlugin: (id: string) =>
    client.post<PluginRecord>(`/admin/plugins/${encodeURIComponent(id)}/disable`),
  uninstallPlugin: (id: string) => client.delete(`/admin/plugins/${encodeURIComponent(id)}`),
  pluginConfig: (id: string, signal?: AbortSignal) =>
    client.get<PluginConfigResponse>(`/admin/plugins/${encodeURIComponent(id)}/config`, signal),
  savePluginConfig: (id: string, config: string) =>
    client.put<{ id: string; has_config: boolean }>(
      `/admin/plugins/${encodeURIComponent(id)}/config`,
      { config },
    ),
  updatePlugin: (
    id: string,
    body: {
      url: string;
      apiKey?: string;
      name?: string;
      pagePath?: string;
      healthPath?: string;
      apiPrefix?: string;
    },
  ) =>
    client.put<PluginRecord>(`/admin/plugins/${encodeURIComponent(id)}`, {
      url: body.url,
      ...(body.apiKey !== undefined ? { api_key: body.apiKey } : {}),
      ...(body.name ? { name: body.name } : {}),
      ...(body.pagePath ? { page_path: body.pagePath } : {}),
      ...(body.healthPath ? { health_path: body.healthPath } : {}),
      ...(body.apiPrefix ? { api_prefix: body.apiPrefix } : {}),
    }),
  registerPlugin: (
    url: string,
    apiKey?: string,
    manual?: {
      id?: string;
      name?: string;
      pagePath?: string;
      healthPath?: string;
      apiPrefix?: string;
    },
  ) =>
    client.post<PluginRecord>("/admin/plugins/register", {
      url,
      ...(apiKey ? { api_key: apiKey } : {}),
      ...(manual?.id ? { id: manual.id } : {}),
      ...(manual?.name ? { name: manual.name } : {}),
      ...(manual?.pagePath ? { page_path: manual.pagePath } : {}),
      ...(manual?.healthPath ? { health_path: manual.healthPath } : {}),
      ...(manual?.apiPrefix ? { api_prefix: manual.apiPrefix } : {}),
    }),
});
