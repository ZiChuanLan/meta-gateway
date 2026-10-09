import { useId, type InputHTMLAttributes, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { useI18n } from "../../i18n";
import { InfoTip } from "../../components/ui";

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
 * caller compares against the deployment default — the resulting badge. It sits
 * on the console's `.field-label`, so a settings row's label is the same object
 * as every other form label in the app.
 *
 * A measurement the label already names in brackets moves out of the name to
 * its right ("探测间隔（秒）" reads "探测间隔 秒"), where it annotates the value
 * instead of lengthening the word.
 */
export function SettingLabel({
  label,
  changed,
  tip,
}: {
  label: string;
  /** Omit for labels that do not map to a single setting (sub-labels). */
  changed?: boolean;
  /** Long explanation, shown behind the (i) in this row's label line. */
  tip?: string;
}) {
  const { name, unit } = splitUnit(label);
  return (
    <span className="field-label runtime-row-label">
      <span>{name}</span>
      {unit ? <span className="runtime-row-unit">{unit}</span> : null}
      {/* Only "changed" gets a badge. A chip on every row that gives the same
          answer is noise, and on a page with instant save the one answer worth a
          mark is "I overrode the default". */}
      {changed ? <SettingState state="changed" /> : null}
      {tip ? <InfoTip label={tip} /> : null}
    </span>
  );
}

/**
 * The measurement a label already names in brackets: "探测间隔（秒）" is a
 * field that reads `探测间隔 秒` over its own control, so the unit moves out of
 * the name and to its right, where it annotates the value. Only
 * measurement-shaped brackets are lifted; "(1/N)" or "(成功请求数)" are part of
 * the name and stay put.
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
 * One settings row: a real `.field`, the console's stacked label-over-control
 * column, laid out inside `.form-grid`. A settings page is therefore the same
 * object as every other form in the app — two columns, each label directly above
 * the control it names — instead of labels pinned left and controls drifting to
 * the far edge of a wide line.
 *
 * It stays a `<label>`, so click-to-focus and the accessible name keep working
 * for the toggle rows, where the control is the row's only child.
 */
export function RuntimeRow({
  label,
  hint,
  changed,
  wide = false,
  children,
}: {
  label: string;
  hint: string;
  /** Omit for rows that do not map to a single setting. */
  changed?: boolean;
  /** Spans both columns: textareas, cron and time pickers. */
  wide?: boolean;
  children: ReactNode;
}) {
  // Every hint rides in the (i) beside the label — same rule as the shared Field
  // (see its comment): a settings page where some rows explain themselves inline
  // and others behind the (i) reads as two different kinds of row. The tip carries
  // the text as its accessible name, so nothing is lost by moving it there, and
  // there is no inline node left for `aria-describedby` to point at.
  return (
    <div className={`field runtime-row${wide ? " wide" : ""}`}>
      {/* The label wraps the control so clicking its name still focuses the field. */}
      <label className="runtime-row-label-wrap">
        <SettingLabel label={label} changed={changed} tip={hint} />
        {children}
      </label>
    </div>
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
  // A control can be described by more than one thing: its row's hint and its own
  // range violation. Stating `aria-describedby` here without merging would drop
  // whichever the caller passed.
  const describedBy =
    [props["aria-describedby"], error ? errorId : null].filter(Boolean).join(" ") || undefined;

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
        aria-describedby={describedBy}
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
