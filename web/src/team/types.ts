export interface Branding {
  name: string;
  logo_url: string;
  accent: string;
  notice: string;
  login_description: string;
  api_base_url: string;
  show_usage: boolean;
  show_routing: boolean;
}
export interface TeamSettings {
  mode: "personal" | "team";
  branding: Branding;
}

/**
 * A tenant group: the quota/rate-limit container a client token can be bound
 * to. It carries the same two budgets as an account (tokens and money), which
 * is why the quotas board shows them side by side.
 */
export interface KeyGroup {
  name: string;
  quota_total_tokens: number;
  quota_used_tokens: number;
  quota_total_cost: number;
  quota_used_cost: number;
  rate_per_minute: number;
  rate_burst: number;
  created_at?: string;
  updated_at?: string;
}

/** Per-model billing markup; 1 means no markup, and the value multiplies the
 *  cost every model's usage produces. */
export interface ModelRatio {
  model: string;
  ratio: number;
  updated_at?: string;
}
export interface ModeInfo {
  mode: "personal" | "team";
  has_owner: boolean;
  role: string;
}
export interface TeamUser {
  id: number;
  username: string;
  name: string;
  role: "owner" | "admin" | "member";
  status: "active" | "paused";
  policy_id: number;
  created_at: string;
  key_count: number;
  /** The account's own credit pool; 0 means unlimited. */
  quota_total_tokens: number;
  quota_used_tokens: number;
  /** The same pool in money, in the ledger's unit. Both are enforced. */
  quota_total_cost: number;
  quota_used_cost: number;
}
export interface Policy {
  id: number;
  name: string;
  models: string[];
  member_ids: number[];
  all_models: boolean;
  max_keys: number;
  rpm: number;
  allow_routing: boolean;
  allow_request_preferences: boolean;
}
export interface Candidate {
  id: number;
  model: string;
  name: string;
  enabled: boolean;
}
export interface RouteEntry {
  id: number;
  weight: number;
  disabled?: boolean;
}
export interface Plan {
  id: number;
  name: string;
  /** True for the implicit plan that keys without a binding ride. */
  default: boolean;
  /** Model name → its ordered upstream list, as the member arranged it. */
  routes: Record<string, RouteEntry[]>;
}
/** One upstream row of the member's arrangement editor. */
export interface RouteUpstream {
  id: number;
  channel_id: number;
  channel: string;
  origin: string;
  group: string;
  site_priority: number;
  site_weight: number;
  site_enabled: boolean;
  weight: number;
  disabled: boolean;
}
export interface RouteOrder {
  model: string;
  plan_id: number;
  customized: boolean;
  upstreams: RouteUpstream[];
}
export interface RouteList {
  plan_id: number;
  models: string[];
}
export interface UserKey {
  id: number;
  name: string;
  enabled: boolean;
  hint: string;
  models: string;
  expires_at: string;
  allowed_ips: string;
  plan_id: number;
  created_at: string;
  last_used_at?: string;
  used_tokens?: number;
  cost?: number;
}
/**
 * One row of the code board. `invite` signs someone up (possibly many times),
 * `credit` tops up an existing account. The plaintext code is never listed:
 * the table only stores its hash.
 */
export interface TeamCode {
  id: number;
  kind: "invite" | "credit";
  label: string;
  note: string;
  policy_id: number;
  role: "member" | "admin";
  expires_at: number;
  max_uses: number;
  used_count: number;
  quota_tokens: number;
  /** Money face value, in the ledger's unit. A voucher may carry either. */
  quota_cost: number;
  revoked: boolean;
  created_at: string;
  prefix: string;
}
/** A freshly minted code: the only moment the plaintext exists. */
export interface MintedCode {
  id: number;
  code: string;
  path: string;
  label: string;
}
/** The account's own credit pool, as the member sees it. */
export interface CreditView {
  total: number;
  used: number;
  available: number;
  unlimited: boolean;
  /**
   * The same pool in money, in the ledger's unit. Both budgets are enforced,
   * so a member may run out of either tokens or spend.
   */
  cost_total: number;
  cost_used: number;
  cost_available: number;
  cost_unlimited: boolean;
}

/** The site's money presentation, as both apps receive it. */
export interface CurrencyView {
  symbol: string;
  rate: number;
}
export interface ImportedMember {
  id: number;
  username: string;
  name: string;
  password: string;
}
export interface ImportFailure {
  line: number;
  input: string;
  reason: string;
}
export interface ImportOutcome {
  created: ImportedMember[];
  failed: ImportFailure[];
}
export type BulkMemberAction = "pause" | "resume" | "policy" | "revoke_sessions" | "delete";
export interface Account {
  user: TeamUser;
  policy: Policy;
  csrf: string;
  branding: Branding;
  credit: CreditView;
  /** Symbol + rate every amount is rendered through. */
  currency?: CurrencyView;
}
/** The provider card in the console's sign-in settings. */
export interface OAuthProviderView {
  id: string;
  label: string;
  enabled: boolean;
  client_id: string;
  /** The secret itself never leaves the server; this says whether one is stored. */
  has_secret: boolean;
  authorize_url: string;
  token_url: string;
  userinfo_url: string;
  scopes: string;
  /** Register this exact URL at the provider. */
  callback_url: string;
  default_authorize_url: string;
  default_token_url: string;
  default_userinfo_url: string;
  default_scopes: string;
}
export interface OAuthSettings {
  auto_register: boolean;
  default_policy_id: number;
  providers: OAuthProviderView[];
}
/** One linked third-party account of a member. */
export interface OAuthBinding {
  id: number;
  provider: string;
  label: string;
  email: string;
  name: string;
  created_at: string;
  last_login_at: string;
}
export interface RequestRow {
  request_id: string;
  key_id: number;
  path: string;
  status: number;
  latency_ms: number;
  created_at: string;
  model: string;
  tokens: number | null;
  cost: number | null;
  prompt_tokens?: number | null;
  completion_tokens?: number | null;
  cache_read_tokens?: number | null;
  cache_creation_tokens?: number | null;
  attempts?: number;
  key_name?: string;
  /** Client family guessed from the relay User-Agent, as shown in the admin log list. */
  client_family?: string;
}
export interface UserModel {
  supports_thinking?: number;
  name: string;
  vendor: string;
  kind: string;
  context_window: number;
  input_modalities: string;
  output_modalities: string;
  endpoints: string;
  candidates: number;
}
/**
 * One account's own figures for one model (`/me/model-stats`).
 *
 * `requests` counts this account's completed client requests; tokens and cost
 * come from the billing records. A request that never reached an upstream
 * resolves to no model and is reported as the view's `unassigned` count instead
 * of being attributed to one.
 */
export interface MemberModelStat {
  model: string;
  requests: number;
  ok: number;
  failed: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cache_read_tokens: number;
  cost: number;
  avg_latency_ms: number;
  p50_ms: number;
  p95_ms: number;
  last_at: string;
}
export interface MemberModelStats {
  since: string;
  until: string;
  unassigned: number;
  models: MemberModelStat[];
}
/**
 * What the gateway's own probes say about the upstreams behind one model. It
 * names neither the upstream nor its URL: a member learns whether the model is
 * serviceable, not where the gateway buys it.
 */
export interface MemberModelAvailability {
  model: string;
  upstreams: number;
  probed: number;
  healthy: number;
  samples: number;
  ok_samples: number;
  availability: number;
  avg_latency_ms: number;
  last_probed_at: string;
}
export interface RequestPreferences {
  failover: "inherit" | "on" | "off";
  max_retries: number | null;
}
export interface PreferencesView {
  preferences: RequestPreferences;
  can_edit: boolean;
  site: { failover_enabled: boolean; retry_times: number };
  effective: { failover_enabled: boolean; retry_times: number };
}
export type TeamRequest = <T>(path: string, init?: RequestInit) => Promise<T>;
