import { ChevronDown } from "lucide-react";

/**
 * The effective routing policy for the selected route, as a disclosure: which
 * signals the gateway will fail over on, and the retry budget that applies —
 * including *where* each number comes from (this model, or the global default).
 *
 * It answers a question the operator otherwise has to reconstruct from three
 * places (the route's own columns, the global runtime settings, and whether a
 * single member is pinned), so the source of every value is printed beside it.
 */
export type RoutePolicyCardProps = {
  t: (key: string, vars?: Record<string, string | number>) => string;
  /** Which failover signals are on, and where the policy came from. */
  effectivePolicy: { latency?: boolean; error?: boolean; source: string } | null;
  effectiveRetryRounds: number | null | undefined;
  effectiveChannelRetries: number | null | undefined;
  /** A pinned single member overrides the route's own retry settings. */
  singleModeApplies: boolean;
  retryPolicyIsOverridden: boolean;
  channelRetryPolicyIsOverridden: boolean;
  /** Cross-channel failover is a deployment-wide switch. */
  crossChannelFailoverEnabled: boolean | undefined;
};

export function RoutePolicyCard({
  t,
  effectivePolicy,
  effectiveRetryRounds,
  effectiveChannelRetries,
  singleModeApplies,
  retryPolicyIsOverridden,
  channelRetryPolicyIsOverridden,
  crossChannelFailoverEnabled,
}: RoutePolicyCardProps) {
  return (
    <details className="route-policy-disclosure">
      <summary>
        {t("routing.effectivePolicy")}
        <ChevronDown size={13} />
      </summary>
      <div className="routing-policy-card">
        <div className="routing-policy-summary">
          <span className="routing-policy-title">{t("routing.effectivePolicy")}</span>
          {effectivePolicy ? (
            <>
              <span className={`routing-signal${effectivePolicy.latency ? " is-on" : " is-off"}`}>
                {t("routing.signal.latency")}:{" "}
                {effectivePolicy.latency ? t("routing.signal.on") : t("routing.signal.off")}
              </span>
              <span className={`routing-signal${effectivePolicy.error ? " is-on" : " is-off"}`}>
                {t("routing.signal.error")}:{" "}
                {effectivePolicy.error ? t("routing.signal.on") : t("routing.signal.off")}
              </span>
              <span className="routing-policy-source">{t(effectivePolicy.source)}</span>
            </>
          ) : (
            <span className="routing-policy-source">{t("routing.policyLoading")}</span>
          )}
        </div>
        <div className="routing-retry-summary">
          <span className="routing-policy-title">{t("routing.retryPolicy")}</span>
          <span className="routing-policy-value">
            {t("routing.retryRounds")}: {effectiveRetryRounds ?? "?"}
            <small>
              {t(
                singleModeApplies
                  ? "routing.policySource.single"
                  : retryPolicyIsOverridden
                    ? "routing.policySource.model"
                    : "routing.policySource.global",
              )}
            </small>
          </span>
          <span className="routing-policy-value">
            {t("routing.channelRetry")}: {effectiveChannelRetries ?? "?"}
            <small>
              {t(
                channelRetryPolicyIsOverridden
                  ? "routing.policySource.model"
                  : "routing.policySource.global",
              )}
            </small>
          </span>
          <span className={`routing-signal${crossChannelFailoverEnabled ? " is-on" : " is-off"}`}>
            {t("routing.failover")}:{" "}
            {crossChannelFailoverEnabled === undefined
              ? "?"
              : crossChannelFailoverEnabled
                ? t("routing.signal.on")
                : t("routing.signal.off")}
          </span>
        </div>
      </div>
    </details>
  );
}
