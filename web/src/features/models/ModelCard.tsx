import type { ReactNode } from "react";

/**
 * One model, told completely, as a card in a responsive grid.
 *
 * The member catalogue used to be a table row per model: name, a couple of
 * badges, a price line, and two buttons — nothing that lets someone compare two
 * models against each other or decide which one to reach for. This is the
 * reference we went to look at (api.ilovecat520.me's model list) distilled to
 * what this gateway can honestly say: the model's identity, its real specs, the
 * capabilities we can derive from those specs, its price as the member will
 * actually pay it, and the two ways in.
 *
 * Deliberately *not* here: usage, latency and 24h availability. Those exist for
 * the operator, but nothing serves them to a member yet, and inventing a number
 * is the one thing this console must never do. The slots are typed so a caller
 * can drop them in the day the backend can answer.
 *
 * Shared on purpose (features/models): the operator's routing directory is the
 * same model, seen with more columns, and can adopt this card unchanged.
 */
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
  /** Extra line under the specs — usage, health, anything the owner can source. */
  metrics?: ReactNode;
  selected?: boolean;
  onSelect?: () => void;
  /** Shown top-right, e.g. the plan group or a status chip. */
  status?: ReactNode;
};

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
  selected,
  onSelect,
  status,
}: ModelCardProps) {
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
        {status ? <div className="model-card-status">{status}</div> : null}
      </header>

      {facts ? <div className="model-card-facts">{facts}</div> : null}
      {metrics ? <div className="model-card-metrics">{metrics}</div> : null}

      {capabilities?.length ? (
        <ul className="model-card-caps">
          {capabilities.map((capability) => (
            <li key={capability}>{capability}</li>
          ))}
        </ul>
      ) : null}

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
