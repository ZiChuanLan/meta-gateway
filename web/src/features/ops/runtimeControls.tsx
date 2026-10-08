import { useId, type InputHTMLAttributes, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { InfoTip } from "../../components/ui";
import { useI18n } from "../../i18n";

// The presentational controls the runtime settings page is built from. They were
// the first ~150 lines of RuntimeSettingsPanel.tsx, which is otherwise a single
// 1,400-line component: the page keeps its state, the controls keep their looks,
// and nothing here reads or writes settings.

/**
 * The "已改 / 默认" badge. Instant save means there is no Save button to
 * compare against, so every field states whether it overrides the deployment
 * default it inherited — otherwise "I changed this" and "this is what the
 * environment already said" look identical on screen.
 */
export function SettingState({ state }: { state: "changed" | "default" }) {
  const { t } = useI18n();
  return (
    <span className={`setting-state is-${state}`}>
      {t(state === "changed" ? "ops.runtime.changed" : "ops.runtime.isDefault")}
    </span>
  );
}

/**
 * One labelled setting: the label, its hint as an InfoTip, and — when the
 * caller compares against the deployment default — the resulting badge.
 */
export function SettingLabel({
  label,
  hint,
  changed,
}: {
  label: string;
  hint: string;
  /** Omit for labels that do not map to a single setting (sub-labels). */
  changed?: boolean;
}) {
  return (
    <span className="setting-label">
      <span>{label}</span>
      <InfoTip label={hint} />
      {changed === undefined ? null : <SettingState state={changed ? "changed" : "default"} />}
    </span>
  );
}

/**
 * The measurement a label already names in brackets: "探测间隔（秒）" is a
 * panel that reads `探测间隔   60 秒`, so the unit moves out of the label and
 * beside the value, where a meter belongs. Only measurement-shaped brackets are
 * lifted; "(1/N)" or "(成功请求数)" are part of the name and stay put.
 */
const UNIT_PATTERN =
  /^(秒|毫秒|分钟|小时|天|次|个|条|行|s|ms|m|h|d|min|sec|hrs?|seconds?|milliseconds?|minutes?|hours?|days?|times?|rows?|items?|entries?)$/i;

export function splitUnit(label: string): { name: string; unit: string } {
  const match = /^(.*?)\s*[（(]([^（）()]{1,12})[)）]\s*$/.exec(label);
  if (!match) return { name: label, unit: "" };
  const unit = match[2]!.trim();
  if (!UNIT_PATTERN.test(unit)) return { name: label, unit: "" };
  return { name: match[1]!.trim(), unit };
}

/**
 * One settings row. The page is a continuous surface with hairline separators,
 * so this — not a card grid — is the repeating unit: label on the left, control
 * on the right, one line tall, the unit riding next to the value.
 *
 * The row carries its own class instead of reusing `.field`: `.field` is a
 * generic stacked column (`display: flex; flex-direction: column`) and winning
 * that declaration back per row is exactly how the previous version ended up
 * with the label stacked above its control.
 *
 * It is a `<label>`, so click-to-focus and the accessible name keep working for
 * the toggle rows, where the control is the row's only child.
 */
export function RuntimeRow({
  label,
  hint,
  changed,
  wide = false,
  text = false,
  children,
}: {
  label: string;
  hint: string;
  /** Omit for rows that do not map to a single setting. */
  changed?: boolean;
  /** Label above, control below at full width: textareas, cron and time pickers. */
  wide?: boolean;
  /** One line, but the control takes the room left over: URL and text inputs. */
  text?: boolean;
  children: ReactNode;
}) {
  const { name, unit } = splitUnit(label);
  return (
    <label className={`runtime-row${wide ? " is-wide" : ""}${text ? " is-text" : ""}`}>
      <SettingLabel label={name} hint={hint} changed={changed} />
      <span className="runtime-row-leader" aria-hidden="true" />
      <span className="runtime-row-value">
        {children}
        {unit ? <span className="runtime-row-unit">{unit}</span> : null}
      </span>
    </label>
  );
}

/** The validation message for a number field, or the operator's own wording. */
export function numberValidationError(
  value: number,
  min: number | undefined,
  max: number | undefined,
  customError: string | undefined,
  t: (key: string, vars?: Record<string, string | number>) => string,
) {
  if (!Number.isFinite(value)) return t("ops.runtime.validation.number");
  if (!Number.isInteger(value)) return t("ops.runtime.validation.integer");
  if (min !== undefined && value < min) {
    return max !== undefined
      ? t("ops.runtime.validation.between", { min, max })
      : t("ops.runtime.validation.min", { min });
  }
  if (max !== undefined && value > max) {
    return min !== undefined
      ? t("ops.runtime.validation.between", { min, max })
      : t("ops.runtime.validation.max", { max });
  }
  return customError;
}

type ValidatedNumberInputProps = Omit<
  InputHTMLAttributes<HTMLInputElement>,
  "max" | "min" | "value"
> & {
  min?: number;
  max?: number;
  value: number;
  customError?: string;
};

/**
 * A number input that reports its own range violation instead of silently
 * accepting it, so a bad value is visible before Save rather than answered by
 * the server.
 */
export function ValidatedNumberInput({
  min,
  max,
  customError,
  disabled,
  value,
  ...props
}: ValidatedNumberInputProps) {
  const { t } = useI18n();
  const errorId = useId();
  const numericValue = Number(value);
  const error =
    disabled || !Number.isFinite(numericValue)
      ? undefined
      : numberValidationError(numericValue, min, max, customError, t);

  return (
    <span className={`setting-input-wrap${error ? " is-invalid" : ""}`}>
      <input
        {...props}
        type="number"
        min={min}
        max={max}
        step={1}
        disabled={disabled}
        value={value}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? errorId : undefined}
      />
      {error ? (
        <span id={errorId} className="setting-validation" role="alert">
          {error}
        </span>
      ) : null}
    </span>
  );
}

/**
 * A folded block whose header is the toggle. The runtime page now selects one
 * section from the index at a time, so the only thing left that needs to start
 * folded is the danger zone: an irreversible wipe should take a second,
 * deliberate click to reveal, not a single nav click.
 */
export function CollapsibleGroup({
  id,
  title,
  description,
  open,
  onToggle,
  children,
  danger = false,
}: {
  id: string;
  title: string;
  description: string;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
  danger?: boolean;
}) {
  return (
    <section
      className={`runtime-fold${open ? " is-open" : ""}${danger ? " is-danger" : ""}`}
      id={id}
    >
      <button
        type="button"
        className="runtime-fold-toggle"
        aria-expanded={open}
        aria-controls={`${id}-body`}
        onClick={onToggle}
      >
        <span className="runtime-fold-chevron" aria-hidden="true">
          <ChevronRight size={15} />
        </span>
        <span className="runtime-fold-copy">
          <span className="runtime-fold-title">{title}</span>
          <span className="runtime-fold-desc">{description}</span>
        </span>
      </button>
      {open ? (
        <div className="runtime-fold-body" id={`${id}-body`}>
          {children}
        </div>
      ) : null}
    </section>
  );
}
