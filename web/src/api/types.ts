export type Status = 'enabled' | 'disabled'

export interface Site { id: number; name: string; base_url: string; platform: string; status: Status; created_at: string; updated_at: string }
export interface Credential { id: number; site_id: number; kind: string; has_secret: boolean; meta_json?: string; status: Status; checkin_enabled: boolean; created_at?: string }
export interface Channel { id: number; site_id?: number; credential_id?: number; name: string; base_url: string; models_csv: string; group_name: string; priority: number; weight: number; status: Status; type_hint?: string; created_at: string; updated_at: string }
export interface ChannelOverview {
  channel: Channel
  credential_kind?: string
  checkin_enabled: boolean
  has_user_credential: boolean
  has_platform_user_id: boolean
  has_api_key: boolean
  site_usable: boolean
  credential_usable: boolean
  model_count: number
  last_checked_at?: string
  last_latency_ms: number
  discovery_source?: string
  route_count: number
  enabled_member_count: number
  cooling_member_count: number
  failure_count: number
  last_error?: string
  last_probe_at?: string
  last_probe_ok?: boolean
  last_probe_error?: string
}
export interface Route { id: number; model_pattern: string; enabled: boolean; mapping_json?: string; notes?: string; created_at: string; updated_at: string }
export interface RouteMember { id: number; route_id: number; channel_id: number; priority: number; weight: number; enabled: boolean; auto: boolean; manual_override: boolean; fail_count: number; cooldown_until?: string; last_error?: string; created_at: string; updated_at: string }
export interface DownstreamKey {
  id: number
  name: string
  enabled: boolean
  scopes?: string
  quota_total_tokens?: number
  quota_used_tokens?: number
  price_prompt_per_1k?: number
  price_completion_per_1k?: number
  model_allowlist?: string
  model_denylist?: string
  estimated_cost?: number
  created_at: string
}
export interface CreatedDownstreamKey extends DownstreamKey { token: string }
export interface UsageSummary {
  request_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  estimated_cost: number
}
export interface UsageRecord {
  id: number
  request_id: string
  downstream_key_id: number
  channel_id: number
  model: string
  path: string
  stream: boolean
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  status: number
  created_at: string
}
export interface ProxyLog {
  id: number
  request_id: string
  channel_id: number
  route_id?: number
  route_pattern?: string
  model: string
  status: number
  latency_ms: number
  attempt: number
  error_brief?: string
  downstream_key_id?: number
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
  stream?: boolean
  path?: string
  created_at: string
}
export interface DiscoveredModel { id: number; channel_id: number; model_name: string; available: boolean; source: string; latency_ms: number; checked_at: string }
export interface CheckinLog { id: number; site_id: number; credential_id: number; source: string; status: 'success' | 'failed' | 'skipped'; category: string; message: string; reward?: string; latency_ms: number; ran_at: string }
export interface AuditEvent { id: number; request_id?: string; actor_kind: string; actor_id?: number; action: string; resource_kind?: string; resource_id?: number; outcome: string; status_code: number; category?: string; created_at: string }
export interface BackupRecord { id: number; name: string; status: string; size_bytes: number; checksum: string; duration_ms: number; category?: string; created_at: string }

/** Effective runtime parameters (editable subset + env bootstrap). Secrets never included. */
export interface RuntimeEditableSettings {
  retry_times: number
  cooldown_seconds: number
  checkin_enabled: boolean
  checkin_cron: string
  relay_rate_per_minute: number
  relay_rate_burst: number
  admin_rate_per_minute: number
  admin_rate_burst: number
  audit_retention_days: number
  audit_retention_rows: number
}

export interface RuntimeSettings {
  source: 'environment' | 'admin_override' | string
  has_override: boolean
  note: string
  editable: RuntimeEditableSettings
  env_bootstrap: RuntimeEditableSettings
  updated_at?: string
  server_http_addr: string
  data_dir: string
}

export interface RoutingCandidate { member: RouteMember; channel: Channel; credential_usable: boolean }
export interface RouteOverview { route: Route; members: RoutingCandidate[] }
export interface RouteEvaluation { candidate: RoutingCandidate; eligible: boolean; reasons: string[] }
export interface RouteExplanation { model: string; route_id: number; evaluated_at: string; selected_priority?: number; candidates: RouteEvaluation[] }

export interface ProbeResult { channel_id: number; adapter: string; models: string[]; latency_ms: number; checked_at: string }
export interface AccountProbeResult {
  channel_id: number
  credential_id: number
  username: string
  display_name?: string
  platform_user_id?: number
  quota?: number
  used_quota?: number
  latency_ms: number
  checked_at: string
}
export interface SyncKeysResult {
  channel_id: number
  listed: number
  created_credentials: number
  reused_credentials: number
  skipped_masked: number
  empty_list?: boolean
  category?: string
  message?: string
  attached_credential_id?: number
  created_channels?: number
  updated_channels?: number
  group_channels?: Array<{
    group: string
    channel_id: number
    credential_id: number
    name: string
    status: string
  }>
  items: Array<{
    name?: string
    group?: string
    credential_id?: number
    enabled?: boolean
    status: string
    category?: string
  }>
}
export interface CreateUpstreamKeyResult {
  credential_id: number
  name: string
  group: string
  category: string
  message: string
}
export interface RefreshResult extends ProbeResult { created_routes: number; created_members: number; enabled_members: number; deleted_members: number; deleted_routes: number }
export interface RefreshSummary { items: Array<{ channel_id: number; result?: RefreshResult; error?: string }>; success_count: number; failure_count: number }
export interface RunResult { site_id: number; credential_id: number; source: string; status: string; category: string; message: string; reward?: string; latency_ms: number; ran_at: string }
export interface RunSummary { items: RunResult[]; success_count: number; failure_count: number; skipped_count: number }
export interface ImportResult {
  created_count: number
  updated_count: number
  adopted_count: number
  channel_ids: number[]
  discovery: Array<{ channel_id: number; status: string; category?: string }>
  discovery_success_count: number
  discovery_failure_count: number
  checkin_capable_count?: number
  missing_api_key_count?: number
  relay_ready_count?: number
  key_sync_success_count?: number
  key_sync_failure_count?: number
  key_sync_skipped_count?: number
  key_sync?: Array<{
    channel_id: number
    status: string
    category?: string
    created?: number
    reused?: number
    masked?: number
  }>
  items?: Array<{
    channel_id: number
    credential_kind?: string
    checkin_capable: boolean
    has_api_key: boolean
    discovery_status?: string
    discovery_category?: string
  }>
}
export interface ExchangeEnvelope { format: string; version: number; exported_at: string; importable: boolean; items: Array<Record<string, unknown>>; skipped?: Array<{ channel_id: number; name: string; reason: string }> }

export interface PluginCatalogEntry {
  id: string
  name: string
  version: string
  description?: string
  kind?: "core" | "addon" | string
  unlocks?: string[]
  capabilities?: string[]
  source?: string
  checksum?: string
}

export interface PluginRecord {
  id: string
  version: string
  status: string
  enabled: boolean
  source?: string
  checksum?: string
  installed_at?: string
  enabled_at?: string
  meta_json?: string
}

/** Combined store view: core orientation cards + add-ons + orphans. */
export interface ModuleStatus {
  id: string
  name: string
  version: string
  description?: string
  kind: "core" | "addon" | string
  unlocks?: string[]
  capabilities?: string[]
  source?: string
  installed: boolean
  enabled: boolean
  can_toggle: boolean
  open_path?: string
}


export type WebDAVSyncMode = 'incremental' | 'replace'

export interface WebDAVSyncResult {
  status: string
  source: string
  fetched_at: string
  target_url?: string
  bytes?: number
  encrypted?: boolean
  category?: string
  message?: string
  latency_ms?: number
  import?: ImportResult
}

export interface WebDAVStatus {
  configured: boolean
  scheduler_armed: boolean
  target_url?: string
  last?: WebDAVSyncResult
  in_progress: boolean
  source?: string
  enabled?: boolean
  url?: string
  username?: string
  has_password?: boolean
  has_backup_password?: boolean
  cron?: string
}

export interface WebDAVSettings {
  enabled: boolean
  url: string
  username: string
  has_password: boolean
  has_backup_password: boolean
  cron: string
  configured: boolean
  scheduler_armed: boolean
  source: string
  target_url?: string
  updated_at?: string
}

export interface WebDAVSettingsUpdate {
  enabled: boolean
  url: string
  username: string
  password?: string
  backup_password?: string
  cron: string
  clear_password?: boolean
  clear_backup_password?: boolean
}
