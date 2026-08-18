import type {
  AuditEvent,
  BackupRecord,
  Channel,
  ChannelOverview,
  ConnectionCreateResponse,
  CheckinLog,
  CreatedDownstreamKey,
  Credential,
  UsageRecord,
  UsageSummary,
  DiscoveredModel,
  DownstreamKey,
  ExchangeEnvelope,
  ImportResult,
  ModuleStatus,
  PluginRecord,
  AccountProbeResult,
  ChannelPingResult,
  FinanceItem,
  ModelMetadata,
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
  RunResult,
  RunSummary,
  StickySnapshot,
  RuntimeEditableSettings,
  RuntimeSettings,
  Site,
} from "./types";

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
    public readonly retryAfter?: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export class ApiClient {
  constructor(
    private readonly token: string,
    private readonly onUnauthorized?: () => void,
  ) {}

  /** Raw bearer token, needed for iframe plugin embedding (?t=). */
  getToken(): string {
    return this.token;
  }

  async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    headers.set("Authorization", `Bearer ${this.token}`);
    if (init.body && !headers.has("Content-Type"))
      headers.set("Content-Type", "application/json");
    let response: Response;
    try {
      response = await fetch(path, { ...init, headers });
    } catch {
      throw new ApiError(0, "Unable to reach Meta Gateway");
    }
    if (!response.ok) {
      if (response.status === 401) this.onUnauthorized?.();
      let message = `Request failed (${response.status})`;
      try {
        const body: unknown = await response.json();
        if (isErrorBody(body)) message = body.error;
        else if (isRecord(body)) {
          if (typeof body.message === "string" && body.message.trim()) {
            message = body.message;
          } else if (
            typeof body.category === "string" &&
            body.category.trim()
          ) {
            message = body.category;
          }
        }
      } catch {
        /* Stable status fallback. */
      }
      const retry = Number.parseInt(
        response.headers.get("Retry-After") ?? "",
        10,
      );
      throw new ApiError(
        response.status,
        message,
        Number.isFinite(retry) ? retry : undefined,
      );
    }
    if (response.status === 204) return undefined as T;
    return response.json() as Promise<T>;
  }

  get<T>(path: string, signal?: AbortSignal) {
    return this.request<T>(path, { signal });
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
  updateSite: (id: number, body: Partial<Site>) =>
    client.put<Site>(`/admin/sites/${id}`, body),
  credentials: (siteId: number, signal?: AbortSignal) =>
    client.getList<Credential>(`/admin/sites/${siteId}/credentials`, signal),
  createCredential: (
    siteId: number,
    body: {
      kind: string;
      secret: string;
      meta_json?: string;
      status: string;
      models_csv?: string;
    },
  ) => client.post<Credential>(`/admin/sites/${siteId}/credentials`, body),
  updateCredential: (
    id: number,
    body: {
      kind?: string;
      secret?: string;
      meta_json?: string;
      status?: string;
      models_csv?: string;
    },
  ) => client.put<Credential>(`/admin/credentials/${id}`, body),
  deleteCredential: (id: number) => client.delete(`/admin/credentials/${id}`),
  revealCredential: (siteId: number, id: number) =>
    client.post<{ secret: string }>(
      `/admin/sites/${siteId}/credentials/${id}/reveal`,
      {},
    ),
  setCheckin: (id: number, enabled: boolean) =>
    client.put<{ credential_id: number; checkin_enabled: boolean }>(
      `/admin/credentials/${id}/checkin`,
      { enabled },
    ),
  runCredential: (id: number) =>
    client.post<RunResult>(`/admin/checkin/credentials/${id}/run`),
  channels: (signal?: AbortSignal) =>
    client.getList<Channel>("/admin/channels", signal),
  createConnection: (body: {
    name?: string;
    base_url: string;
    secret: string;
    type_hint?: string;
    platform?: string;
    status?: string;
    models_csv?: string;
    group_name?: string;
  }) => client.post<ConnectionCreateResponse>("/admin/connections", body),
  channelOverviews: (signal?: AbortSignal) =>
    client.getList<ChannelOverview>("/admin/channels/overview", signal),
  createChannel: (body: Partial<Channel>) =>
    client.post<Channel>("/admin/channels", body),
  updateChannel: (id: number, body: Partial<Channel>) =>
    client.put<Channel>(`/admin/channels/${id}`, body),
  deleteChannel: (id: number) => client.delete(`/admin/channels/${id}`),
  duplicateChannel: (id: number) =>
    client.post<Channel>(`/admin/channels/${id}/duplicate`, {}),
  factoryReset: (confirm: string) =>
    client.post<{ deleted: Record<string, number> }>("/admin/reset", {
      confirm,
    }),
  lastDBGC: (signal?: AbortSignal) =>
    client.get<{ result: DBGCResult | null; ran_at?: string }>(
      "/admin/db/gc",
      signal,
    ),
  runDBGC: () => client.post<DBGCResult>("/admin/db/gc", {}),
  globalSearch: (q: string, signal?: AbortSignal) =>
    client.get<SearchHits>(`/admin/search?q=${encodeURIComponent(q)}`, signal),
  routeOverviews: (signal?: AbortSignal) =>
    client.getList<RouteOverview>("/admin/routes/overview", signal),
  createRoute: (body: Partial<Route>) =>
    client.post<Route>("/admin/routes", body),
  updateRoute: (id: number, body: Partial<Route>) =>
    client.put<Route>(`/admin/routes/${id}`, body),
  deleteRoute: (id: number) => client.delete(`/admin/routes/${id}`),
  createMember: (routeId: number, body: Partial<RouteMember>) =>
    client.post<RouteMember>(`/admin/routes/${routeId}/members`, body),
  updateMember: (id: number, body: Partial<RouteMember>) =>
    client.put<RouteMember>(`/admin/route-members/${id}`, body),
  clearMemberHealth: (id: number) =>
    client.post<RouteMember>(`/admin/route-members/${id}/clear-health`),
  deleteMember: (id: number) => client.delete(`/admin/route-members/${id}`),
  explain: (model: string, signal?: AbortSignal) =>
    client.get<RouteExplanation>(
      `/admin/routes/explain?model=${encodeURIComponent(model)}`,
      signal,
    ),
  sticky: (signal?: AbortSignal) =>
    client.get<StickySnapshot>("/admin/sticky", signal),
  keys: (signal?: AbortSignal) =>
    client.getList<DownstreamKey>("/admin/downstream-keys", signal),
  createKey: (body: {
    name: string;
    scopes?: string;
    token?: string;
    quota_total_tokens?: number;
    price_prompt_per_1k?: number;
    price_completion_per_1k?: number;
    price_cache_per_1k?: number;
    model_allowlist?: string;
    model_denylist?: string;
    expires_at?: string;
    allowed_ips?: string;
  }) => client.post<CreatedDownstreamKey>("/admin/downstream-keys", body),
  updateKey: (
    id: number,
    body: {
      name?: string;
      enabled?: boolean;
      scopes?: string;
      quota_total_tokens?: number;
      price_prompt_per_1k?: number;
      price_completion_per_1k?: number;
      price_cache_per_1k?: number;
      model_allowlist?: string;
      model_denylist?: string;
      expires_at?: string;
      allowed_ips?: string;
       reset_used?: boolean;
    },
  ) => client.put<DownstreamKey>(`/admin/downstream-keys/${id}`, body),
  deleteKey: (id: number) => client.delete(`/admin/downstream-keys/${id}`),
  revealKey: (id: number) =>
    client.post<{ token: string }>(`/admin/downstream-keys/${id}/reveal`, {}),
  rotateKey: (id: number) =>
    client.post<{ id: number; token: string }>(
      `/admin/downstream-keys/${id}/rotate`,
      {},
    ),
  usageSummary: (
    downstreamKeyId?: number,
    signal?: AbortSignal,
    since?: string,
  ) => {
    const query = new URLSearchParams();
    if (downstreamKeyId != null)
      query.set("downstream_key_id", String(downstreamKeyId));
    if (since) query.set("since", since);
    const suffix = query.size ? `?${query.toString()}` : "";
    return client.get<UsageSummary>(`/admin/usage/summary${suffix}`, signal);
  },
  usageRecords: (
    filters?: {
      downstream_key_id?: number;
      channel_id?: number;
      model?: string;
      limit?: number;
    },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams();
    if (filters?.downstream_key_id != null)
      query.set("downstream_key_id", String(filters.downstream_key_id));
    if (filters?.channel_id != null)
      query.set("channel_id", String(filters.channel_id));
    if (filters?.model) query.set("model", filters.model);
    if (filters?.limit != null) query.set("limit", String(filters.limit));
    const suffix = query.size ? `?${query.toString()}` : "";
    return client.getList<UsageRecord>(`/admin/usage${suffix}`, signal);
  },
  proxyLogs: (
    filters?: {
      site_id?: number;
      channel_id?: number;
      model?: string;
      status?: number | "failed";
      upstream_request_id?: string;
      before_id?: number;
      limit?: number;
    },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams();
    if (filters?.site_id != null) query.set("site_id", String(filters.site_id));
    if (filters?.channel_id != null)
      query.set("channel_id", String(filters.channel_id));
    if (filters?.model) query.set("model", filters.model);
    if (filters?.status != null) query.set("status", String(filters.status));
    if (filters?.upstream_request_id)
      query.set("upstream_request_id", filters.upstream_request_id);
    if (filters?.before_id != null)
      query.set("before_id", String(filters.before_id));
    if (filters?.limit != null) query.set("limit", String(filters.limit));
    const suffix = query.size ? `?${query.toString()}` : "";
    return client.getList<ProxyLog>(`/admin/proxy-logs${suffix}`, signal);
  },
  proxyLogLatencyHistogram: (sample = 1000, signal?: AbortSignal) =>
    client.get<{
      buckets: number[];
      total: number;
      slow_count: number;
      p50_ms: number;
      p95_ms: number;
    }>(`/admin/proxy-logs/latency-histogram?sample=${sample}`, signal),
  discoveredModels: (channelId?: number, signal?: AbortSignal) =>
    client.getList<DiscoveredModel>(
      `/admin/discovery/models${channelId ? `?channel_id=${channelId}` : ""}`,
      signal,
    ),
  missingModels: (signal?: AbortSignal) =>
    client.get<{
      items: Array<{
        model: string;
        channel_id: number;
        channel_name: string;
        source: "models_csv" | "discovered";
      }>;
    }>("/admin/discovery/missing-models", signal),
  probeChannel: (id: number) =>
    client.post<ProbeResult>(`/admin/discovery/channels/${id}/probe`),
  tryChat: (body: {
    model: string;
    prompt?: string;
    max_tokens?: number;
    channel_id?: number;
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
    }>("/admin/try/chat", body),
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
    client.get<{ items: FinanceItem[] }>(
      "/admin/channels/account/finance",
      signal,
    ),
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
    client.delete(
      `/admin/model-blocks?channel_id=${channelId}&model=${encodeURIComponent(model)}`,
    ),
  createRedemptionCodes: (body: {
    count: number;
    quota_tokens: number;
    expires_at?: string;
  }) =>
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
  deleteRedemptionCode: (id: number) =>
    client.delete(`/admin/redemption-codes/${id}`),
  totpStatus: (signal?: AbortSignal) =>
    client.get<{ enabled: boolean }>("/admin/totp/status", signal),
  totpSetup: () =>
    client.post<{ secret: string; otpauth_uri: string }>(
      "/admin/totp/setup",
      {},
    ),
  totpEnable: (code: string) =>
    client.post<{ enabled: boolean }>("/admin/totp/enable", { code }),
  totpDisable: (code: string) =>
    client.post<{ enabled: boolean }>("/admin/totp/disable", { code }),
  modelMetadata: (signal?: AbortSignal) =>
    client.get<{ items: ModelMetadata[] }>("/admin/model-metadata", signal),
  upsertModelMetadata: (name: string, body: Partial<ModelMetadata>) =>
    client.put<ModelMetadata>(
      `/admin/model-metadata/${encodeURIComponent(name)}`,
      body,
    ),
  deleteModelMetadata: (name: string) =>
    client.delete(`/admin/model-metadata/${encodeURIComponent(name)}`),
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
  createAlertRule: (body: Partial<AlertRule>) =>
    client.post<AlertRule>("/admin/alert-rules", body),
  updateAlertRule: (id: number, body: Partial<AlertRule>) =>
    client.put<AlertRule>(`/admin/alert-rules/${id}`, body),
  deleteAlertRule: (id: number) => client.delete(`/admin/alert-rules/${id}`),
  promptGuards: (signal?: AbortSignal) =>
    client.get<{ items: PromptGuardRule[] }>("/admin/prompt-guards", signal),
  createPromptGuard: (body: Partial<PromptGuardRule>) =>
    client.post<PromptGuardRule>("/admin/prompt-guards", body),
  updatePromptGuard: (id: number, body: Partial<PromptGuardRule>) =>
    client.put<PromptGuardRule>(`/admin/prompt-guards/${id}`, body),
  deletePromptGuard: (id: number) =>
    client.delete(`/admin/prompt-guards/${id}`),
  healthHistory: (channelId: number, signal?: AbortSignal) =>
    client.get<{ items: HealthPoint[] }>(
      `/admin/health-history?channel_id=${channelId}`,
      signal,
    ),
  healthSummary: (hours = 24, signal?: AbortSignal) =>
    client.get<{ items: HealthSummaryItem[] }>(
      `/admin/health-history/summary?hours=${hours}`,
      signal,
    ),
  decisionSnapshot: (requestId: string, signal?: AbortSignal) =>
    client.get<{
      id: number;
      request_id: string;
      model: string;
      route_id: number;
      selected_channel_id: number;
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
      `/admin/decision-snapshot?request_id=${encodeURIComponent(requestId)}`,
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
  ) =>
    client.post<CreateUpstreamKeyResult>(
      `/admin/channels/${id}/account/create-key`,
      body,
    ),
  tokenGroups: (id: number, signal?: AbortSignal) =>
    client.get<{ groups: string[] }>(
      `/admin/channels/${id}/account/token-groups`,
      signal,
    ),
  refreshChannel: (id: number) =>
    client.post<RefreshResult>(`/admin/discovery/channels/${id}/refresh`),
  refreshAll: () => client.post<RefreshSummary>("/admin/discovery/refresh"),
  pingChannel: (id: number) =>
    client.post<ChannelPingResult>(`/admin/channels/${id}/ping`),
  checkinLogs: (query: string, signal?: AbortSignal) =>
    client.getList<CheckinLog>(`/admin/checkin/logs${query}`, signal),
  runAllCheckins: () => client.post<RunSummary>("/admin/checkin/run"),
  auditEvents: (beforeId?: number, signal?: AbortSignal) =>
    client.getList<AuditEvent>(
      `/admin/audit-events?limit=100${beforeId ? `&before_id=${beforeId}` : ""}`,
      signal,
    ),
  cleanupAudit: () =>
    client.post<{ removed: number }>("/admin/audit-events/cleanup"),
  backups: (signal?: AbortSignal) =>
    client.getList<BackupRecord>("/admin/backups", signal),
  createBackup: () => client.post<BackupRecord>("/admin/backups"),
  runtimeSettings: (signal?: AbortSignal) =>
    client.get<RuntimeSettings>("/admin/runtime-settings", signal),
  updateRuntimeSettings: (body: RuntimeEditableSettings) =>
    client.put<RuntimeSettings>("/admin/runtime-settings", body),
  resetRuntimeSettings: () =>
    client.post<RuntimeSettings>("/admin/runtime-settings/reset"),
  exportData: (includeSecrets: boolean, channelIds: number[]) =>
    client.post<ExchangeEnvelope>("/admin/exchange/export", {
      include_secrets: includeSecrets,
      channel_ids: channelIds,
    }),
  importData: (document: unknown) =>
    client.post<ImportResult>("/admin/exchange/import", document),
  webdavStatus: (signal?: AbortSignal) =>
    client.get<WebDAVStatus>("/admin/webdav/status", signal),
  webdavSettings: (signal?: AbortSignal) =>
    client.get<WebDAVSettings>("/admin/webdav/settings", signal),
  updateWebdavSettings: (body: WebDAVSettingsUpdate) =>
    client.put<WebDAVSettings>("/admin/webdav/settings", body),
  webdavTest: () => client.post<WebDAVSyncResult>("/admin/webdav/test"),
  webdavSync: (mode: WebDAVSyncMode = "incremental") =>
    client.post<WebDAVSyncResult>("/admin/webdav/sync", { mode }),
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
        source: { id: string; name: string; url: string };
      }>;
    }>("/admin/plugins/market", signal),
  installMarketPlugin: (id: string) =>
    client.post<PluginRecord>(
      `/admin/plugins/market/${encodeURIComponent(id)}/install`,
      {},
    ),
  pluginsStatus: (signal?: AbortSignal) =>
    client.getList<ModuleStatus>("/admin/plugins/status", signal),
  plugins: (signal?: AbortSignal) =>
    client.getList<PluginRecord>("/admin/plugins", signal),
  activatePlugin: (id: string) =>
    client.post<PluginRecord>(
      `/admin/plugins/${encodeURIComponent(id)}/activate`,
    ),
  disablePlugin: (id: string) =>
    client.post<PluginRecord>(
      `/admin/plugins/${encodeURIComponent(id)}/disable`,
    ),
  uninstallPlugin: (id: string) =>
    client.delete(`/admin/plugins/${encodeURIComponent(id)}`),
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
