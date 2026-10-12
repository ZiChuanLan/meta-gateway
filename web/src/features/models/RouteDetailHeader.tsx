import { Sparkles } from "lucide-react";
import type { ReactNode } from "react";
import type { Route, RoutingCandidate } from "../../api/types";
import { ActionMenu, type ActionMenuItem } from "../../components/ActionMenu";
import { Button, InfoTip, StatusBadge } from "../../components/ui";

/**
 * The selected route's header: which model this is, who serves it, whether it is
 * on, and the two clusters of controls — the primary action (run a call) on the
 * left, the settings group (routing mode + overflow menu) pinned right.
 *
 * Split out of the detail panel because the panel's body is the routing list,
 * which is a different concern with a different owner; this part only needs the
 * route and the handful of derived facts the board already computed.
 *
 * The two bare (i) icons that used to float here live on the elements they
 * explain: the mode hint on the mode control, the scope note on the summary line
 * under the title.
 */
type Text = (key: string, vars?: Record<string, string | number>) => string;

export type RouteDetailHeaderProps = {
  t: Text;
  route: Route;
  /** The member the gateway would use first, if any. */
  primary: RoutingCandidate | null | undefined;
  /** How many members can serve this route. */
  memberCount: number;
  /** A single member is pinned as the only path. */
  singleModePinned: boolean;
  onOpenTry: () => void;
  /** Opens the versioned billing editor for this route's model. */
  onOpenBilling: () => void;
  saveRoutingMode: {
    pendingId?: number | string | null;
    /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
    mutate: (...args: any[]) => unknown;
  };
  toggleRoute: { pendingId?: number | string | null };
  /** The routes menu, so the header's overflow is the same menu as the row's. */
  actions: ActionMenuItem[];
  /** The members the mode control picks its default from. */
  candidates: RoutingCandidate[];
  /** Which member group the board below is showing ("default" by default). */
  scopeLabel?: string;
  /** How the model has been answering lately; rides the status badge. */
  availability?: ReactNode;
};

export function RouteDetailHeader({
  t,
  route,
  primary,
  memberCount,
  singleModePinned,
  onOpenTry,
  onOpenBilling,
  saveRoutingMode,
  toggleRoute,
  actions,
  candidates,
  scopeLabel,
  availability,
}: RouteDetailHeaderProps) {
  return (
    <>
      <div className="detail-head">
        <div>
          <p className="detail-kicker">{t("modelsPage.detailKicker")}</p>
          <h2 className="mono">{route.model_pattern}</h2>
          {/* Who serves it, how many paths are left, and which group the list
              below is showing — one meta line instead of a row of its own. */}
          <small title={`${t("modelsPage.memberSummaryHint")} ${t("modelsPage.scopeHint")}`}>
            {primary
              ? t(singleModePinned ? "modelsPage.pinnedMember" : "modelsPage.servedBy", {
                  name: primary.channel.name,
                })
              : t("modelsPage.noUpstream")}
            {memberCount > 1 ? ` · ${t("modelsPage.extraPaths", { n: memberCount - 1 })}` : ""}
            {scopeLabel ? ` · ${scopeLabel}` : ""}
          </small>
        </div>
        <div className="detail-head-status">
          {availability}
          <StatusBadge value={route.enabled ? "enabled" : "disabled"} />
        </div>
      </div>

      <div className="detail-primary-bar">
        <Button icon={<Sparkles size={14} />} onClick={onOpenTry}>
          {t("try.open")}
        </Button>
        <Button variant="secondary" className="route-billing-entry" onClick={onOpenBilling}>
          {t("billing.open")}
        </Button>
        <span className="bar-spacer" />
        <div className="routing-mode-control" title={t("modelsPage.scopeHint")}>
          <span>{t("routing.mode.label")}</span>
          <InfoTip label={t("routing.modeHint")} />
          <select
            className="routing-mode-select"
            aria-label={t("routing.mode.label")}
            value={route.routing_mode || "auto"}
            disabled={saveRoutingMode.pendingId === route.id}
            onChange={(event) => {
              const next = event.target.value;
              if (next === "single") {
                // Manual single selection pins the top member; the per-member menu
                // pins a specific channel.
                const top = candidates.find((c) => c.member.enabled) ?? candidates[0];
                saveRoutingMode.mutate({
                  route,
                  mode: next,
                  singleMemberId: route.single_member_id ?? top?.member.id ?? null,
                });
                return;
              }
              saveRoutingMode.mutate({ route, mode: next });
            }}
          >
            <option value="auto">{t("routing.mode.auto")}</option>
            <option value="adaptive">{t("routing.mode.adaptive")}</option>
            <option value="latency">{t("routing.mode.latency")}</option>
            <option value="weighted">{t("routing.mode.weighted")}</option>
            <option value="single">{t("routing.mode.single")}</option>
          </select>
        </div>
        <ActionMenu
          compact
          label={t("common.moreActions")}
          disabled={toggleRoute.pendingId === route.id}
          items={actions}
        />
      </div>
    </>
  );
}
