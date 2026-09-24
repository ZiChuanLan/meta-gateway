import type {
  FinanceItem,
  Route,
  RouteMember,
  RouteOverview,
  RoutingCandidate,
} from "../../api/types";

export function primaryMember(members: RoutingCandidate[], route?: Pick<Route, "routing_mode" | "single_member_id">) {
  if (!members.length) return null;
  if (route?.routing_mode === "single" && route.single_member_id != null) {
    const pinned = members.find((entry) => entry.member.id === route.single_member_id);
    if (pinned) return pinned;
  }
  return sortMembers(members)[0] ?? null;
}

/**
 * primaryChannelName names the connection routing would try first for a route,
 * so a model picker can answer "which site serves this?" without sending the
 * operator to the Models page. Empty when the route has no usable member.
 */
export function primaryChannelName(overview?: RouteOverview) {
  const head = primaryMember(overview?.members ?? [], overview?.route);
  return head?.channel.name ?? "";
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

// originModelOf resolves the upstream real model a member rewrites to when it
// (or its route, the legacy alias form) carries a {"real":"…"} mapping. Empty
// means the member forwards the route name unchanged.
export function originModelOf(member: RouteMember, route?: Pick<Route, "mapping_json">): string {
  const raw = member.mapping_json || route?.mapping_json || "";
  if (!raw) return "";
  try {
    const parsed = JSON.parse(raw) as { real?: string };
    return parsed.real ?? "";
  } catch {
    return "";
  }
}

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/**
 * One row of a 试调 "上游连接" picker. It identifies a route MEMBER, not a
 * channel: a unified alias holds one member per upstream 原模型 name, and
 * several of those can sit on the SAME channel — so a channel is not a
 * selectable upstream, a member is.
 */
export type UpstreamChoice = {
  memberId: number;
  channelId: number;
  name: string;
  /** The upstream name this member rewrites to; "" = forwarded unchanged. */
  origin: string;
  /** Member group ("default" when unset); narrows which keys can reach it. */
  group: string;
  priority: number;
  weight: number;
  label: string;
};

/**
 * upstreamChoices builds the 上游连接 picker rows for one route.
 *
 * The label leads with the channel and then names the 原模型, because those are
 * the two facts that tell two rows apart: a channel name alone repeats once the
 * alias form has put several upstream names behind it, which made every row of
 * the list look — and behave — identical.
 */
export function upstreamChoices(
  members: RoutingCandidate[],
  route: Pick<Route, "mapping_json"> | undefined,
  t: Translate,
): UpstreamChoice[] {
  const choices = sortMembers(members).map((candidate) => {
    const origin = originModelOf(candidate.member, route);
    const name = candidate.channel.name;
    const head = origin
      ? `${name} · ${t("routing.memberOrigin", { model: origin })}`
      : name;
    return {
      memberId: candidate.member.id,
      channelId: candidate.channel.id,
      name,
      origin,
      group: (candidate.member.group_name || "").trim() || "default",
      priority: candidate.member.priority,
      weight: candidate.member.weight,
      label: `${head} · p${candidate.member.priority}/w${candidate.member.weight}`,
    };
  });
  // Rows that print identically cannot be told apart, let alone chosen, so grow
  // the label until they can — using the smallest fact that does the job: the
  // channel id (the console keys the channel-models page by it) and then the
  // group, which is what separates two members that only differ that way. A
  // collision surviving both is one upstream by every displayed fact, and the
  // picker has nothing left to say about it.
  return separate(separate(choices, (choice) => `#${choice.channelId}`), (choice) => choice.group);
}

/** separate appends a suffix to every choice whose label is still ambiguous. */
function separate(
  choices: UpstreamChoice[],
  suffix: (choice: UpstreamChoice) => string,
): UpstreamChoice[] {
  const counts = new Map<string, number>();
  for (const choice of choices) {
    counts.set(choice.label, (counts.get(choice.label) ?? 0) + 1);
  }
  return choices.map((choice) =>
    (counts.get(choice.label) ?? 0) > 1
      ? { ...choice, label: `${choice.label} · ${suffix(choice)}` }
      : choice,
  );
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
