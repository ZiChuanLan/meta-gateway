export type Status = 'enabled' | 'disabled'

export interface Site { id: number; name: string; base_url: string; platform: string; status: Status; created_at: string; updated_at: string }
export interface Credential { id: number; site_id: number; kind: string; has_secret: boolean; meta_json?: string; status: Status; checkin_enabled: boolean; created_at?: string }
export interface Channel { id: number; site_id?: number; credential_id?: number; name: string; base_url: string; models_csv: string; group_name: string; priority: number; weight: number; status: Status; type_hint?: string; created_at: string; updated_at: string }
export interface Route { id: number; model_pattern: string; enabled: boolean; mapping_json?: string; notes?: string; created_at: string; updated_at: string }
export interface RouteMember { id: number; route_id: number; channel_id: number; priority: number; weight: number; enabled: boolean; auto: boolean; manual_override: boolean; fail_count: number; cooldown_until?: string; last_error?: string; created_at: string; updated_at: string }
export interface DownstreamKey { id: number; name: string; enabled: boolean; scopes?: string; created_at: string }
export interface CreatedDownstreamKey extends DownstreamKey { token: string }
export interface ProxyLog { id: number; request_id: string; channel_id: number; model: string; status: number; latency_ms: number; attempt: number; error_brief?: string; created_at: string }
export interface DiscoveredModel { id: number; channel_id: number; model_name: string; available: boolean; source: string; latency_ms: number; checked_at: string }
export interface CheckinLog { id: number; site_id: number; credential_id: number; source: string; status: 'success' | 'failed' | 'skipped'; category: string; message: string; reward?: string; latency_ms: number; ran_at: string }
export interface AuditEvent { id: number; request_id?: string; actor_kind: string; actor_id?: number; action: string; resource_kind?: string; resource_id?: number; outcome: string; status_code: number; category?: string; created_at: string }
export interface BackupRecord { id: number; name: string; status: string; size_bytes: number; checksum: string; duration_ms: number; category?: string; created_at: string }

export interface RoutingCandidate { member: RouteMember; channel: Channel; credential_usable: boolean }
export interface RouteEvaluation { candidate: RoutingCandidate; eligible: boolean; reasons: string[] }
export interface RouteExplanation { model: string; route_id: number; evaluated_at: string; selected_priority?: number; candidates: RouteEvaluation[] }

export interface RefreshResult { channel_id: number; adapter: string; models: string[]; created_routes: number; created_members: number; enabled_members: number; disabled_members: number }
export interface RefreshSummary { items: Array<{ channel_id: number; result?: RefreshResult; error?: string }>; success_count: number; failure_count: number }
export interface RunResult { site_id: number; credential_id: number; source: string; status: string; category: string; message: string; reward?: string; latency_ms: number; ran_at: string }
export interface RunSummary { items: RunResult[]; success_count: number; failure_count: number; skipped_count: number }
export interface ImportResult { created_count: number; updated_count: number; adopted_count: number; channel_ids: number[]; discovery: Array<{ channel_id: number; status: string; category?: string }>; discovery_success_count: number; discovery_failure_count: number }
export interface ExchangeEnvelope { format: string; version: number; exported_at: string; importable: boolean; items: Array<Record<string, unknown>> }
