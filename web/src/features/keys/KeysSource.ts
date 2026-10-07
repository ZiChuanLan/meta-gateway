import type {
  CreatedDownstreamKey,
  KeyCreateInput,
  KeyUpdateInput,
  DiscoveredModel,
  DownstreamKey,
  ModelMetadata,
  RouteOverview,
  UsageSummary,
} from "../../api/types";

/**
 * Where a list page gets its data, and what it is allowed to do with it.
 *
 * The console and the member app show the SAME page: one renderer, two
 * sources. Everything that differs between an operator and a member is
 * expressed here — the endpoints behind the source, and the capabilities that
 * decide which columns and actions exist at all. A capability that is off
 * removes the control, it does not merely disable it: a member must not see a
 * "delete" item they can never use.
 *
 * The mutation fields are optional for that reason: a source that cannot
 * create, rotate or delete simply leaves them out, and the page adapts.
 */
export type KeysSource = {
  keys: (signal?: AbortSignal) => Promise<DownstreamKey[]>;
  discoveredModels: (signal?: AbortSignal) => Promise<Pick<DiscoveredModel, "model_name">[]>;
  usageSummary: (signal?: AbortSignal) => Promise<UsageSummary>;
  routeOverviews: (signal?: AbortSignal) => Promise<RouteOverview[]>;
  routeGroups: (signal?: AbortSignal) => Promise<{ groups: string[] }>;
  /** Tenant groups a token may be bound to (multi-user gateways only). */
  keyGroups?: (signal?: AbortSignal) => Promise<{ groups: string[] }>;
  modelMetadata: (signal?: AbortSignal) => Promise<{ items: ModelMetadata[] }>;
  createKey?: (body: KeyCreateInput) => Promise<Pick<CreatedDownstreamKey, "id" | "token">>;
  updateKey?: (id: number, body: KeyUpdateInput) => Promise<unknown>;
  /** Remove a key. Sources may resolve with a status payload; the page only
   *  needs it to have succeeded. */
  deleteKey?: (id: number) => Promise<unknown>;
  revealKey?: (id: number) => Promise<{ token: string }>;
  rotateKey?: (id: number) => Promise<{ id: number; token: string }>;
};

/** What this viewer may see and do. Everything defaults to off. */
export type KeysCapabilities = {
  /** Create new keys at all. */
  create: boolean;
  /** Rename, re-scope, re-quota an existing key. */
  edit: boolean;
  /** Read a key's plaintext token. */
  reveal: boolean;
  /** Roll a key's token. */
  rotate: boolean;
  /** Remove a key. */
  remove: boolean;
  /** Which scopes a key may carry (an operator concern). */
  scopes: boolean;
  /** Per-key quota and spend budget. */
  quotas: boolean;
  /** Model allow/deny lists and route groups. */
  modelScope: boolean;
  /** Client IP restrictions and expiry. */
  advanced: boolean;
  /** Money: prices, cost columns, the billing summary. */
  pricing: boolean;
  /** Upstream detail (routes, aliases, channels behind a key). */
  upstream: boolean;
  /**
   * Team concepts: the tenant group a token is bound to, and the member who
   * owns it. These are the multi-user module's business — an operator running a
   * gateway for themselves should never see them, so the page gates them on the
   * operating mode as well.
   */
  team: boolean;
  /**
   * Lay the tokens out as cards rather than as the operator's table.
   *
   * A site's token table is scanned by column — many rows, few questions. A
   * person's own tokens are a handful, and each one is read on its own: which
   * token is this (the masked tail), what has it cost me, when did I last use
   * it. That is a card, and the same rows feed both layouts.
   */
  cards: boolean;
};

export const MEMBER_KEY_CAPS: KeysCapabilities = {
  create: true,
  edit: true,
  reveal: true,
  rotate: true,
  remove: true,
  scopes: false,
  quotas: false,
  modelScope: true,
  advanced: true,
  pricing: true,
  upstream: false,
  team: false,
  cards: true,
};

export const ADMIN_KEY_CAPS: KeysCapabilities = {
  create: true,
  edit: true,
  reveal: true,
  rotate: true,
  remove: true,
  scopes: true,
  quotas: true,
  modelScope: true,
  advanced: true,
  pricing: true,
  upstream: true,
  team: true,
  cards: false,
};
