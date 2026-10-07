import type { KeysSource } from "../features/keys/KeysSource";
import type { DownstreamKey, UsageSummary } from "../api/types";
import { accountRequest } from "../team/transport";
import type { UserKey } from "../team/types";

/**
 * The member app's keys source: the console's renderer bound to `/me`.
 *
 * The shapes differ because the two sides really do expose different things —
 * a member has no `scopes` (they always get relay), sees their own usage
 * instead of an operator's quota columns, and never learns which upstream
 * channel answered. The mapping below is exactly that difference, in one
 * place, instead of a second page that has to be kept in step.
 */
function toDownstreamKey(key: UserKey): DownstreamKey {
  return {
    id: key.id,
    name: key.name,
    enabled: key.enabled,
    // Members do not choose scopes; every key they make is a relay key.
    scopes: "relay",
    // Usage, not quota: the operator-only limits are absent on purpose, and
    // the capability flags keep those columns out of the table regardless.
    quota_used_tokens: key.used_tokens ?? 0,
    quota_total_tokens: 0,
    model_allowlist: key.models ?? "",
    model_denylist: "",
    expires_at: key.expires_at ?? "",
    allowed_ips: key.allowed_ips ?? "",
    group_name: "",
    // Whether a token can still be revealed: an account's own key always can.
    has_token: true,
    // The masked tail, so a card can say which token it is without a reveal
    // round-trip, and the last time the gateway saw it.
    token_hint: key.hint ?? "",
    last_used_at: key.last_used_at ?? "",
    cost: key.cost ?? 0,
    created_at: key.created_at,
  };
}

type MemberKey = UserKey & {
  models?: string;
  used_tokens?: number;
  cost?: number;
  last_used_at?: string;
  plan_id?: number;
};

export const memberKeysSource: KeysSource = {
  keys: async (signal) => {
    const rows = await accountRequest<MemberKey[]>("/me/keys", { signal });
    return rows.map(toDownstreamKey);
  },
  // The member catalogue is the authorized model list, which is what the
  // allow-list picker needs; there is no discovery snapshot on this side.
  discoveredModels: async (signal) => {
    const models = await accountRequest<string[]>("/me/models", { signal });
    return models.map((model_name) => ({ model_name }));
  },
  usageSummary: (signal) => accountRequest<UsageSummary>("/me/usage/summary", { signal }),
  // Members have no visibility into routes, groups or the catalogue behind a
  // key: the capability flags keep these out of the UI, and these stubs keep
  // the shared renderer from having to special-case their absence.
  routeOverviews: async () => [],
  routeGroups: async () => ({ groups: [] }),
  // A member is not told which tenant group their token belongs to; the
  // capability flag keeps the field out of the UI regardless.
  keyGroups: async () => ({ groups: [] }),
  modelMetadata: async () => ({ items: [] }),

  createKey: async (body) => {
    const created = await accountRequest<{ id: number; token: string }>("/me/keys", {
      method: "POST",
      body: JSON.stringify(memberKeyBody(body)),
    });
    return created;
  },
  updateKey: async (id, body) =>
    accountRequest<{ ok: boolean }>(`/me/keys/${id}`, {
      method: "PATCH",
      body: JSON.stringify(memberKeyBody(body)),
    }),
  deleteKey: (id) => accountRequest(`/me/keys/${id}`, { method: "DELETE" }),
  revealKey: (id) =>
    accountRequest<{ token: string }>(`/me/keys/${id}/reveal`, {
      method: "POST",
    }),
  rotateKey: (id) =>
    accountRequest<{ id: number; token: string }>(`/me/keys/${id}/rotate`, {
      method: "POST",
    }),
};

/**
 * The console's key editor sends operator fields the member endpoint does not
 * accept (scopes, route groups, quotas). The server decodes strictly, so the
 * body is trimmed to what `/me/keys` documents rather than forwarded as-is.
 */
function memberKeyBody(body: Record<string, unknown>) {
  const out: Record<string, unknown> = {};
  if (body.name !== undefined) out.name = body.name;
  if (body.enabled !== undefined) out.enabled = body.enabled;
  if (body.model_allowlist !== undefined) out.models = body.model_allowlist;
  if (body.expires_at !== undefined) out.expires_at = body.expires_at;
  if (body.allowed_ips !== undefined) out.allowed_ips = body.allowed_ips;
  return out;
}
