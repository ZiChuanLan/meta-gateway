import { useState } from "react";
import type { AvailabilityItem, AvailabilitySeries } from "../api/types";
import { useI18n } from "../i18n";
import { Button, Dialog } from "./ui";

export function availabilityPercent(ratio: number | null | undefined) {
  if (ratio == null) return "—";
  // Never round a partial success up to 100%.
  return `${(Math.floor(Math.max(0, Math.min(1, ratio)) * 1000) / 10).toFixed(1)}%`;
}
// Bands, applied to the ratio bar, the ticks and the figure alike: green means
// healthy, amber degraded, red broken. A window with 96% success is healthy.
export function availabilityBand(series: AvailabilitySeries | null | undefined) {
  if (!series || series.ratio == null) return "unknown";
  const ratio = series.ratio;
  if (ratio >= 0.95) return "ok";
  if (ratio >= 0.8) return "mixed";
  return "failed";
}
export function AvailabilityBars({ series }: { series: AvailabilitySeries }) {
  const { t } = useI18n();
  return (
    <span className="availability-bars" aria-hidden="true">
      {series.buckets.map((bucket, index) => {
        const rate = bucket.samples > 0 ? bucket.ok / bucket.samples : 0;
        const state =
          bucket.samples === 0
            ? "empty"
            : bucket.failed === 0 || rate >= 0.95
              ? "ok"
              : rate >= 0.8
                ? "mixed"
                : "failed";
        return (
          <span
            key={bucket.at}
            className={`availability-bar is-${state}`}
            title={`${new Date(bucket.at).toLocaleString()} · ${t("availability.bucket", { ok: bucket.ok, failed: bucket.failed, ignored: bucket.ignored })}`}
            data-bin={index}
          />
        );
      })}
    </span>
  );
}
// One short vertical bar, for a source that publishes a number without a
// timeline. Vertical on purpose: it reads as the same kind of object as the
// history ticks, and it never implies time buckets that do not exist.
export function AvailabilityGauge({ series }: { series: AvailabilitySeries }) {
  const state = availabilityBand(series);
  const share = Math.max(0, Math.min(1, series.ratio ?? 0)) * 100;
  return (
    <span className={`availability-gauge is-${state}`} aria-hidden="true">
      <i style={{ height: `${share}%` }} />
    </span>
  );
}
export function AvailabilityIndicator({
  item,
  loading = false,
  error = false,
  compact = false,
}: {
  item?: AvailabilityItem;
  loading?: boolean;
  error?: boolean;
  compact?: boolean;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const series = item?.primary;
  // Ticks only where a timeline really exists; a source that publishes just a
  // number gets the gauge instead of a row of empty ticks.
  const timeline = series && series.buckets.some((bucket) => bucket.samples > 0);
  const shape = !series || series.ratio == null ? "none" : timeline ? "history" : "gauge";
  const band = availabilityBand(series);
  const state = error
    ? "error"
    : loading
      ? "loading"
      : series?.ratio == null
        ? "unknown"
        : series.failed === 0 && series.samples > 0
          ? "ok"
          : "measured";
  const label = error
    ? t("availability.loadFailed")
    : loading
      ? t("common.loading")
      : series
        ? t(`availability.source.${series.source}`)
        : item?.ambiguous
          ? t("availability.ambiguous")
          : t("availability.noData");
  const title = error
    ? t("availability.loadFailed")
    : loading
      ? t("common.loading")
      : !series
        ? item?.ambiguous
          ? `${t("availability.ambiguous")} · ${t("availability.ambiguousHint")}`
          : t("availability.noDataHint")
        : [
            label,
            series.samples > 0
              ? t("availability.counts", {
                  ok: series.ok,
                  total: series.samples,
                  failed: series.failed,
                })
              : t("availability.aggregateOnly"),
            series.stale ? t("availability.stale") : "",
            series.paths.length > 1 ? t("availability.mixedPaths") : "",
            t("availability.explanation"),
          ]
            .filter(Boolean)
            .join(" · ");
  return (
    <>
      <button
        type="button"
        className={`availability-indicator is-${state}${compact ? " is-compact" : ""}`}
        disabled={loading || error || !item}
        onClick={(event) => {
          event.stopPropagation();
          setOpen(true);
        }}
        aria-label={`${t("availability.details")} ${item?.model ?? ""}`}
        title={title}
      >
        <span className="availability-readout">
          {shape === "history" && series ? <AvailabilityBars series={series} /> : null}
          {shape === "gauge" && series ? <AvailabilityGauge series={series} /> : null}
          <strong className={`is-${band}`}>{availabilityPercent(series?.ratio)}</strong>
        </span>
      </button>
      {open && item ? <AvailabilityDetails item={item} onClose={() => setOpen(false)} /> : null}
    </>
  );
}
function AvailabilityDetails({ item, onClose }: { item: AvailabilityItem; onClose: () => void }) {
  const { t } = useI18n();
  return (
    <Dialog
      title={`${t("availability.details")} · ${item.model}`}
      onClose={onClose}
      actions={
        <Button variant="secondary" onClick={onClose}>
          {t("common.close")}
        </Button>
      }
    >
      <p className="availability-explanation">{t("availability.explanation")}</p>
      {item.ambiguous ? <p className="inline-error">{t("availability.ambiguousHint")}</p> : null}
      {item.evidence.length === 0 ? (
        <p className="field-hint">{t("availability.noDataHint")}</p>
      ) : null}
      <div className="availability-evidence">
        {item.evidence.map((series, index) => (
          <section key={`${series.source}-${index}`} className="availability-source">
            <header>
              <strong>{t(`availability.source.${series.source}`)}</strong>
              <span>{availabilityPercent(series.ratio)}</span>
              {series.stale ? (
                <span className="availability-stale">{t("availability.stale")}</span>
              ) : null}
            </header>
            <AvailabilityBars series={series} />
            <p>
              {series.samples > 0
                ? t("availability.counts", {
                    ok: series.ok,
                    total: series.samples,
                    failed: series.failed,
                  })
                : t("availability.aggregateOnly")}
              {series.ignored > 0
                ? ` · ${t("availability.ignored", { count: series.ignored })}`
                : ""}
            </p>
            <small>
              {t("availability.updated")}:{" "}
              {series.last_at ? new Date(series.last_at).toLocaleString() : "—"}
            </small>
            {series.paths.length > 0 ? (
              <small className="mono">{series.paths.join(" · ")}</small>
            ) : null}
            <details className="availability-history">
              <summary>{t("availability.history")}</summary>
              <table className="table">
                <thead>
                  <tr>
                    <th>{t("availability.time")}</th>
                    <th>{t("availability.ok")}</th>
                    <th>{t("availability.failed")}</th>
                    <th>{t("availability.excluded")}</th>
                  </tr>
                </thead>
                <tbody>
                  {series.buckets.map((bucket) => (
                    <tr key={bucket.at}>
                      <td>{new Date(bucket.at).toLocaleString()}</td>
                      <td>{bucket.samples ? bucket.ok : "—"}</td>
                      <td>{bucket.samples ? bucket.failed : "—"}</td>
                      <td>{bucket.ignored || "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </details>
          </section>
        ))}
      </div>
    </Dialog>
  );
}
