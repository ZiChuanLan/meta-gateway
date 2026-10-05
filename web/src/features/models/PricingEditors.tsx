import { Plus, Trash2 } from "lucide-react";
import { InfoTip, Button } from "../../components/ui";
import { useI18n } from "../../i18n";
import type { PriceTier, PriceWindow } from "../../api/types";

/**
 * Editors for the two pricing features that are not a single number: the
 * context-length ladder and the time-of-day windows.
 *
 * Both are shared by the model-metadata dialog and the route-member dialog,
 * because both price layers carry the same two fields — one editor means one
 * place that writes the JSON, so the two layers cannot drift into different
 * shapes.
 *
 * The API carries them as JSON text (the convention mapping_json and
 * payload_rules already follow), so this module owns the conversion in both
 * directions and neither dialog has to think about it.
 */

/** ISO weekday numbers with their translation key, Monday first. */
const WEEKDAYS: Array<{ day: number; key: string }> = [
  { day: 1, key: "pricing.mon" },
  { day: 2, key: "pricing.tue" },
  { day: 3, key: "pricing.wed" },
  { day: 4, key: "pricing.thu" },
  { day: 5, key: "pricing.fri" },
  { day: 6, key: "pricing.sat" },
  { day: 7, key: "pricing.sun" },
];

/**
 * Reads a stored ladder. Malformed text yields an empty ladder rather than
 * throwing: the dialog is an editor, and an editor that cannot open the value
 * it is supposed to fix is useless.
 */
export function parseTiers(raw?: string): PriceTier[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.map((entry) => normalizeTier(entry as Partial<PriceTier>));
  } catch {
    return [];
  }
}

export function parseWindows(raw?: string): PriceWindow[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.map((entry) => normalizeWindow(entry as Partial<PriceWindow>));
  } catch {
    return [];
  }
}

/** Invalid editing values remain invalid until explicitly corrected, never coerced to free. */
export const pricingNumber = (raw: string): number => raw.trim() === "" ? NaN : Number(raw);
export const blankTier = (tier: PriceTier) => [tier.max_prompt_tokens,tier.prompt,tier.completion,tier.cache,tier.per_request].every(Number.isNaN);
export const blankWindow = (window: PriceWindow) => window.days.length === 0 && [window.from_hour,window.to_hour,window.multiplier].every(Number.isNaN);
export function validTiers(tiers: PriceTier[]): boolean {
  const ceilings = new Set<number>();
  return tiers.length <= 20 && tiers.filter((tier)=>!blankTier(tier)).every((tier) => {
    if (!Number.isInteger(tier.max_prompt_tokens) || tier.max_prompt_tokens < 0 || ceilings.has(tier.max_prompt_tokens)) return false;
    ceilings.add(tier.max_prompt_tokens);
    return [tier.prompt, tier.completion, tier.cache, tier.per_request].every((n) => Number.isFinite(n) && n >= 0);
  });
}
export function validWindows(windows: PriceWindow[]): boolean {
  return windows.length <= 20 && windows.filter((window)=>!blankWindow(window)).every((window) =>
    [window.from_hour, window.to_hour].every((n) => Number.isInteger(n) && n >= 0 && n <= 23) &&
    Number.isFinite(window.multiplier) && window.multiplier > 0 && window.multiplier <= 1000 &&
    window.days.every((n) => Number.isInteger(n) && n >= 1 && n <= 7));
}

/** Renders a ladder for storage; an empty ladder clears the column. */
export function encodeTiers(tiers: PriceTier[]): string {
  if (!validTiers(tiers)) throw new Error("Invalid price tiers");
  const configured = tiers.filter((tier)=>!blankTier(tier));
  return configured.length === 0 ? "" : JSON.stringify(configured);
}

export function encodeWindows(windows: PriceWindow[]): string {
  if (!validWindows(windows)) throw new Error("Invalid price windows");
  const configured = windows.filter((window)=>!blankWindow(window));
  return configured.length === 0 ? "" : JSON.stringify(configured);
}

function normalizeTier(raw: Partial<PriceTier>): PriceTier {
  const number = (value: unknown) => (typeof value === "number" && Number.isFinite(value) ? value : 0);
  return {
    max_prompt_tokens: number(raw.max_prompt_tokens),
    prompt: number(raw.prompt),
    completion: number(raw.completion),
    cache: number(raw.cache),
    per_request: number(raw.per_request),
  };
}

function normalizeWindow(raw: Partial<PriceWindow>): PriceWindow {
  const hour = (value: unknown) =>
    typeof value === "number" && Number.isFinite(value) ? Math.min(23, Math.max(0, Math.trunc(value))) : 0;
  const days = Array.isArray(raw.days)
    ? raw.days.filter((day): day is number => typeof day === "number" && day >= 1 && day <= 7)
    : [];
  return {
    days: [...new Set(days)].sort((a, b) => a - b),
    from_hour: hour(raw.from_hour),
    to_hour: hour(raw.to_hour),
    multiplier: typeof raw.multiplier === "number" && raw.multiplier > 0 ? raw.multiplier : 1,
  };
}

const EMPTY_TIER: PriceTier = { max_prompt_tokens: NaN, prompt: NaN, completion: NaN, cache: NaN, per_request: NaN };
const EMPTY_WINDOW: PriceWindow = { days: [], from_hour: NaN, to_hour: NaN, multiplier: NaN };

export function PriceTiersEditor({
  value,
  onChange,
  disabled,
}: {
  value: PriceTier[];
  onChange: (next: PriceTier[]) => void;
  disabled?: boolean;
}) {
  const { t } = useI18n();
  const patch = (index: number, partial: Partial<PriceTier>) =>
    onChange(value.map((tier, i) => (i === index ? { ...tier, ...partial } : tier)));
  return (
    <fieldset className="pricing-editor">
      <legend>
        {t("pricing.tiers")} <InfoTip label={t("pricing.tiersHint")} />
      </legend>
      <p className="field-hint">{t("pricing.blankRules")}</p>
      {!validTiers(value) ? <p role="alert">{t("pricing.invalidTiers")}</p> : null}
      {value.map((tier, index) => (
        <div className="pricing-row pricing-row-tier" key={index}>
          <label>
            <span>{t("pricing.tierCeiling")}</span>
            <input
              type="number"
              min={0}
              step={1}
              value={Number.isFinite(tier.max_prompt_tokens) ? tier.max_prompt_tokens : ""}
              placeholder={t("pricing.tierOpenEnded")}
              disabled={disabled}
              onChange={(event) =>
                patch(index, { max_prompt_tokens: pricingNumber(event.target.value) })
              }
            />
          </label>
          <label>
            <span>{t("pricing.tierPrompt")}</span>
            <input
              type="number"
              min={0}
              step="any"
              value={Number.isFinite(tier.prompt) ? tier.prompt : ""}
              disabled={disabled}
              onChange={(event) => patch(index, { prompt: pricingNumber(event.target.value) })}
            />
          </label>
          <label>
            <span>{t("pricing.tierCompletion")}</span>
            <input
              type="number"
              min={0}
              step="any"
              value={Number.isFinite(tier.completion) ? tier.completion : ""}
              disabled={disabled}
              onChange={(event) =>
                patch(index, { completion: pricingNumber(event.target.value) })
              }
            />
          </label>
          <label>
            <span>{t("pricing.tierCache")}</span>
            <input
              type="number"
              min={0}
              step="any"
              value={Number.isFinite(tier.cache) ? tier.cache : ""}
              disabled={disabled}
              onChange={(event) => patch(index, { cache: pricingNumber(event.target.value) })}
            />
          </label>
          <label>
            <span>{t("pricing.tierPerRequest")}</span>
            <input
              type="number"
              min={0}
              step="any"
              value={Number.isFinite(tier.per_request) ? tier.per_request : ""}
              disabled={disabled}
              onChange={(event) =>
                patch(index, { per_request: pricingNumber(event.target.value) })
              }
            />
          </label>
          <button
            type="button"
            className="button button-quiet pricing-remove"
            aria-label={t("pricing.removeTier")}
            title={t("pricing.removeTier")}
            disabled={disabled}
            onClick={() => onChange(value.filter((_, i) => i !== index))}
          >
            <Trash2 size={14} />
          </button>
        </div>
      ))}
      <Button
        type="button"
        variant="quiet"
        icon={<Plus size={14} />}
        disabled={disabled || value.length >= 20}
        onClick={() => onChange([...value, { ...EMPTY_TIER }])}
      >
        {t("pricing.addTier")}
      </Button>
    </fieldset>
  );
}

export function PriceWindowsEditor({
  value,
  onChange,
  disabled,
}: {
  value: PriceWindow[];
  onChange: (next: PriceWindow[]) => void;
  disabled?: boolean;
}) {
  const { t } = useI18n();
  const patch = (index: number, partial: Partial<PriceWindow>) =>
    onChange(value.map((window, i) => (i === index ? { ...window, ...partial } : window)));
  const toggleDay = (index: number, day: number) => {
    const window = value[index];
    if (!window) return;
    const days = window.days.includes(day)
      ? window.days.filter((value) => value !== day)
      : [...window.days, day].sort((a, b) => a - b);
    patch(index, { days });
  };
  return (
    <fieldset className="pricing-editor">
      <legend>
        {t("pricing.schedule")} <InfoTip label={t("pricing.scheduleHint")} />
      </legend>
      <p className="field-hint">{t("pricing.blankRules")}</p>
      <p className="field-hint">{t("pricing.windowSemantics")}</p>
      {!validWindows(value) ? <p role="alert">{t("pricing.invalidWindows")}</p> : null}
      {value.map((window, index) => (
        <div className="pricing-window" key={index}>
          <div className="pricing-row pricing-row-window">
            <label>
              <span>{t("pricing.windowFrom")}</span>
              <input
                type="number"
                min={0}
                max={23}
                value={Number.isFinite(window.from_hour) ? window.from_hour : ""}
                disabled={disabled}
                onChange={(event) =>
                  patch(index, { from_hour: pricingNumber(event.target.value) })
                }
              />
            </label>
            <label>
              <span>{t("pricing.windowTo")}</span>
              <input
                type="number"
                min={0}
                max={23}
                value={Number.isFinite(window.to_hour) ? window.to_hour : ""}
                disabled={disabled}
                onChange={(event) =>
                  patch(index, { to_hour: pricingNumber(event.target.value) })
                }
              />
            </label>
            <label>
              <span>{t("pricing.windowMultiplier")}</span>
              <input
                type="number"
                min={0.000001}
                max={1000}
                step="any"
                value={Number.isFinite(window.multiplier) ? window.multiplier : ""}
                disabled={disabled}
                onChange={(event) =>
                  patch(index, { multiplier: pricingNumber(event.target.value) })
                }
              />
            </label>
            <button
              type="button"
              className="button button-quiet pricing-remove"
              aria-label={t("pricing.removeWindow")}
              title={t("pricing.removeWindow")}
              disabled={disabled}
              onClick={() => onChange(value.filter((_, i) => i !== index))}
            >
              <Trash2 size={14} />
            </button>
          </div>
          <div className="pricing-days">
            <span>{t("pricing.windowDays")}</span>
            {WEEKDAYS.map(({ day, key }) => (
              <button
                type="button"
                key={day}
                className="pricing-day"
                aria-pressed={window.days.includes(day)}
                disabled={disabled}
                onClick={() => toggleDay(index, day)}
              >
                {t(key)}
              </button>
            ))}
            <small className="pricing-days-note">
              {window.days.length === 0 ? t("pricing.everyDay") : ""}
            </small>
          </div>
          {window.from_hour === window.to_hour ? (
            <small className="pricing-days-note">{t("pricing.allDay")}</small>
          ) : null}
        </div>
      ))}
      <Button
        type="button"
        variant="quiet"
        icon={<Plus size={14} />}
        disabled={disabled || value.length >= 20}
        onClick={() => onChange([...value, { ...EMPTY_WINDOW, days: [] }])}
      >
        {t("pricing.addWindow")}
      </Button>
    </fieldset>
  );
}
