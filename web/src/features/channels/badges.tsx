import type { ChannelPingResult, ChannelOverview } from "../../api/types"
import { StatusBadge } from "../../components/ui"
import { useI18n } from "../../i18n"
import { channelAccountState, channelConnectivityState, channelHealthState, channelReadiness } from "../channelHealth"
import {  } from "./helpers"

export function channelHealthReasonLabel(
  overview: ChannelOverview,
  t: (key: string, vars?: Record<string, string | number>) => string,
) {
  switch (overview.health_reason) {
    case "manual_disabled":
      return t("channels.healthReason.manualDisabled");
    case "auto_disabled":
      return t("channels.healthReason.autoDisabled");
    case "not_checked":
      return t("channels.healthReason.notChecked");
    case "authentication_failed":
      return t("channels.healthReason.authenticationFailed");
    case "credential_scope":
      return t("channels.healthReason.credentialScope");
    case "credential_unavailable":
      return t("channels.healthReason.credentialUnavailable");
    case "invalid_base_url":
      return t("channels.healthReason.invalidBaseUrl");
    case "probe_failed":
      return t("channels.healthReason.probeFailed");
    case "probe_slow":
      return t("channels.healthReason.probeSlow");
    case "route_cooling":
      return t("channels.healthReason.routeCooling", {
        count: overview.cooling_member_count,
      });
    case "route_failures":
      return t("channels.healthReason.routeFailures", {
        count: overview.failure_count,
      });
    case "probe_ok":
      return t("channels.healthReason.probeOk");
    default:
      return t("channels.healthReason.unknown");
  }
}

export function ChannelHealthBadge({ overview }: { overview: ChannelOverview }) {
  const { t } = useI18n();
  const state = channelHealthState(overview);
  const reason = channelHealthReasonLabel(overview, t);
  return (
    <span
      className={`badge badge-${state}`}
      title={reason}
      data-testid="channel-health-badge"
    >
      {t(`channels.healthState.${state}`)}
    </span>
  );
}

export /**
 * Credential-layer badge (access_token/session probes). Distinct from the
 * business health badge: an expired access token is not a model-probe
 * failure, and vice versa. Rendered only when the account state needs an
 * operator action (invalid / banned / rate-limited / failed).
 */
function ChannelAccountBadge({ overview }: { overview: ChannelOverview }) {
  const { t } = useI18n();
  const state = channelAccountState(overview);
  const tone =
    state === "invalid" || state === "banned"
      ? "unhealthy"
      : state === "rate_limited" || state === "failed"
        ? "warn"
        : state === "ok"
          ? "healthy"
          : "neutral";
  return (
    <span
      className={`badge badge-${tone}`}
      title={t(`channels.accountState.${state}`)}
      data-testid="channel-account-badge"
    >
      {t(`channels.accountState.${state}`)}
    </span>
  );
}

export /**
 * Health + readiness as one badge. When a connection merely lacks an API key
 * the two stacked badges read as noise on every row, so they merge into a
 * single "state · needs key" badge; the tooltip keeps the health reason.
 */
function ChannelStatusBadges({ overview }: { overview: ChannelOverview }) {
  const { t } = useI18n();
  const readiness = channelReadiness(overview);
  if (readiness === "missing_key") {
    const health = channelHealthState(overview);
    return (
      <span
        className="badge badge-missing-key"
        title={channelHealthReasonLabel(overview, t)}
      >
        {t(`channels.healthState.${health}`)} · {t("channels.badge.missingKey")}
      </span>
    );
  }
  return (
    <>
      <ChannelHealthBadge overview={overview} />
      <ChannelReadinessBadge overview={overview} />
    </>
  );
}

export function ChannelReadinessBadge({ overview }: { overview: ChannelOverview }) {
  const readiness = channelReadiness(overview);
  if (
    readiness !== "auto_disabled" &&
    readiness !== "blocked" &&
    readiness !== "missing_key" &&
    readiness !== "token_invalid"
  ) {
    return null;
  }
  return <StatusBadge value={readiness} />;
}

export function ChannelConnectivityBadge({
  overview,
  live,
}: {
  overview: ChannelOverview;
  live?: ChannelPingResult | null;
}) {
  const { t, status } = useI18n();
  const state = channelConnectivityState(overview, live);
  const latency = live?.latency_ms ?? overview.last_ping_ms;
  const error = live ? live.error : overview.last_ping_error;
  const detail =
    state === "reachable"
      ? latency != null && latency > 0
        ? t("channels.pingOkShort", { ms: latency })
        : ""
      : state === "unreachable" && error
        ? status(error)
        : "";
  const stateLabel =
    state === "reachable"
      ? t("channels.reachable")
      : state === "unreachable"
        ? t("channels.unreachable")
        : t("channels.connectivityUnknown");
  // The error label often repeats the state label ("不可达 · 不可达");
  // only append detail when it adds information.
  const suffix = detail && detail !== stateLabel ? ` · ${detail}` : "";
  return (
    <span
      className={`badge badge-${state}`}
      data-testid="channel-connectivity-badge"
    >
      {stateLabel}
      {suffix}
    </span>
  );
}
