import type {
  AuditEvent,
  BackupRecord,
  Channel,
	ChannelOverview,
	CheckinLog,
	CreatedDownstreamKey,
	Credential,
	UsageRecord,
	UsageSummary,
	DiscoveredModel,
	DownstreamKey,
	ExchangeEnvelope,
	ImportResult,
	PluginCatalogEntry,
	ModuleStatus,
	PluginRecord,
	AccountProbeResult,
	ChannelPingResult,
	FinanceItem,
	ModelPrice,
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
					} else if (typeof body.category === "string" && body.category.trim()) {
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
		client.get<{ family?: string; site_type?: string; title_matched?: boolean; evidence?: string; title?: string }>(
			`/admin/site-type?url=${encodeURIComponent(url)}`,
			signal,
		),
	updateSite: (id: number, body: Partial<Site>) =>
		client.put<Site>(`/admin/sites/${id}`, body),
	deleteSite: (id: number) => client.delete(`/admin/sites/${id}`),
	credentials: (siteId: number, signal?: AbortSignal) =>
		client.getList<Credential>(`/admin/sites/${siteId}/credentials`, signal),
	createCredential: (
		siteId: number,
		body: { kind: string; secret: string; meta_json?: string; status: string; models_csv?: string },
	) => client.post<Credential>(`/admin/sites/${siteId}/credentials`, body),
	updateCredential: (
		id: number,
		body: { kind?: string; secret?: string; meta_json?: string; status?: string; models_csv?: string },
	) => client.put<Credential>(`/admin/credentials/${id}`, body),
	deleteCredential: (id: number) => client.delete(`/admin/credentials/${id}`),
	setCheckin: (id: number, enabled: boolean) =>
		client.put<{ credential_id: number; checkin_enabled: boolean }>(
			`/admin/credentials/${id}/checkin`,
			{ enabled },
		),
	runCredential: (id: number) =>
		client.post<RunResult>(`/admin/checkin/credentials/${id}/run`),
	channels: (signal?: AbortSignal) =>
		client.getList<Channel>("/admin/channels", signal),
	channelOverviews: (signal?: AbortSignal) =>
		client.getList<ChannelOverview>("/admin/channels/overview", signal),
	createChannel: (body: Partial<Channel>) =>
		client.post<Channel>("/admin/channels", body),
	updateChannel: (id: number, body: Partial<Channel>) =>
		client.put<Channel>(`/admin/channels/${id}`, body),
	deleteChannel: (id: number) => client.delete(`/admin/channels/${id}`),
	routes: (signal?: AbortSignal) =>
		client.getList<Route>("/admin/routes", signal),
	routeOverviews: (signal?: AbortSignal) =>
		client.getList<RouteOverview>("/admin/routes/overview", signal),
	createRoute: (body: Partial<Route>) =>
		client.post<Route>("/admin/routes", body),
	updateRoute: (id: number, body: Partial<Route>) =>
		client.put<Route>(`/admin/routes/${id}`, body),
	deleteRoute: (id: number) => client.delete(`/admin/routes/${id}`),
	members: (routeId: number, signal?: AbortSignal) =>
		client.getList<RouteMember>(`/admin/routes/${routeId}/members`, signal),
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
		name: string
		scopes?: string
		token?: string
		quota_total_tokens?: number
		price_prompt_per_1k?: number
		price_completion_per_1k?: number
		model_allowlist?: string
		model_denylist?: string
	}) => client.post<CreatedDownstreamKey>("/admin/downstream-keys", body),
	updateKey: (
		id: number,
		body: {
			name?: string
			enabled?: boolean
			scopes?: string
			quota_total_tokens?: number
			price_prompt_per_1k?: number
			price_completion_per_1k?: number
			model_allowlist?: string
			model_denylist?: string
			reset_used?: boolean
		},
	) => client.put<DownstreamKey>(`/admin/downstream-keys/${id}`, body),
	deleteKey: (id: number) => client.delete(`/admin/downstream-keys/${id}`),
	usageSummary: (downstreamKeyId?: number, signal?: AbortSignal) => {
		const query = new URLSearchParams()
		if (downstreamKeyId != null) query.set("downstream_key_id", String(downstreamKeyId))
		const suffix = query.size ? `?${query.toString()}` : ""
		return client.get<UsageSummary>(`/admin/usage/summary${suffix}`, signal)
	},
	usageRecords: (
		filters?: {
			downstream_key_id?: number
			channel_id?: number
			model?: string
			limit?: number
		},
		signal?: AbortSignal,
	) => {
		const query = new URLSearchParams()
		if (filters?.downstream_key_id != null)
			query.set("downstream_key_id", String(filters.downstream_key_id))
		if (filters?.channel_id != null) query.set("channel_id", String(filters.channel_id))
		if (filters?.model) query.set("model", filters.model)
		if (filters?.limit != null) query.set("limit", String(filters.limit))
		const suffix = query.size ? `?${query.toString()}` : ""
		return client.getList<UsageRecord>(`/admin/usage${suffix}`, signal)
	},
	proxyLogs: (
		filters?: {
			site_id?: number;
			channel_id?: number;
			model?: string;
			status?: number | "failed";
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
		client.post<{ items: Array<{ channel_id: number; channel_name: string; ok: boolean; username?: string; error?: string }> }>(
			"/admin/channels/account/probe-all",
		),
	finance: (signal?: AbortSignal) =>
		client.get<{ items: FinanceItem[] }>(
			"/admin/channels/account/finance",
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
		client.get<{ groups: string[] }>(
			`/admin/channels/${id}/account/token-groups`,
			signal,
		),
	pricing: (id: number, signal?: AbortSignal) =>
		client.get<{ prices: ModelPrice[] }>(
			`/admin/channels/${id}/account/pricing`,
			signal,
		),
	refreshChannel: (id: number) =>
		client.post<RefreshResult>(`/admin/discovery/channels/${id}/refresh`),
	refreshAll: () => client.post<RefreshSummary>("/admin/discovery/refresh"),
	pingChannel: (id: number) =>
		client.post<ChannelPingResult>(
			`/admin/channels/${id}/ping`,
		),
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
	pluginsCatalog: (signal?: AbortSignal) =>
		client.getList<PluginCatalogEntry>("/admin/plugins/catalog", signal),
	pluginsStatus: (signal?: AbortSignal) =>
		client.getList<ModuleStatus>("/admin/plugins/status", signal),
	plugins: (signal?: AbortSignal) =>
		client.getList<PluginRecord>("/admin/plugins", signal),
	activatePlugin: (id: string) =>
		client.post<PluginRecord>(
			`/admin/plugins/${encodeURIComponent(id)}/activate`,
		),
	installPlugin: (id: string) =>
		client.post<PluginRecord>(
			`/admin/plugins/${encodeURIComponent(id)}/install`,
		),
	enablePlugin: (id: string) =>
		client.post<PluginRecord>(
			`/admin/plugins/${encodeURIComponent(id)}/enable`,
		),
	disablePlugin: (id: string) =>
		client.post<PluginRecord>(
			`/admin/plugins/${encodeURIComponent(id)}/disable`,
		),
	uninstallPlugin: (id: string) =>
		client.delete(`/admin/plugins/${encodeURIComponent(id)}`),
});
