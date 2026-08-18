import type { FinanceItem, RouteMember, RoutingCandidate } from "../../api/types"

export function primaryMember(members: RoutingCandidate[]) {
  if (!members.length) return null;
  return sortMembers(members)[0] ?? null;
}

export function sortMembers(members: RoutingCandidate[]) {
  return [...members].sort((left, right) => {
    if (right.member.priority !== left.member.priority) {
      return right.member.priority - left.member.priority;
    }
    if (right.member.weight !== left.member.weight) {
      return right.member.weight - left.member.weight;
    }
    return left.channel.name.localeCompare(right.channel.name);
  });
}

export function isActiveCooldown(member: Pick<RouteMember, "cooldown_until">) {
  if (!member.cooldown_until) return false;
  const until = new Date(member.cooldown_until).getTime();
  return Number.isFinite(until) && until > Date.now();
}

export function candidateState(candidate: RoutingCandidate) {
  const member = candidate.member;
  // Channel-level guard: an auto-disabled channel is parked by the
  // consecutive-failure circuit. Surface it as the dominant state on every
  // member row so the model page (the routing view) shows it clearly.
  if (candidate.channel.status === "auto_disabled") return "auto_disabled";
  if (!member.enabled) return "disabled";
  if (!candidate.credential_usable) return "no_credential";
  // Historical failures do not keep a member degraded after its penalty ends.
  if (isActiveCooldown(member)) return "cooling_down";
  return "ready";
}

export function formatCooldownLeft(iso: string, now = Date.now()) {
  const until = new Date(iso).getTime();
  if (!Number.isFinite(until)) return "?";
  const seconds = Math.max(0, Math.ceil((until - now) / 1000));
  if (seconds >= 60) {
    const mins = Math.floor(seconds / 60);
    return `${mins}m${seconds % 60 > 0 ? ` ${seconds % 60}s` : ""}`;
  }
  return `${seconds}s`;
}

export /**
 * memberFinance resolves a member's call price and affordable call count on a
 * model from the finance overview. The upstream price table is quoted in quota
 * per 1M tokens; dividing by quota_per_unit yields the site-currency price.
 * Returns null when the channel has no finance data or the model is not priced.
 */
function memberFinance(
  member: RouteMember,
  model: string,
  items: FinanceItem[],
): { priceUsd: string; calls: string; fixed: boolean; overdrawn: boolean } | null {
  if (!member || !model || !items.length) return null;
  const item = items.find((entry) => entry.channel_id === member.channel_id);
  if (!item || item.quota_per_unit <= 0) return null;
  const price = item.prices?.[model];
  if (!price || !price.price_usd || price.price_usd <= 0) return null;
  const quotaPerUnit = item.quota_per_unit;
  const priceUsd = price.price_usd;
  const balanceUsd = item.balance / quotaPerUnit;
  const fixed = price.mode === "fixed";
  // fixed: price per request → affordable request count.
  // token: price per 1M tokens → affordable 1M-token units (shown as M).
  // A negative balance (overdrawn upstream) affords nothing; show 0 instead
  // of a misleading negative count and let the caller render the overdrawn state.
  const rawCalls =
    balanceUsd <= 0 ? 0 : Math.floor(balanceUsd / priceUsd);
  const formatUsd = (value: number) => {
    if (value >= 1) return value.toFixed(2);
    if (value >= 0.01) return value.toFixed(4);
    return value.toFixed(6);
  };
  const formatCount = (value: number) =>
    value >= 1000 ? `${Math.round(value / 1000)}k` : String(value);
  return {
    priceUsd: formatUsd(priceUsd),
    // Pure count — the render layer appends the unit (" 次" for fixed,
    // "M" for per-1M-token) exactly once.
    calls: formatCount(rawCalls),
    fixed,
    overdrawn: balanceUsd < 0,
  };
}

export function getEffectiveRoutingPolicy(
  mode: string,
  runtime?: {
    routing_latency_aware: boolean;
    routing_error_aware: boolean;
  },
) {
  if (!runtime) return null;
  switch (mode) {
    case "adaptive":
      return {
        latency: true,
        error: true,
        source: "routing.policySource.model",
      };
    case "latency":
      return {
        latency: true,
        error: runtime.routing_error_aware,
        source: "routing.policySource.mixed",
      };
    case "weighted":
      return {
        latency: false,
        error: false,
        source: "routing.policySource.model",
      };
    default:
      return {
        latency: runtime.routing_latency_aware,
        error: runtime.routing_error_aware,
        source: "routing.policySource.global",
      };
  }
}
