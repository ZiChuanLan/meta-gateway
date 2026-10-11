import { useState } from "react";
import { Link } from "react-router-dom";
import type { AvailabilityItem, AvailabilitySeries } from "../api/types";
import { useI18n } from "../i18n";
import { Button, Dialog } from "./ui";

export function availabilityPercent(ratio: number | null | undefined) {
  if (ratio == null) return "—";
  // Never round a partial success up to 100%.
  return `${(Math.floor(Math.max(0, Math.min(1, ratio)) * 1000) / 10).toFixed(1)}%`;
}
export function AvailabilityBars({ series }: { series: AvailabilitySeries }) {
  const { t } = useI18n();
  return (
    <span className="availability-bars" aria-hidden="true">
      {series.buckets.map((bucket, index) => {
        const state =
          bucket.samples === 0
            ? "empty"
            : bucket.failed === 0
              ? "ok"
              : bucket.ok === 0
                ? "failed"
                : "mixed";
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
      >
        <span className="availability-readout">
          {series ? (
            <AvailabilityBars series={series} />
          ) : (
            <span className="availability-empty-bars" aria-hidden="true">
              {Array.from({ length: 24 }, (_, i) => (
                <i key={i} />
              ))}
            </span>
          )}
          <strong>{availabilityPercent(series?.ratio)}</strong>
        </span>
        <span className="availability-caption">
          {label}
          {series?.stale ? ` · ${t("availability.stale")}` : ""}
          {series?.paths.length && series.paths.length > 1
            ? ` · ${t("availability.mixedPaths")}`
            : ""}
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
      {item.site_id ? (
        <Link
          className="button button-quiet"
          to={`/channels?view=observations&site=${item.site_id}&model=${encodeURIComponent(item.model)}`}
          onClick={onClose}
        >
          {t("availability.openSite")}
        </Link>
      ) : null}
    </Dialog>
  );
}
