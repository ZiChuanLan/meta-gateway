import type { ReactNode } from "react";
import { Check, Copy } from "lucide-react";
import { ActionMenu, type ActionMenuItem } from "../../components/ActionMenu";
import { useI18n } from "../../i18n";
import { formatCost, formatTokens } from "../../lib/format";

/**
 * One client token, as the person who holds it reads it.
 *
 * The member's token page used to be the site's table seen through a narrower
 * window: name, status, created, actions — and no way to tell one token from
 * another, because the masked tail the gateway already stores never reached the
 * screen. Someone with three tokens for three editors could not answer "which
 * of these is the one in Cursor", and nothing on the row said what a token had
 * cost.
 *
 * A card answers those three questions in the order they get asked — which token
 * is this (masked tail, with copy), what has it used (tokens and money), when
 * did it last work — and leaves the operator's columns (scopes, tenant groups,
 * upstream detail) to the site's table, which is driven by the same rows.
 *
 * The copy button reveals through the API and copies in one step: the plaintext
 * exists in the database and the copy is a read of it, exactly like the reveal
 * dialog, only without making the reader transcribe a secret by hand.
 */
export function KeyCard({
  name,
  id,
  hint,
  usedTokens,
  cost,
  lastUsedAt,
  modelScope,
  expiresAt,
  allowedIPs,
  busy,
  onCopy,
  copied,
  copyPending,
  toggle,
  actions,
}: {
  name: string;
  id: number;
  /** The masked tail the gateway stores; empty on tokens minted before it did. */
  hint?: string;
  usedTokens: number;
  cost: number;
  lastUsedAt?: string;
  modelScope?: string;
  expiresAt?: string;
  allowedIPs?: string;
  busy?: boolean;
  onCopy?: () => void;
  copied?: boolean;
  copyPending?: boolean;
  /** The on/off control, built by the page so its logic stays in one place. */
  toggle?: ReactNode;
  actions: ActionMenuItem[];
}) {
  const { t } = useI18n();
  const scope = (modelScope ?? "").trim();
  const ips = (allowedIPs ?? "").trim();
  const masked = hint ? `••••••••••${hint}` : "••••••••••••";
  return (
    <article className={`key-card${busy ? " is-busy" : ""}`}>
      <header className="key-card-head">
        <div className="key-card-identity">
          <h3 title={name}>{name}</h3>
          <small>#{id}</small>
        </div>
        <div className="key-card-head-actions">
          {toggle}
          <ActionMenu compact label={t("common.moreActions")} title={name} items={actions} />
        </div>
      </header>

      <div className="key-card-token">
        <code title={t("keys.card.tokenHint")}>{masked}</code>
        {onCopy ? (
          <button
            type="button"
            className="key-card-copy"
            onClick={onCopy}
            disabled={copyPending}
            aria-label={t("keys.card.copyToken", { name })}
          >
            {copied ? <Check size={13} /> : <Copy size={13} />}
            <span>{copied ? t("dashboard.copied") : t("dashboard.copy")}</span>
          </button>
        ) : null}
      </div>

      <dl className="key-card-usage">
        <div>
          <dt>{t("keys.stat.usedTokens")}</dt>
          <dd>{formatTokens(usedTokens ?? 0)}</dd>
        </div>
        <div>
          <dt>{t("keys.costCol")}</dt>
          <dd>{formatCost(cost ?? 0)}</dd>
        </div>
        <div>
          <dt>{t("keys.card.lastUsed")}</dt>
          <dd className={lastUsedAt ? "" : "is-quiet"}>
            {lastUsedAt ? relative(lastUsedAt, t) : t("keys.card.never")}
          </dd>
        </div>
      </dl>

      <footer className="key-card-meta">
        {scope ? (
          <span className="key-card-chip" title={scope}>
            {t("keys.card.models")}
            <strong>{scope}</strong>
          </span>
        ) : (
          <span className="key-card-chip">
            {t("keys.card.models")}
            <strong>{t("keys.card.modelsAll")}</strong>
          </span>
        )}
        <span className="key-card-chip">
          {t("keys.card.expires")}
          <strong>{expiresAt ? shortDate(expiresAt) : t("keys.card.neverExpires")}</strong>
        </span>
        <span className="key-card-chip">
          {t("keys.card.ips")}
          <strong>{ips || t("keys.card.ipsAny")}</strong>
        </span>
      </footer>
    </article>
  );
}

/**
 * "3 天前" — the question is "is this token still in use", and a timestamp makes
 * the reader do the subtraction. Falls back to the raw date when the stamp is
 * unparseable rather than inventing an age.
 */
function relative(value: string, t: (key: string, vars?: Record<string, string | number>) => string) {
  const at = new Date(value).getTime();
  if (!Number.isFinite(at)) return value;
  const minutes = Math.floor((Date.now() - at) / 60_000);
  if (minutes < 1) return t("keys.card.justNow");
  if (minutes < 60) return t("keys.card.minutesAgo", { n: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t("keys.card.hoursAgo", { n: hours });
  const days = Math.floor(hours / 24);
  if (days < 30) return t("keys.card.daysAgo", { n: days });
  return shortDate(value);
}

function shortDate(value: string) {
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return value;
  return at.toISOString().slice(0, 10);
}
