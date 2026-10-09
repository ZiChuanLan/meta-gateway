import type { ReactNode } from "react";

/**
 * A budget, as every level of this product spells it: used over total, the
 * percentage, and a bar that turns amber past 70% and red past 90%.
 *
 * The markup was written three times before this file existed — the users
 * boards, the token table and (now) the member's own credit panel each had their
 * own copy of the same arithmetic and their own class names, so the same budget
 * could look different depending on which page you read it from, and a page that
 * did not happen to load the second stylesheet got an unstyled bar. There is one
 * implementation of the meter and one set of rules for it; callers own the
 * words, this owns the shape.
 *
 * `total <= 0` means unlimited, and it is the caller's job to say so in its own
 * vocabulary — a meter is not drawn for a budget nobody can exhaust.
 */
export type QuotaLevel = "ok" | "high" | "critical";

export function quotaLevel(percent: number): QuotaLevel {
  if (percent >= 90) return "critical";
  if (percent >= 70) return "high";
  return "ok";
}

export function quotaPercent(used: number, total: number): number {
  if (!Number.isFinite(total) || total <= 0) return 0;
  const raw = (used / total) * 100;
  return Math.max(0, Math.min(100, Math.round(raw)));
}

/** The bar alone: for the tables that print their own figures above it. */
export function QuotaBar({ percent, label }: { percent: number; label?: string }) {
  return (
    <span className="quota-bar" role={label ? "img" : undefined} aria-label={label}>
      {/* Scale, not width: the track is full width and the fill is transformed, so a
            changing percentage never triggers layout. */}
      <span
        className={`quota-bar-fill is-${quotaLevel(percent)}`}
        style={{ transform: `scaleX(${percent / 100})` }}
      />
    </span>
  );
}

/**
 * The full meter: caption, the prominent figure, used/total, percentage and bar.
 *
 * `headline` is what the reader came for — a remaining balance belongs there,
 * with the totals beneath it, rather than four numbers of equal weight that have
 * to be compared to be understood.
 */
export function QuotaMeter({
  label,
  headline,
  used,
  total,
  format,
  percentLabel,
  unlimitedLabel,
  footer,
}: {
  label?: string;
  headline?: ReactNode;
  used: number;
  total: number;
  format: (value: number) => string;
  /** aria-label for the bar, built by the caller in its own language. */
  percentLabel?: (percent: number) => string;
  unlimitedLabel?: ReactNode;
  footer?: ReactNode;
}) {
  if (!total || total <= 0) {
    return (
      <span className="quota-budget">
        {label ? <span className="quota-budget-label">{label}</span> : null}
        <span className="quota-budget-unlimited">{unlimitedLabel ?? "—"}</span>
        {footer}
      </span>
    );
  }
  const percent = quotaPercent(used, total);
  return (
    <span className="quota-budget">
      {label ? <span className="quota-budget-label">{label}</span> : null}
      {headline ? <span className="quota-budget-headline">{headline}</span> : null}
      <span className="quota-budget-figures">
        {format(used)} / {format(total)}
        <span className={`quota-budget-percent is-${quotaLevel(percent)}`}>{percent}%</span>
      </span>
      <QuotaBar percent={percent} label={percentLabel?.(percent)} />
      {footer}
    </span>
  );
}
