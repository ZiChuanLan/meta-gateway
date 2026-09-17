import { CalendarRange, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "./ui";
import { useI18n } from "../i18n";

/**
 * Shared time-window control for the overview and log workspaces.
 *
 * Rolling presets are resolved against a `now` that only advances on a tick or
 * an explicit refresh, so the value handed to a query stays byte-stable between
 * renders (a `new Date()` per render would churn every query key).
 */

export type TimePreset = {
  id: string;
  /** Window length in minutes; 0 means "no lower bound" (all time). */
  minutes: number;
  labelKey: string;
};

export const TIME_PRESETS: TimePreset[] = [
  { id: "15m", minutes: 15, labelKey: "timeRange.preset15m" },
  { id: "1h", minutes: 60, labelKey: "timeRange.preset1h" },
  { id: "6h", minutes: 360, labelKey: "timeRange.preset6h" },
  { id: "24h", minutes: 1440, labelKey: "timeRange.preset24h" },
  { id: "7d", minutes: 10080, labelKey: "timeRange.preset7d" },
  { id: "30d", minutes: 43200, labelKey: "timeRange.preset30d" },
  { id: "all", minutes: 0, labelKey: "timeRange.presetAll" },
];

export const CUSTOM_PRESET = "custom";

export type TimeRangeController = {
  /** Preset id, or "custom" when the absolute inputs own the window. */
  preset: string;
  /** Resolved inclusive lower/upper bound (RFC3339); undefined = open-ended. */
  since?: string;
  until?: string;
  /** Raw `datetime-local` drafts for the custom inputs. */
  draftSince: string;
  draftUntil: string;
  /** True when both drafts parse and are ordered. */
  draftValid: boolean;
  setPreset: (id: string) => void;
  setDraft: (patch: { since?: string; until?: string }) => void;
  /** Re-anchor rolling presets to the current wall clock. */
  refresh: () => void;
};

/** `datetime-local` value (local wall clock, second precision) from an ISO instant. */
export function toLocalInput(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** Local `datetime-local` value → RFC3339. Empty stays empty (open-ended). */
export function fromLocalInput(value: string): string | undefined {
  const raw = value.trim();
  if (!raw) return undefined;
  const d = new Date(raw);
  if (Number.isNaN(d.getTime())) return undefined;
  return d.toISOString();
}

export function useTimeRange(
  initialPreset = "24h",
  /**
   * Optional absolute seeds (RFC3339) used when the custom range is restored
   * from a URL. Read once — the hook owns the state afterwards.
   */
  seed?: { since?: string; until?: string },
): TimeRangeController {
  const [preset, setPresetState] = useState(initialPreset);
  const [drafts, setDrafts] = useState<{ since: string; until: string }>(() => ({
    since:
      toLocalInput(seed?.since) ||
      toLocalInput(new Date(Date.now() - 60 * 60_000).toISOString()),
    until: toLocalInput(seed?.until) || toLocalInput(new Date().toISOString()),
  }));
  const [tick, setTick] = useState(0);

  // Rolling windows follow the clock; re-anchor once a minute.
  useEffect(() => {
    if (preset === CUSTOM_PRESET || preset === "all") return;
    const id = window.setInterval(() => setTick((v) => v + 1), 60_000);
    return () => window.clearInterval(id);
  }, [preset]);

  const now = useMemo(() => Date.now(), [tick]);

  const draftSince = fromLocalInput(drafts.since);
  const draftUntil = fromLocalInput(drafts.until);
  const draftValid =
    !draftSince ||
    !draftUntil ||
    new Date(draftUntil).getTime() >= new Date(draftSince).getTime();

  const bounds = useMemo(() => {
    if (preset === CUSTOM_PRESET) {
      // An invalid draft must not silently become "all time": hold the last
      // usable window by clamping the upper bound.
      if (!draftValid) return { since: draftSince, until: draftSince };
      return { since: draftSince, until: draftUntil };
    }
    const minutes = TIME_PRESETS.find((p) => p.id === preset)?.minutes ?? 1440;
    if (minutes <= 0) return {};
    return {
      since: new Date(now - minutes * 60_000).toISOString(),
      until: new Date(now).toISOString(),
    };
  }, [preset, draftSince, draftUntil, draftValid, now]);

  const setDraft = useCallback((patch: { since?: string; until?: string }) => {
    setDrafts((prev) => ({ ...prev, ...patch }));
    setPresetState(CUSTOM_PRESET);
  }, []);

  return {
    preset,
    since: bounds.since,
    until: bounds.until,
    draftSince: drafts.since,
    draftUntil: drafts.until,
    draftValid,
    setPreset: setPresetState,
    setDraft,
    refresh: () => setTick((v) => v + 1),
  };
}

/** Compact local clock stamp ("09-17 20:16:03"); null when unset/unparseable. */
export function formatClock(iso?: string): string | null {
  if (!iso) return null;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return null;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** Compact absolute caption for a resolved window ("09-17 14:00 → 15:00"). */
export function describeRange(
  since: string | undefined,
  until: string | undefined,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  const from = formatClock(since);
  const to = formatClock(until);
  return `${from ?? t("timeRange.openStart")} → ${to ?? t("timeRange.openEnd")}`;
}

export function TimeRangePicker({
  range,
  onRefresh,
  compact = false,
}: {
  range: TimeRangeController;
  /** Invoked alongside the re-anchor so callers can force a refetch. */
  onRefresh?: () => void;
  compact?: boolean;
}) {
  const { t } = useI18n();
  const custom = range.preset === CUSTOM_PRESET;
  return (
    <div className={`time-range${compact ? " is-compact" : ""}`}>
      <div className="time-range-presets" role="tablist" aria-label={t("timeRange.label")}>
        {TIME_PRESETS.map((p) => (
          <button
            key={p.id}
            type="button"
            role="tab"
            aria-selected={range.preset === p.id}
            className={range.preset === p.id ? "is-active" : ""}
            onClick={() => range.setPreset(p.id)}
          >
            {t(p.labelKey)}
          </button>
        ))}
        <button
          type="button"
          role="tab"
          aria-selected={custom}
          className={custom ? "is-active" : ""}
          onClick={() => range.setPreset(CUSTOM_PRESET)}
        >
          <CalendarRange size={12} />
          {t("timeRange.custom")}
        </button>
      </div>
      {custom ? (
        <div className="time-range-custom">
          <label>
            <span>{t("timeRange.from")}</span>
            <input
              type="datetime-local"
              step={1}
              aria-label={t("timeRange.from")}
              value={range.draftSince}
              onChange={(e) => range.setDraft({ since: e.target.value })}
            />
          </label>
          <label>
            <span>{t("timeRange.to")}</span>
            <input
              type="datetime-local"
              step={1}
              aria-label={t("timeRange.to")}
              value={range.draftUntil}
              onChange={(e) => range.setDraft({ until: e.target.value })}
            />
          </label>
          {!range.draftValid ? (
            <span className="time-range-invalid">{t("timeRange.invalid")}</span>
          ) : null}
        </div>
      ) : null}
      <div className="time-range-meta">
        <span className="time-range-caption">
          {describeRange(range.since, range.until, t)}
        </span>
        <Button
          variant="quiet"
          icon={<RefreshCw size={13} />}
          onClick={() => {
            range.refresh();
            onRefresh?.();
          }}
        >
          {t("timeRange.reanchor")}
        </Button>
      </div>
    </div>
  );
}

/**
 * Time range whose selection survives a reload: the preset id (and, for the
 * custom window, both absolute bounds) live in the URL next to the other
 * filters, so a link to "the last 15 minutes of failures" reproduces it.
 */
export function useUrlTimeRange(
  params: URLSearchParams,
  setParams: (
    next: URLSearchParams,
    options?: { replace?: boolean },
  ) => void,
  defaultPreset = "24h",
): TimeRangeController {
  // The seed is read once: a later navigation must not yank the picker back.
  const seedRef = useRef<{ preset: string; seed?: { since?: string; until?: string } } | null>(null);
  if (seedRef.current == null) {
    const raw = (params.get("range") ?? "").trim();
    const known = raw === CUSTOM_PRESET || TIME_PRESETS.some((p) => p.id === raw);
    seedRef.current = {
      preset: known ? raw : defaultPreset,
      seed: {
        since: params.get("from") || undefined,
        until: params.get("to") || undefined,
      },
    };
  }
  const range = useTimeRange(seedRef.current.preset, seedRef.current.seed);

  useEffect(() => {
    const wantRange = range.preset === defaultPreset ? "" : range.preset;
    const wantFrom = range.preset === CUSTOM_PRESET ? range.draftSince : "";
    const wantTo = range.preset === CUSTOM_PRESET ? range.draftUntil : "";
    if (
      (params.get("range") ?? "") === wantRange &&
      (params.get("from") ?? "") === wantFrom &&
      (params.get("to") ?? "") === wantTo
    ) {
      return;
    }
    const next = new URLSearchParams(params);
    for (const [key, value] of [
      ["range", wantRange],
      ["from", wantFrom],
      ["to", wantTo],
    ] as const) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    setParams(next, { replace: true });
  }, [range.preset, range.draftSince, range.draftUntil, params, setParams, defaultPreset]);

  return range;
}
