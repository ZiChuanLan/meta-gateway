type Status = "enabled" | "disabled" | "auto_disabled";

type ChannelHealthState =
  "disabled" | "unhealthy" | "degraded" | "healthy" | "unknown";
type ChannelConnectivityState = "unknown" | "reachable" | "unreachable";
type ChannelAccountState =
  "unknown" | "ok" | "invalid" | "banned" | "rate_limited" | "failed";

export interface Site {
  id: number;
  name: string;
  base_url: string;
  platform: string;
  status: Status;
  created_at: string;
  updated_at: string;
}
export interface Credential {
  id: number;
  site_id: number;
  kind: string;
  has_secret: boolean;
  meta_json?: string;
  status: Status;
  checkin_enabled: boolean;
  models_csv?: string;
  created_at?: string;
}
export interface Channel {
  id: number;
  site_id?: number;
  credential_id?: number;
  name: string;
  base_url: string;
  models_csv: string;
  group_name: string;
  priority: number;
  weight: number;
  status: Status;
  type_hint?: string;
  max_reasoning_effort?: string;
  payload_rules?: string;
  max_concurrent?: number;
  proxy_url?: string;
  header_override?: string;
  system_prompt?: string;
  retry_config?: string;
  stable_first?: boolean;
  stable_first_requests?: number;
  created_at: string;
  updated_at: string;
}
export interface ConnectionCreateResponse {
  channel: Channel;
  site?: Site;
  credential_id: number;
  reused_site?: boolean;
  has_secret?: boolean;
  platform?: string;
  detection_matched?: boolean;
}
export interface ChannelOverview {
  channel: Channel;
  credential_kind?: string;
  checkin_enabled: boolean;
  checkin_supported: boolean;
  account_supported: boolean;
  has_user_credential: boolean;
  has_platform_user_id: boolean;
  has_api_key: boolean;
  site_usable: boolean;
  credential_usable: boolean;
  model_count: number;
  last_checked_at?: string;
  last_latency_ms: number;
  discovery_source?: string;
  route_count: number;
  enabled_member_count: number;
  cooling_member_count: number;
  failure_count: number;
  last_error?: string;
  last_probe_at?: string;
  last_probe_ok?: boolean;
  last_probe_error?: string;
  last_ping_at?: string;
  last_ping_ok?: boolean;
  last_ping_error?: string;
  last_ping_ms?: number;
  last_account_probe_at?: string;
  last_account_probe_ok?: boolean;
  last_account_probe_error?: string;
  health_state?: ChannelHealthState;
  health_reason?: string;
  connectivity_state?: ChannelConnectivityState;
  account_state?: ChannelAccountState;
}
export interface Route {
  id: number;
  model_pattern: string;
  enabled: boolean;
  routing_mode: string;
  mapping_json?: string;
  notes?: string;
  single_member_id?: number | null;
  retry_times?: number | null;
  channel_retry_times?: number | null;
  max_reasoning_effort?: string | null;
  max_concurrent?: number | null;
  proxy_url?: string | null;
  header_override?: string | null;
  system_prompt?: string | null;
  retry_config?: string | null;
  payload_rules?: string | null;
  stable_first?: boolean | null;
  stable_first_denominator?: number | null;
  stable_first_promote_requests?: number | null;
  stable_first_requests?: number;
  model_group?: string;
  created_at: string;
  updated_at: string;
}
export interface RouteMember {
  id: number;
  route_id: number;
  channel_id: number;
  priority: number;
  weight: number;
  enabled: boolean;
  auto: boolean;
  manual_override: boolean;
  fail_count: number;
  cooldown_until?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}
export interface DownstreamKey {
  id: number;
  name: string;
  enabled: boolean;
  scopes?: string;
  quota_total_tokens?: number;
  quota_used_tokens?: number;
  price_prompt_per_1k?: number;
  price_completion_per_1k?: number;
  price_cache_per_1k?: number;
  model_allowlist?: string;
  model_denylist?: string;
  expires_at?: string;
  allowed_ips?: string;
  group_name?: string;
  estimated_cost?: number;
  has_token?: boolean;
  created_at: string;
}
export interface CreatedDownstreamKey extends DownstreamKey {
  token: string;
}
export interface UsageSummary {
  request_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  /** Persisted billing amount for the selected window (or all time). */
  cost: number;
  /** Legacy field returned by older gateways. */
  estimated_cost?: number;
}
export interface UsageRecord {
  id: number;
  request_id: string;
  downstream_key_id: number;
  channel_id: number;
  model: string;
  path: string;
  stream: boolean;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  status: number;
  /** Persisted billing amount; older gateways may omit it. */
  cost?: number;
  created_at: string;
}
export interface ProxyLog {
  id: number;
  request_id: string;
  channel_id: number;
  route_id?: number;
  route_pattern?: string;
  model: string;
  status: number;
  latency_ms: number;
  attempt: number;
  error_brief?: string;
  downstream_key_id?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  first_byte_ms?: number;
  client_family?: string;
  reasoning_effort?: string;
  tokens_per_second?: number;
  stream?: boolean;
  path?: string;
  session_key?: string;
  upstream_request_id?: string;
  created_at: string;
}
export interface DiscoveredModel {
  id: number;
  channel_id: number;
  model_name: string;
  available: boolean;
  source: string;
  latency_ms: number;
  checked_at: string;
}

export interface ModelMetadata {
  id?: number;
  model_name: string;
  context_window: number;
  input_modalities: string;
  output_modalities: string;
  supports_thinking: number; // -1 unknown, 0 no, 1 yes
  vendor: string;
  notes: string;
  updated_at?: string;
}

export interface ErrorPassRule {
  id?: number;
  name: string;
  status_code: number; // 0 = any 4xx
  keyword: string;
  model_glob: string;
  channel_id: number; // 0 = all
  action: "passthrough" | "rewrite" | "ignore_monitor";
  rewrite_to: number;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface AlertRule {
  id?: number;
  name: string;
  metric: string;
  operator: "gt" | "gte" | "lt" | "lte" | "eq" | "neq";
  threshold: number;
  window_seconds: number;
  sustained_seconds: number;
  cooldown_seconds: number;
  level: string;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface SearchHits {
  channels: { id: number; name: string; url: string }[];
  routes: { id: number; model: string; status: string }[];
  credentials: { id: number; name: string; kind: string; site_id: number }[];
  logs: {
    id: number;
    request_id: string;
    model: string;
    channel_id: number;
    status: number;
    created_at: string;
    upstream_request_id: string;
    key_fingerprint: string;
  }[];
}

export interface DBGCResult {
  route_members: number;
  proxy_logs: number;
  discovered_models: number;
  checkin_logs: number;
  usage_records: number;
  balance_history: number;
  decision_snapshots: number;
  channel_health_history: number;
  channel_model_blocks: number;
  redemption_codes: number;
  error_passthrough_rules: number;
  freelist_pages: number;
  page_size: number;
  vacuum_freed_bytes: number;
  vacuumed: boolean;
}

export interface PromptGuardRule {
  id?: number;
  name: string;
  pattern: string;
  action: "mask" | "reject" | "exclude";
  replacement?: string;
  exclude_channels?: string;
  channel_scope: number;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface HealthPoint {
  id: number;
  channel_id: number;
  ok: boolean;
  latency_ms: number;
  verdict: string;
  probed_at: string;
}

export interface HealthSummaryItem {
  channel_id: number;
  total: number;
  ok: number;
  availability: number;
}
export interface CheckinLog {
  id: number;
  site_id: number;
  credential_id: number;
  source: string;
  status: "success" | "failed" | "skipped";
  category: string;
  message: string;
  reward?: string;
  latency_ms: number;
  ran_at: string;
}
export interface AuditEvent {
  id: number;
  request_id?: string;
  actor_kind: string;
  actor_id?: number;
  action: string;
  resource_kind?: string;
  resource_id?: number;
  outcome: string;
  status_code: number;
  category?: string;
  created_at: string;
}
export interface BackupRecord {
  id: number;
  name: string;
  status: string;
  size_bytes: number;
  checksum: string;
  duration_ms: number;
  category?: string;
  created_at: string;
}

/** Effective runtime parameters (editable subset + env bootstrap). Secrets never included. */
export interface RuntimeEditableSettings {
  retry_times: number;
  cross_channel_failover_enabled: boolean;
  cooldown_seconds: number;
  fault_protection_enabled: boolean;
  checkin_enabled: boolean;
  checkin_cron: string;
  relay_rate_per_minute: number;
  relay_rate_burst: number;
  admin_rate_per_minute: number;
  admin_rate_burst: number;
  audit_retention_days: number;
  audit_retention_rows: number;
  channel_auto_disable_threshold: number;
  routing_latency_aware: boolean;
  routing_error_aware: boolean;
  recovery_probe_enabled: boolean;
  recovery_probe_interval_seconds: number;
  stable_first_enabled: boolean;
  stable_first_denominator: number;
  stable_first_promote_requests: number;
  routing_concurrency_enabled: boolean;
  routing_concurrency_limit: number;
  webhook_url?: string;
  proxy_url?: string;
  max_concurrent?: number;
  discovery_cron?: string;
  db_gc_cron?: string;
  webhook_throttle_seconds: number;
  sticky_enabled: boolean;
  sticky_ttl_minutes: number;
  alert_config_json?: string;
  alert_sweep_interval_seconds: number;
  alert_daily_summary_interval_seconds: number;
  health_sweep_enabled: boolean;
  health_sweep_interval_seconds: number;
  health_sweep_jitter_seconds: number;
  health_sweep_degraded_ms: number;
  health_sweep_concurrency: number;
  health_sweep_timeout_seconds: number;
  channel_retry_times: number;
  key_pool_rotation: boolean;
}

export interface RuntimeSettings {
  source: "environment" | "admin_override" | string;
  has_override: boolean;
  note: string;
  editable: RuntimeEditableSettings;
  env_bootstrap: RuntimeEditableSettings;
  updated_at?: string;
  server_http_addr: string;
  data_dir: string;
  backup_dir: string;
  plugins_dir: string;
  metrics_token_masked: string;
}

export interface RoutingCandidate {
  member: RouteMember;
  channel: Channel;
  credential_usable: boolean;
}
export interface RouteOverview {
  route: Route;
  members: RoutingCandidate[];
}
interface RouteEvaluation {
  candidate: RoutingCandidate;
  eligible: boolean;
  reasons: string[];
  score?: number;
}
export interface RouteExplanation {
  model: string;
  route_id: number;
  routing_mode?: string;
  evaluated_at: string;
  selected_priority?: number;
  candidates: RouteEvaluation[];
  session_key?: string;
  sticky_channel_id?: number;
  sticky_hit?: boolean;
  sticky_reason?: string;
  retry_times_override?: number;
  channel_retry_times_override?: number;
}

interface StickyStats {
  bound_sessions: number;
  hits: number;
  binds: number;
  escapes: number;
}
interface StickyEntry {
  key: string;
  channel_id: number;
  expires_at: string;
}
export interface StickySnapshot {
  enabled: boolean;
  stats: StickyStats;
  entries: StickyEntry[];
  ttl_seconds: number;
}

export interface ProbeResult {
  channel_id: number;
  adapter: string;
  models: string[];
  latency_ms: number;
  checked_at: string;
}
export interface AccountProbeResult {
  channel_id: number;
  credential_id: number;
  username: string;
  display_name?: string;
  platform_user_id?: number;
  quota?: number;
  used_quota?: number;
  latency_ms: number;
  checked_at: string;
}
interface ModelPrice {
  model: string;
  currency?: string;
  price_usd?: number;
  mode?: "fixed" | "token" | "legacy";
  ratio?: number;
  quota_per_1m?: number;
}
export interface FinanceItem {
  channel_id: number;
  balance: number;
  quota_total?: number;
  quota_used?: number;
  quota_per_unit: number;
  prices: Record<string, ModelPrice>;
}
export interface SyncKeysResult {
  channel_id: number;
  listed: number;
  created_credentials: number;
  reused_credentials: number;
  skipped_masked: number;
  /** Local api_key credentials removed because their upstream token no longer exists. */
  deleted_credentials?: number;
  empty_list?: boolean;
  category?: string;
  message?: string;
  attached_credential_id?: number;
  created_channels?: number;
  updated_channels?: number;
  group_channels?: Array<{
    group: string;
    channel_id: number;
    credential_id: number;
    name: string;
    status: string;
  }>;
  items: Array<{
    name?: string;
    group?: string;
    credential_id?: number;
    enabled?: boolean;
    status: string;
    category?: string;
  }>;
}
export interface CreateUpstreamKeyResult {
  credential_id: number;
  name: string;
  group: string;
  category: string;
  message: string;
}
export interface RefreshResult extends ProbeResult {
  created_routes: number;
  created_members: number;
  enabled_members: number;
  deleted_members: number;
  deleted_routes: number;
}
export interface ChannelPingResult {
  channel_id: number;
  reachable: boolean;
  latency_ms?: number;
  status_code?: number;
  error?: string;
  checked_at?: string;
}
export interface RefreshSummary {
  items: Array<{ channel_id: number; result?: RefreshResult; error?: string }>;
  success_count: number;
  failure_count: number;
}
export interface RunResult {
  site_id: number;
  credential_id: number;
  source: string;
  status: string;
  category: string;
  message: string;
  reward?: string;
  latency_ms: number;
  ran_at: string;
}
export interface RunSummary {
  items: RunResult[];
  success_count: number;
  failure_count: number;
  skipped_count: number;
}
export interface ImportResult {
  created_count: number;
  updated_count: number;
  adopted_count: number;
  channel_ids: number[];
  discovery: Array<{ channel_id: number; status: string; category?: string }>;
  discovery_success_count: number;
  discovery_failure_count: number;
  checkin_capable_count?: number;
  missing_api_key_count?: number;
  relay_ready_count?: number;
  key_sync_success_count?: number;
  key_sync_failure_count?: number;
  key_sync_skipped_count?: number;
  key_sync?: Array<{
    channel_id: number;
    status: string;
    category?: string;
    created?: number;
    reused?: number;
    masked?: number;
  }>;
  items?: Array<{
    channel_id: number;
    credential_kind?: string;
    checkin_capable: boolean;
    has_api_key: boolean;
    discovery_status?: string;
    discovery_category?: string;
  }>;
}
export interface ExchangeEnvelope {
  format: string;
  version: number;
  exported_at: string;
  importable: boolean;
  items: Array<Record<string, unknown>>;
  skipped?: Array<{ channel_id: number; name: string; reason: string }>;
}

export interface PluginRecord {
  id: string;
  version: string;
  status: string;
  enabled: boolean;
  source?: string;
  checksum?: string;
  installed_at?: string;
  enabled_at?: string;
  meta_json?: string;
}

/** Combined store view: core orientation cards + add-ons + orphans. */
export interface ModuleStatus {
  id: string;
  name: string;
  version: string;
  description?: string;
  kind: "core" | "addon" | string;
  unlocks?: string[];
  capabilities?: string[];
  source?: string;
  installed: boolean;
  enabled: boolean;
  can_toggle: boolean;
  open_path?: string;
}

export type WebDAVSyncMode = "incremental" | "replace";

export interface WebDAVSyncResult {
  status: string;
  source: string;
  fetched_at: string;
  target_url?: string;
  bytes?: number;
  encrypted?: boolean;
  category?: string;
  message?: string;
  latency_ms?: number;
  import?: ImportResult;
}

export interface WebDAVStatus {
  configured: boolean;
  scheduler_armed: boolean;
  target_url?: string;
  last?: WebDAVSyncResult;
  in_progress: boolean;
  source?: string;
  enabled?: boolean;
  url?: string;
  username?: string;
  has_password?: boolean;
  has_backup_password?: boolean;
  cron?: string;
}

export interface WebDAVSettings {
  enabled: boolean;
  url: string;
  username: string;
  has_password: boolean;
  has_backup_password: boolean;
  cron: string;
  configured: boolean;
  scheduler_armed: boolean;
  source: string;
  target_url?: string;
  updated_at?: string;
}

export interface WebDAVSettingsUpdate {
  enabled: boolean;
  url: string;
  username: string;
  password?: string;
  backup_password?: string;
  cron: string;
  clear_password?: boolean;
  clear_backup_password?: boolean;
}
