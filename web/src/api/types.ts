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
  auth_mode?: "access_token" | "cookie" | "auto" | string;
  has_secret: boolean;
  has_cookie?: boolean;
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
  model_sync_mode?: "auto" | "manual";
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
  mapping_json?: string;
  group_name?: string;
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
  route_group_name?: string;
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
  error_detail?: string;
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

export interface UnifyVariant {
  channel_id: number;
  channel_name: string;
  model_name: string;
  /** Already served by an existing member — applying skips it. */
  mapped: boolean;
}

/** Normalization rules, applied as a pipeline in this order. */
export type UnifyRule =
  | "account_prefix"
  | "vendor_prefix"
  | "date_suffix"
  | "index_suffix";

export interface UnifyGroup {
  canonical: string;
  variants: UnifyVariant[];
  /** Set when a route with this exact pattern already exists. */
  route_id?: number;
  mapped_count?: number;
  /** Rules this merge actually needed, excluding the always-safe prefix strip. */
  rules?: UnifyRule[];
  /** True when a rule that can conflate different models was needed. */
  risky: boolean;
}

export interface UnifyPreview {
  /** Every merge the enabled rules produce, canonical form already applied. */
  groups: UnifyGroup[];
  /** Originals currently hidden by an applied group. */
  archived: ArchivedRoute[];
}

/** One probe run over a selection of (channel, model) pairs. */
export interface ProbeTask {
  id: number;
  status: "running" | "done" | "cancelled";
  total: number;
  completed: number;
  ok_count: number;
  fail_count: number;
  max_tokens: number;
  concurrency: number;
  /** What was sent upstream; runs with different prompts are not comparable. */
  prompt: string;
  started_at: string;
  finished_at: string | null;
}

/** A single probe attempt: one model, on one channel. */
export interface ModelProbeResult {
  id: number;
  task_id: number;
  channel_id: number;
  model: string;
  ok: boolean;
  status_code: number;
  latency_ms: number;
  error?: string;
  probed_at: string;
}

/** Latest known state of a (channel, model) pair. */
export interface ModelHealth {
  channel_id: number;
  model: string;
  ok: boolean;
  latency_ms: number;
  probed_at: string;
  source: string;
  /** Consecutive probe failures; drives automatic member disabling. */
  consecutive_failures: number;
}

/** Probe scope. Empty arrays mean "everything". */
export interface ProbeStartRequest {
  channel_ids?: number[];
  models?: string[];
  /** User message sent upstream; omitted means the built-in default. */
  prompt?: string;
  max_tokens?: number;
  concurrency?: number;
  /**
   * Consecutive failures after which a member is disabled for this run.
   * Omitted or 0 leaves routing untouched.
   */
  auto_disable_after?: number;
}

export interface UnifyApplyResult {
  routes_created: number;
  members_created: number;
  members_skipped: number;
  routes_archived: number;
  batch_count: number;
}

/** One applied group, kept so it can be reverted later. */
export interface UnifyBatch {
  id: number;
  canonical: string;
  route_id: number;
  routes_created: number;
  members_created: number;
  routes_archived: number;
  created_at: string;
  /** Set once the batch has been reverted. */
  undone_at?: string;
}

/** An original model name hidden by an applied group, restorable on its own. */
export interface ArchivedRoute {
  batch_id: number;
  canonical: string;
  route_id: number;
  model_name: string;
  archived_at: string;
}

export interface UnifyOp {
  id: number;
  batch_id: number;
  seq: number;
  op: "route_created" | "member_created" | "route_archived";
  route_id: number;
  member_id?: number;
  prev_enabled?: boolean;
  undone: boolean;
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
  /** Scheduled model probing. Empty cron = schedule off. */
  probe_cron?: string;
  probe_prompt?: string;
  probe_max_tokens: number;
  probe_concurrency: number;
  /** Consecutive failures before a member is disabled; 0 = report only. */
  probe_auto_disable: number;
  probe_channels: number[];
  probe_models: string[];
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
  /** Gateway may query GitHub for newer releases to power the update badge. */
  update_check_enabled: boolean;
  /** Sync mode newly created channels inherit when the request omits it. */
  default_model_sync_mode: "auto" | "manual";
}

export interface UpdateCheckStatus {
  enabled: boolean;
  current: string;
  latest: string;
  has_update: boolean;
  release_url: string;
  checked_at?: string;
  error?: string;
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
  config_fields?: PluginConfigField[];
  has_config?: boolean;
}

/** One manifest-declared plugin configuration input. */
export interface PluginConfigField {
  key: string;
  type?: "string" | "text" | "number" | "bool" | "select" | "secret" | string;
  label?: string;
  description?: string;
  required?: boolean;
  secret?: boolean;
  default?: unknown;
  options?: string[];
}

/** GET /admin/plugins/{id}/config response. */
export interface PluginConfigResponse {
  id: string;
  config: string;
  fields: PluginConfigField[];
  has_config: boolean;
}

/** One generic external check-in site (non-New-API, cookie-authenticated). */
export interface ExternalCheckin {
  site_id: number;
  credential_id: number;
  name: string;
  base_url: string;
  checkin_path?: string;
  checkin_method?: string;
  headers?: Record<string, string>;
  checkin_enabled: boolean;
  has_cookie: boolean;
}

export type WebDAVSyncMode = "incremental" | "replace";

export interface WebDAVUploadResult {
  status: string;
  target_url?: string;
  bytes?: number;
  encrypted?: boolean;
  category?: string;
  message?: string;
}

export type WebDAVSyncDirection = "download" | "upload";

export interface WebDAVSyncResult {
  status: string;
  direction?: WebDAVSyncDirection;
  source: string;
  fetched_at: string;
  target_url?: string;
  bytes?: number;
  encrypted?: boolean;
  category?: string;
  message?: string;
  latency_ms?: number;
  import?: ImportResult;
  upload?: WebDAVUploadResult;
}

export interface WebDAVStatus {
  configured: boolean;
  download_configured?: boolean;
  upload_configured?: boolean;
  scheduler_armed: boolean;
  download_scheduler_armed?: boolean;
  upload_scheduler_armed?: boolean;
  target_url?: string;
  upload_target_url?: string;
  last?: WebDAVSyncResult;
  last_download?: WebDAVSyncResult;
  last_upload?: WebDAVSyncResult;
  in_progress: boolean;
  source?: string;
  enabled?: boolean;
  upload_enabled?: boolean;
  url?: string;
  username?: string;
  has_password?: boolean;
  has_backup_password?: boolean;
  upload_url?: string;
  upload_username?: string;
  has_upload_password?: boolean;
  has_upload_backup_password?: boolean;
  cron?: string;
  download_cron?: string;
  upload_cron?: string;
}

export interface WebDAVSettings {
  enabled: boolean;
  upload_enabled: boolean;
  url: string;
  username: string;
  has_password: boolean;
  has_backup_password: boolean;
  upload_url: string;
  upload_username: string;
  has_upload_password: boolean;
  has_upload_backup_password: boolean;
  cron: string;
  download_cron: string;
  upload_cron: string;
  download_scheduler_armed: boolean;
  upload_scheduler_armed: boolean;
  download_configured: boolean;
  upload_configured: boolean;
  configured: boolean;
  scheduler_armed: boolean;
  source: string;
  target_url?: string;
  upload_target_url?: string;
  updated_at?: string;
}

export interface WebDAVSettingsUpdate {
  enabled: boolean;
  upload_enabled: boolean;
  url: string;
  username: string;
  password?: string;
  backup_password?: string;
  upload_url: string;
  upload_username: string;
  upload_password?: string;
  upload_backup_password?: string;
  cron?: string;
  download_cron?: string;
  upload_cron?: string;
  clear_password?: boolean;
  clear_backup_password?: boolean;
  clear_upload_password?: boolean;
  clear_upload_backup_password?: boolean;
}
