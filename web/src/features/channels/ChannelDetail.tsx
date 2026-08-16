import { Activity, Pencil, RefreshCw, UserCheck } from "lucide-react"
import { useQuery } from "@tanstack/react-query"
import { api } from "../../api/client"
import type { AccountProbeResult, ChannelPingResult, ChannelOverview, Site } from "../../api/types"
import { Button, formatDate } from "../../components/ui"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { channelAccountState } from "../channelHealth"
import { ChannelAccountBadge, ChannelConnectivityBadge, ChannelHealthBadge, ChannelStatusBadges } from "./badges"
import { ChannelModelBlocks } from "./ChannelModelBlocks"
import { capabilityFlags } from "./helpers"

export function ChannelDetail({
  overview,
  site,
  busy,
  accountData,
  onCheckAccount,
  onPing,
  pingPending,
  pingResult,
  onRefresh,
  onEdit,
}: {
  overview: ChannelOverview;
  site?: Site;
  busy: boolean;
  accountData: AccountProbeResult | null;
  onCheckAccount: () => void;
  onPing: () => void;
  pingPending: boolean;
  pingResult: ChannelPingResult | null;
  onRefresh: () => void;
  onEdit: () => void;
}) {
  const { t, status } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const ch = overview.channel;
  const displayBase = ch.base_url || site?.base_url || "";
  const caps = capabilityFlags(overview);
  const probeError =
    overview.last_probe_error === "probe_slow"
      ? ""
      : overview.last_probe_error;
  const finance = useQuery({
    queryKey: ["finance"],
    queryFn: ({ signal }) => service.finance(signal),
    retry: false,
    staleTime: 120_000,
  });
  const financeItem = (finance.data?.items ?? []).find(
    (item) => item.channel_id === ch.id,
  );
  const quotaPerUnit =
    financeItem?.quota_per_unit && financeItem.quota_per_unit > 0
      ? financeItem.quota_per_unit
      : 500000;
  const balance =
    accountData?.quota != null && accountData.used_quota != null
      ? accountData.quota - accountData.used_quota
      : null;
  const formatCurrency = (value: number) =>
    (value / quotaPerUnit).toLocaleString("en-US", {
      style: "currency",
      currency: "USD",
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    });

  // Health probe history: availability over 24h + recent probe dots.
  const healthSummary = useQuery({
    queryKey: ["health-summary"],
    queryFn: ({ signal }) => service.healthSummary(24, signal),
    refetchInterval: 60_000,
  });
  const healthHistory = useQuery({
    queryKey: ["health-history", ch.id],
    queryFn: ({ signal }) => service.healthHistory(ch.id, signal),
    refetchInterval: 60_000,
  });
  const summaryItem = (healthSummary.data?.items ?? []).find(
    (item) => item.channel_id === ch.id,
  );
  const availability =
    summaryItem && summaryItem.total > 0
      ? Math.round(summaryItem.availability * 100)
      : null;
  const probePoints = healthHistory.data?.items ?? [];

  return (
    <>
      <div className="detail-head">
        <div className="detail-title-block">
          <p className="detail-kicker">{t("channels.detailKicker")}</p>
          <h2>{ch.name}</h2>
          <p className="detail-subtitle mono" title={displayBase}>
            <span>#{ch.id}</span>
            {displayBase ? <span className="detail-dot">·</span> : null}
            {displayBase ? (
              <a
                className="truncate base-url-link"
                href={displayBase}
                target="_blank"
                rel="noopener noreferrer"
              >
                {displayBase}
              </a>
            ) : null}
          </p>
        </div>
        <div className="capability-stack is-compact">
          <ChannelStatusBadges overview={overview} />
          {caps.tokenProblem ? (
            <span className="capability-chip is-warn">
              {t("channels.badge.tokenProblem")}
            </span>
          ) : null}
          {caps.checkinScheduled ? (
            <span className="capability-chip is-checkin">
              {t("channels.badge.checkinOn")}
            </span>
          ) : caps.checkinNeedsUserID ? (
            <span className="capability-chip is-warn">
              {t("channels.badge.needsUserId")}
            </span>
          ) : caps.hasUser ? (
            <span className="capability-chip is-muted">
              {t("channels.badge.checkinOff")}
            </span>
          ) : null}
          {caps.hasAPIKey ? (
            <span className="capability-chip is-key">
              {t("channels.badge.hasKey")}
            </span>
          ) : null}
          {caps.modelsReady ? (
            <span className="capability-chip is-models">
              {t("channels.badge.models")}
            </span>
          ) : null}
        </div>
      </div>

      <div className="detail-meta is-compact">
        <div>
          <span className="label">{t("common.type")}</span>
          <span>{ch.type_hint || site?.platform || "—"}</span>
        </div>
        <div>
          <span className="label">{t("channels.defaultRouting")}</span>
          <span title={t("channels.priorityHint")}>
            {t("channels.defaultPriorityWeight", {
              priority: ch.priority,
              weight: ch.weight,
            })}
          </span>
        </div>
        <div>
          <span className="label">{t("common.models")}</span>
          <span>{overview.model_count}</span>
        </div>
        {balance != null ? (
          <div>
            <span className="label">{t("channels.balance")}</span>
            <span title={t("channels.balanceHint")}>
              {formatCurrency(balance)}
            </span>
          </div>
        ) : null}
        <div>
          <span className="label">{t("common.checked")}</span>
          <span>
            {overview.last_checked_at
              ? formatDate(overview.last_checked_at)
              : t("channels.neverChecked")}
          </span>
        </div>
        <div>
          <span className="label">{t("channels.healthState")}</span>
          <span className="detail-health-value">
            <ChannelHealthBadge overview={overview} />
          </span>
        </div>
        {channelAccountState(overview) !== "ok" &&
        channelAccountState(overview) !== "unknown" ? (
          <div>
            <span className="label">{t("channels.accountState")}</span>
            <span className="detail-health-value">
              <ChannelAccountBadge overview={overview} />
            </span>
          </div>
        ) : null}
        <div>
          <span className="label">{t("channels.reachability")}</span>
          <span className="detail-health-value">
            <ChannelConnectivityBadge overview={overview} live={pingResult} />
            {pingResult?.checked_at ?? overview.last_ping_at ? (
              <small>
                {formatDate(
                  pingResult?.checked_at ?? overview.last_ping_at ?? "",
                )}
              </small>
            ) : null}
          </span>
        </div>
      {probeError || overview.last_error ? (
        <div className="detail-meta-error">
          <span className="label">{t("common.error")}</span>
          <span
            className="truncate"
            title={probeError || overview.last_error}
          >
            {status(probeError ?? overview.last_error ?? "")}
          </span>
        </div>
      ) : null}
      </div>

      {(() => {
        const models = (ch.models_csv || "")
          .split(",")
          .map((name) => name.trim())
          .filter(Boolean);
        if (!models.length) return null;
        const shown = models.slice(0, 50);
        return (
          <div className="detail-pricing">
            <span className="label">{t("channels.modelsTitle")}</span>
            <div className="detail-pricing-list">
              {shown.map((name) => (
                <div key={name} className="detail-pricing-row">
                  <code>{name}</code>
                </div>
              ))}
            </div>
            {models.length > 50 ? (
              <div className="is-quiet detail-pricing-more">
                {t("channels.modelsMore", {
                  count: models.length - 50,
                })}
              </div>
            ) : null}
          </div>
        );
      })()}

      <ChannelModelBlocks channelId={ch.id} />

      <div className="detail-pricing">
        <span className="label">{t("channels.healthTitle")}</span>
        <div className="health-summary-row">
          <strong className={"health-availability" + (availability == null ? " is-na" : availability >= 90 ? " is-good" : availability >= 70 ? " is-warn" : " is-bad")}>
            {availability == null
              ? t("channels.healthNoData")
              : `${availability}%`}
          </strong>
          {summaryItem ? (
            <span className="is-quiet" style={{ fontSize: 12 }}>
              {t("channels.healthProbes", {
                ok: summaryItem.ok,
                total: summaryItem.total,
              })}
            </span>
          ) : null}
        </div>
        {probePoints.length ? (
          <div
            className="health-dots"
            title={probePoints
              .slice()
              .reverse()
              .map(
                (p) =>
                  `${p.ok ? "✓" : "✗"} ${p.latency_ms}ms ${p.verdict} ${new Date(p.probed_at).toLocaleString()}`,
              )
              .join("\n")}
          >
            {probePoints
              .slice()
              .reverse()
              .slice(-30)
              .map((p) => (
                <span
                  key={p.id}
                  className={"health-dot" + (p.ok ? " is-ok" : " is-fail")}
                />
              ))}
          </div>
        ) : null}
      </div>

      <div className="detail-primary-bar is-compact">
        <Button
          icon={
            <Activity
              size={14}
              className={pingPending ? "spin" : ""}
            />
          }
          disabled={busy}
          onClick={onPing}
        >
          {pingPending
            ? t("channels.pinging")
            : pingResult
              ? pingResult.reachable
                ? t("channels.pingOk", {
                    ms: pingResult.latency_ms ?? "",
                  })
                : t("channels.pingFail", { error: pingResult.error ?? "" })
              : t("channels.ping")}
        </Button>
        {caps.hasUser ? (
          <Button
            variant="secondary"
            icon={<UserCheck size={14} />}
            disabled={busy}
            onClick={onCheckAccount}
          >
            {t("channels.checkAccount")}
          </Button>
        ) : null}
        <Button
          variant="secondary"
          icon={<RefreshCw size={14} className={busy ? "spin" : ""} />}
          disabled={busy}
          onClick={onRefresh}
        >
          {t("channels.fetchModels")}
        </Button>
        <Button
          variant="secondary"
          disabled={busy}
          onClick={onEdit}
          icon={<Pencil size={14} />}
        >
          {t("common.edit")}
        </Button>
        <p className="detail-actions-hint is-quiet">{t("channels.pathHint")}</p>
      </div>
    </>
  );
}
