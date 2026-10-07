import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Boxes, Check, Coins, Copy, KeyRound, ScrollText } from "lucide-react";
import { QuotaMeter } from "../components/QuotaMeter";
import { useI18n } from "../i18n";
import { formatCost } from "../lib/format";
import { accountRequest } from "../team/transport";
import type { Account } from "../team/types";

/**
 * A member's own home band.
 *
 * The overview used to open with the operator's readouts: an endpoint strip, a
 * channel-health matrix, the traffic of the whole gateway. None of it answers
 * the three questions somebody arriving at their own account actually has —
 * where do I connect, what is left of my budget, and what should I do next — and
 * the one number that decides whether their requests work at all (their credit)
 * existed only three pages away, as five plain figures.
 *
 * So this band is the member's answer to those questions and nothing else:
 * identity, the base URL with its copy, the two budgets as meters, the limits
 * the owner set, and four ways in. It reads `/me`, the same query the account
 * page reads, so the two cannot disagree about a balance. It renders nothing if
 * the account request fails: the page below it is still the page.
 */
export function MemberHome() {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const account = useQuery({
    queryKey: ["me", "account"],
    queryFn: ({ signal }) => accountRequest<Account>("/me", { signal }),
  });
  const data = account.data;
  if (!data) return null;

  const credit = data.credit;
  const base = data.branding.api_base_url || `${location.origin}/v1`;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(base);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      // Clipboard unavailable: the URL is on screen and selectable regardless.
    }
  };
  const actions = [
    { to: "/keys", label: t("member.home.action.newKey"), icon: <KeyRound size={15} /> },
    { to: "/models", label: t("member.home.action.browseModels"), icon: <Boxes size={15} /> },
    { to: "/logs", label: t("member.home.action.logs"), icon: <ScrollText size={15} /> },
    { to: "/account", label: t("member.home.action.redeem"), icon: <Coins size={15} /> },
  ];

  return (
    <section className="member-home" aria-label={t("member.home.band")}>
      <div className="member-home-copy">
        <p className="member-home-meta">
          <span className="member-home-chip">{data.branding.name}</span>
          <span className="member-home-chip">
            {t("member.home.planLabel")} · {data.policy.name}
          </span>
          <span className="member-home-chip">
            {t("member.home.limits", { rpm: data.policy.rpm, keys: data.policy.max_keys })}
          </span>
        </p>
        <div className="member-home-endpoint">
          <span className="member-home-endpoint-label">{t("member.home.endpointLabel")}</span>
          <code>{base}</code>
          <button type="button" className="member-home-copy-button" onClick={copy}>
            {copied ? <Check size={14} /> : <Copy size={14} />}
            <span>{copied ? t("dashboard.copied") : t("dashboard.copy")}</span>
          </button>
        </div>
        <nav className="member-home-actions" aria-label={t("member.home.actionsLabel")}>
          {actions.map((action) => (
            <Link key={action.to} className="member-action" to={action.to}>
              <span className="member-action-icon" aria-hidden="true">
                {action.icon}
              </span>
              {action.label}
            </Link>
          ))}
        </nav>
      </div>
      <div className="member-home-credit">
        <span className="member-home-credit-title">{t("member.home.creditTitle")}</span>
        <QuotaMeter
          label={t("member.home.creditTokens")}
          headline={
            credit.unlimited ? (
              t("member.home.unlimited")
            ) : (
              <>
                <span className="member-home-remaining">{t("member.home.remaining")}</span>
                {credit.available.toLocaleString()}
              </>
            )
          }
          used={credit.used}
          total={credit.total}
          format={(value) => value.toLocaleString()}
          percentLabel={(percent) => t("member.home.usedPercent", { percent })}
          unlimitedLabel={t("member.home.unlimited")}
        />
        <QuotaMeter
          label={t("member.home.creditCost")}
          headline={
            credit.cost_unlimited ? (
              t("member.home.unlimited")
            ) : (
              <>
                <span className="member-home-remaining">{t("member.home.remaining")}</span>
                {formatCost(credit.cost_available)}
              </>
            )
          }
          used={credit.cost_used}
          total={credit.cost_total}
          format={formatCost}
          percentLabel={(percent) => t("member.home.usedPercent", { percent })}
          unlimitedLabel={t("member.home.unlimited")}
        />
      </div>
    </section>
  );
}
