import type { ChannelOverview } from "../api/types";

/**
 * Business health is deliberately separate from network reachability.
 *
 * A channel can be reachable while its model probe, credentials, or route
 * members are unhealthy. Conversely, a health verdict must never be used to
 * claim that the host is unreachable; that is the connectivity dimension.
 */
export const CHANNEL_HEALTH_STATES = [
  "disabled",
  "unhealthy",
  "degraded",
  "healthy",
  "unknown",
] as const;

export type ChannelHealthState = (typeof CHANNEL_HEALTH_STATES)[number];

export const CHANNEL_CONNECTIVITY_STATES = [
  "unknown",
  "reachable",
  "unreachable",
] as const;

export type ChannelConnectivityState =
  (typeof CHANNEL_CONNECTIVITY_STATES)[number];

export const CHANNEL_READINESS_STATES = [
  "disabled",
  "auto_disabled",
  "blocked",
  "missing_key",
  "token_invalid",
  "unhealthy",
  "degraded",
  "ready",
  "unverified",
] as const;

export type ChannelReadiness = (typeof CHANNEL_READINESS_STATES)[number];

export const CHANNEL_ACCOUNT_STATES = [
  "unknown",
  "ok",
  "invalid",
  "banned",
  "rate_limited",
  "failed",
] as const;

export type ChannelAccountState = (typeof CHANNEL_ACCOUNT_STATES)[number];

type LiveConnectivity = { reachable: boolean } | null | undefined;

function isHealthState(value: string | undefined): value is ChannelHealthState {
  return Boolean(
    value &&
      (CHANNEL_HEALTH_STATES as readonly string[]).includes(value),
  );
}

function isConnectivityState(
  value: string | undefined,
): value is ChannelConnectivityState {
  return Boolean(
    value &&
      (CHANNEL_CONNECTIVITY_STATES as readonly string[]).includes(value),
  );
}

function isAccountState(
  value: string | undefined,
): value is ChannelAccountState {
  return Boolean(
    value && (CHANNEL_ACCOUNT_STATES as readonly string[]).includes(value),
  );
}

/**
 * Returns the backend health verdict. The fallback only exists for old
 * gateways that do not yet return health_state; it intentionally does not
 * inspect last_ping_ok.
 */
export function channelHealthState(
  overview: ChannelOverview,
): ChannelHealthState {
  if (isHealthState(overview.health_state)) return overview.health_state;
  if (overview.channel.status === "disabled") return "disabled";
  if (overview.channel.status === "auto_disabled") return "unhealthy";
  if (overview.last_probe_at && overview.last_probe_ok === false) {
    return "unhealthy";
  }
  if (overview.failure_count > 0 || overview.cooling_member_count > 0) {
    return "degraded";
  }
  if (overview.last_probe_ok === true) return "healthy";
  return "unknown";
}

/**
 * Returns the credential-layer verdict (access_token/session probes). The
 * backend derives account_state from last_account_probe_*; the fallback only
 * covers older gateways without the field.
 */
export function channelAccountState(
  overview: ChannelOverview,
): ChannelAccountState {
  if (isAccountState(overview.account_state)) return overview.account_state;
  if (!overview.last_account_probe_at) return "unknown";
  if (overview.last_account_probe_ok === true) return "ok";
  switch (overview.last_account_probe_error) {
    case "upstream_unauthorized":
      return "invalid";
    case "account_banned":
      return "banned";
    case "rate_limited":
      return "rate_limited";
    default:
      return "failed";
  }
}

/**
 * Readiness is the UI's routing/configuration view. It is not a replacement
 * for health_state and is kept separate so missing keys or a disabled site do
 * not get mislabeled as network failures.
 */
export function channelReadiness(
  overview: ChannelOverview,
): ChannelReadiness {
  if (overview.channel.status === "auto_disabled") return "auto_disabled";
  if (overview.channel.status !== "enabled") return "disabled";
  if (!overview.site_usable) return "blocked";
  if (!overview.has_api_key) return "missing_key";
  if (
    overview.last_probe_at &&
    overview.last_probe_ok === false &&
    overview.last_probe_error === "upstream_unauthorized"
  ) {
    return "token_invalid";
  }

  const health = channelHealthState(overview);
  if (health === "healthy" && overview.last_probe_ok === true) {
    return "ready";
  }
  if (health === "unhealthy") return "unhealthy";
  if (health === "degraded") return "degraded";
  return "unverified";
}

export function isChannelReady(overview: ChannelOverview): boolean {
  return channelReadiness(overview) === "ready";
}

/**
 * The attention filter excludes the dedicated missing-key bucket, but includes
 * an unverified connection because it still needs an operator action.
 */
export function channelNeedsAttention(overview: ChannelOverview): boolean {
  const readiness = channelReadiness(overview);
  return (
    readiness !== "ready" &&
    readiness !== "missing_key" &&
    readiness !== "disabled"
  );
}

/**
 * Returns the latest network-layer verdict. A live manual result wins over
 * the persisted result; absent a timestamp the state is unknown, even though
 * the legacy boolean field defaults to false.
 */
export function channelConnectivityState(
  overview: ChannelOverview,
  live?: LiveConnectivity,
): ChannelConnectivityState {
  if (live) return live.reachable ? "reachable" : "unreachable";
  if (isConnectivityState(overview.connectivity_state)) {
    return overview.connectivity_state;
  }
  if (!overview.last_ping_at) return "unknown";
  return overview.last_ping_ok ? "reachable" : "unreachable";
}
