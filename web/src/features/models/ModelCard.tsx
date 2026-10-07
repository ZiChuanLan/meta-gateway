import type { ReactNode } from "react";
import { useI18n } from "../../i18n";
import { formatCost, formatTokens, useCurrency } from "../../lib/format";

/**
 * One model, told completely, as a card in a responsive grid.
 *
 * The member catalogue used to be a table row per model: name, a couple of
 * badges, a price line, and two buttons — nothing that lets someone compare two
 * models against each other or decide which one to reach for. This is the
 * reference we went to look at (api.ilovecat520.me's model list) distilled to
 * what this gateway can honestly say: the model's identity, its real specs, the
 * capabilities we can derive from those specs, what this account has actually
 * done with it, whether the upstreams behind it are answering, its price as the
 * member will actually pay it, and the two ways in.
 *
 * The personal figures and the upstream health arrive as typed props rather
 * than as free-form slots, because a card that prints "3.2s" has to know whether
 * that number is a mean of 400 requests or a single sample: an unmeasured model
 * prints a dash, never a plausible-looking default.
 *
 * Shared on purpose (features/models): the operator's routing directory is the
 * same model, seen with more columns, and can adopt this card unchanged.
 */

/** What this account did with this model, over whatever window the host asked. */
export type ModelCardStats = {
  requests: number;
  ok: number;
  failed: number;
  /** 2xx share, or null when nothing was measured in the window. */
  successRate: number | null;
  avgLatencyMs: number | null;
  p95Ms: number | null;
  tokens: number;
  cost: number;
  /** The window these figures cover, worded by the host that read them. */
  windowLabel: string;
};

/** The gateway's own probes for the upstreams that serve this model. */
export type ModelCardHealth = {
  /** Enabled upstreams serving this model, for this reader. */
  upstreams: number;
  /** How many of them answered their latest probe. */
  healthy: number;
  /** How many of them have answered a probe at least once. */
  probed: number;
  /** Probe samples behind the ratio; 0 means "never probed", not "all good". */
  samples: number;
  /** 0..1, meaningful only when samples > 0. */
  availability: number;
  avgLatencyMs: number;
};

export type ModelCardProps = {
  name: string;
  /** Vendor or group chip: who makes it, or where it came from. */
  vendor?: string;
  /** The grouping chip (the member's plan group, the operator's model family). */
  group?: string;
  /** True when the model is one the reader has arranged / pinned. */
  arranged?: boolean;
  /** Spec line: context window, modalities, thinking. */
  facts?: ReactNode;
  /** Chips derived from those same fields — never a claim we cannot source. */
  capabilities?: string[];
  /** Price as the reader pays it, including the group multiplier when there is one. */
  prices?: ReactNode;
  /** The ways in: details, connect, arrange. */
  actions?: ReactNode;
  /** Extra line under the specs — a note the host owns. */
  metrics?: ReactNode;
  /** This account's own figures for the model. Omitted when the host cannot read them. */
  stats?: ModelCardStats;
  /** Upstream probe state. Omitted when the host may not read it. */
  health?: ModelCardHealth;
  selected?: boolean;
  onSelect?: () => void;
  /** Shown top-right, e.g. the plan group or a status chip. */
  status?: ReactNode;
};

/** Seconds with one decimal; whole seconds past ten, where a decimal is noise. */
function seconds(ms: number) {
  return `${(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)}s`;
}

export function ModelCard({
  name,
  vendor,
  group,
  arranged,
  facts,
  capabilities,
  prices,
  actions,
  metrics,
  stats,
  health,
  selected,
  onSelect,
  status,
}: ModelCardProps) {
  const { t } = useI18n();
  // The price block subscribes to the currency; these two figures are money as
  // well, so the same subscription has to cover them.
  useCurrency();
  const healthTone =
    !health || health.samples === 0
      ? "unknown"
      : health.availability >= 0.99
        ? "ok"
        : health.availability >= 0.5
          ? "degraded"
          : "down";
  const healthLabel =
    healthTone === "unknown"
      ? t("modelsPage.health.unprobed")
      : healthTone === "ok"
        ? t("modelsPage.health.up")
        : healthTone === "degraded"
          ? t("modelsPage.health.degraded")
          : t("modelsPage.health.down");
  return (
    <article
      className={`model-card${selected ? " is-selected" : ""}${onSelect ? " is-clickable" : ""}`}
      tabIndex={onSelect ? 0 : undefined}
      aria-label={name}
      /* The layout owns the detail dialog and looks for this marker on the
         clicked element — the table rows carry it too, so cards and rows open
         the same detail the same way. */
      data-model-selectable={onSelect ? "true" : undefined}
      onClick={onSelect}
      onKeyDown={
        onSelect
          ? (event) => {
              if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                onSelect();
              }
            }
          : undefined
      }
    >
      <header className="model-card-head">
        <div className="model-card-identity">
          <h3 className="mono" title={name}>
            {name}
          </h3>
          <div className="model-card-chips">
            {vendor ? <span className="model-card-chip is-vendor">{vendor}</span> : null}
            {group ? <span className="model-card-chip">{group}</span> : null}
            {arranged ? <span className="model-card-chip is-arranged">★</span> : null}
          </div>
        </div>
        {status || health ? (
          <div className="model-card-status">
            {/* The state badge sits where the eye lands first (top right); the
                evidence for it — the ratio and which upstreams are behind it —
                stays on the card's own line. */}
            {status ?? (
              <span className={`model-health-badge is-${healthTone}`}>
                <span className="model-health-dot" aria-hidden="true" />
                {healthLabel}
              </span>
            )}
          </div>
        ) : null}
      </header>

      {facts ? <div className="model-card-facts">{facts}</div> : null}

      {stats ? (
        <div className="model-card-usage">
          {/* The window on its own line, once — not repeated inside all four
              labels, where "用量 · 近 24 小时" wrapped and knocked the four
              columns out of alignment with each other. */}
          <span className="model-card-usage-caption">{stats.windowLabel}</span>
          <dl className="model-card-stats">
            <div>
              <dt>{t("modelsPage.stats.usage")}</dt>
              <dd>{stats.requests > 0 ? formatTokens(stats.tokens) : "—"}</dd>
              <small>{t("modelsPage.stats.requests", { n: stats.requests })}</small>
            </div>
            <div>
              <dt>{t("modelsPage.stats.success")}</dt>
              <dd>
                {stats.requests > 0 && stats.successRate !== null
                  ? `${Math.round(stats.successRate * 100)}%`
                  : "—"}
              </dd>
              <small>
                {stats.failed > 0 ? t("modelsPage.stats.failed", { n: stats.failed }) : ""}
              </small>
            </div>
            <div>
              <dt>{t("modelsPage.stats.latency")}</dt>
              <dd>{stats.avgLatencyMs === null ? "—" : seconds(stats.avgLatencyMs)}</dd>
              <small>
                {stats.p95Ms === null
                  ? ""
                  : t("modelsPage.stats.p95", { value: seconds(stats.p95Ms) })}
              </small>
            </div>
            <div>
              <dt>{t("modelsPage.stats.cost")}</dt>
              <dd>{stats.requests > 0 && stats.cost > 0 ? formatCost(stats.cost) : "—"}</dd>
              <small />
            </div>
          </dl>
        </div>
      ) : null}

      {health ? (
        // The gateway's probes, not this account's calls: a member who has never
        // called the model still gets a true answer for it, and one who has gets
        // told which of the two numbers they are looking at.
        <div className={`model-card-health is-${healthTone}`}>
          {health.samples > 0 ? (
            <span className="model-health-figure">
              {Math.round(health.availability * 100)}%
              {health.avgLatencyMs > 0 ? (
                <span className="model-health-latency">{seconds(health.avgLatencyMs)}</span>
              ) : null}
            </span>
          ) : null}
          {/* An unprobed model must not be summarised as "0/1 up": nothing has
              been asked yet, so the only true line is how many upstreams there
              are. */}
          <span className="model-health-source">
            {health.samples > 0
              ? t("modelsPage.health.upstreams", { up: health.healthy, total: health.upstreams })
              : t("modelsPage.health.upstreamCount", { total: health.upstreams })}
          </span>
        </div>
      ) : null}

      {capabilities?.length ? (
        <ul className="model-card-caps">
          {capabilities.map((capability) => (
            <li key={capability}>{capability}</li>
          ))}
        </ul>
      ) : null}

      {metrics ? <div className="model-card-metrics">{metrics}</div> : null}

      <div className="model-card-foot">
        <div className="model-card-price">{prices}</div>
        <div className="model-card-actions">{actions}</div>
      </div>
    </article>
  );
}

/** The grid the cards live in. One column on a phone, two on a tablet, three wider. */
export function ModelCardGrid({ children }: { children: ReactNode }) {
  return <div className="model-card-grid">{children}</div>;
}
