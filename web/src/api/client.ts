import type { AuditEvent, BackupRecord, Channel, CheckinLog, CreatedDownstreamKey, Credential, DiscoveredModel, DownstreamKey, ExchangeEnvelope, ImportResult, ProxyLog, RefreshResult, RefreshSummary, Route, RouteExplanation, RouteMember, RunResult, RunSummary, Site } from './types'

export class ApiError extends Error {
  constructor(public readonly status: number, message: string, public readonly retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
  }
}

export class ApiClient {
  constructor(private readonly token: string, private readonly onUnauthorized?: () => void) {}

  async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers)
    headers.set('Accept', 'application/json')
    headers.set('Authorization', `Bearer ${this.token}`)
    if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
    let response: Response
    try {
      response = await fetch(path, { ...init, headers })
    } catch {
      throw new ApiError(0, 'Unable to reach Meta Gateway')
    }
    if (!response.ok) {
      if (response.status === 401) this.onUnauthorized?.()
      let message = `Request failed (${response.status})`
      try {
        const body: unknown = await response.json()
        if (isErrorBody(body)) message = body.error
      } catch { /* Stable status fallback. */ }
      const retry = Number.parseInt(response.headers.get('Retry-After') ?? '', 10)
      throw new ApiError(response.status, message, Number.isFinite(retry) ? retry : undefined)
    }
    if (response.status === 204) return undefined as T
    return response.json() as Promise<T>
  }

  get<T>(path: string, signal?: AbortSignal) { return this.request<T>(path, { signal }) }
  async getList<T>(path: string, signal?: AbortSignal) {
    return (await this.get<T[] | null>(path, signal)) ?? []
  }
  post<T>(path: string, body?: unknown) { return this.request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) }) }
  put<T>(path: string, body: unknown) { return this.request<T>(path, { method: 'PUT', body: JSON.stringify(body) }) }
  delete(path: string) { return this.request<{ status: string }>(path, { method: 'DELETE' }) }
}

function isErrorBody(value: unknown): value is { error: string } {
  return typeof value === 'object' && value !== null && 'error' in value && typeof value.error === 'string'
}

export const api = (client: ApiClient) => ({
  sites: (signal?: AbortSignal) => client.getList<Site>('/admin/sites', signal),
  createSite: (body: Partial<Site>) => client.post<Site>('/admin/sites', body),
  updateSite: (id: number, body: Partial<Site>) => client.put<Site>(`/admin/sites/${id}`, body),
  deleteSite: (id: number) => client.delete(`/admin/sites/${id}`),
  credentials: (siteId: number, signal?: AbortSignal) => client.getList<Credential>(`/admin/sites/${siteId}/credentials`, signal),
  createCredential: (siteId: number, body: { kind: string; secret: string; meta_json?: string; status: string }) => client.post<Credential>(`/admin/sites/${siteId}/credentials`, body),
  deleteCredential: (id: number) => client.delete(`/admin/credentials/${id}`),
  setCheckin: (id: number, enabled: boolean) => client.put<{ credential_id: number; checkin_enabled: boolean }>(`/admin/credentials/${id}/checkin`, { enabled }),
  runCredential: (id: number) => client.post<RunResult>(`/admin/checkin/credentials/${id}/run`),
  channels: (signal?: AbortSignal) => client.getList<Channel>('/admin/channels', signal),
  createChannel: (body: Partial<Channel>) => client.post<Channel>('/admin/channels', body),
  updateChannel: (id: number, body: Partial<Channel>) => client.put<Channel>(`/admin/channels/${id}`, body),
  deleteChannel: (id: number) => client.delete(`/admin/channels/${id}`),
  routes: (signal?: AbortSignal) => client.getList<Route>('/admin/routes', signal),
  createRoute: (body: Partial<Route>) => client.post<Route>('/admin/routes', body),
  updateRoute: (id: number, body: Partial<Route>) => client.put<Route>(`/admin/routes/${id}`, body),
  deleteRoute: (id: number) => client.delete(`/admin/routes/${id}`),
  members: (routeId: number, signal?: AbortSignal) => client.getList<RouteMember>(`/admin/routes/${routeId}/members`, signal),
  createMember: (routeId: number, body: Partial<RouteMember>) => client.post<RouteMember>(`/admin/routes/${routeId}/members`, body),
  updateMember: (id: number, body: Partial<RouteMember>) => client.put<RouteMember>(`/admin/route-members/${id}`, body),
  deleteMember: (id: number) => client.delete(`/admin/route-members/${id}`),
  explain: (model: string, signal?: AbortSignal) => client.get<RouteExplanation>(`/admin/routes/explain?model=${encodeURIComponent(model)}`, signal),
  keys: (signal?: AbortSignal) => client.getList<DownstreamKey>('/admin/downstream-keys', signal),
  createKey: (body: { name: string; scopes: string }) => client.post<CreatedDownstreamKey>('/admin/downstream-keys', body),
  deleteKey: (id: number) => client.delete(`/admin/downstream-keys/${id}`),
  proxyLogs: (signal?: AbortSignal) => client.getList<ProxyLog>('/admin/proxy-logs', signal),
  discoveredModels: (channelId?: number, signal?: AbortSignal) => client.getList<DiscoveredModel>(`/admin/discovery/models${channelId ? `?channel_id=${channelId}` : ''}`, signal),
  refreshChannel: (id: number) => client.post<RefreshResult>(`/admin/discovery/channels/${id}/refresh`),
  refreshAll: () => client.post<RefreshSummary>('/admin/discovery/refresh'),
  checkinLogs: (query: string, signal?: AbortSignal) => client.getList<CheckinLog>(`/admin/checkin/logs${query}`, signal),
  runAllCheckins: () => client.post<RunSummary>('/admin/checkin/run'),
  auditEvents: (beforeId?: number, signal?: AbortSignal) => client.getList<AuditEvent>(`/admin/audit-events?limit=100${beforeId ? `&before_id=${beforeId}` : ''}`, signal),
  cleanupAudit: () => client.post<{ removed: number }>('/admin/audit-events/cleanup'),
  backups: (signal?: AbortSignal) => client.getList<BackupRecord>('/admin/backups', signal),
  createBackup: () => client.post<BackupRecord>('/admin/backups'),
  exportData: (includeSecrets: boolean, channelIds: number[]) => client.post<ExchangeEnvelope>('/admin/exchange/export', { include_secrets: includeSecrets, channel_ids: channelIds }),
  importData: (document: unknown) => client.post<ImportResult>('/admin/exchange/import', document),
})
