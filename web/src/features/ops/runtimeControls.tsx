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
 * section from the sidebar at a time, so the only thing left that needs to
 * start folded is the danger zone: an irreversible wipe should take a second,
 * deliberate click to reveal, not a single nav click.
 */
export function CollapsibleGroup({
  id,
  title,
  description,
  cardCount,
  open,
  onToggle,
  children,
  danger = false,
}: {
  id: string;
  title: string;
  description: string;
  cardCount?: number;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
  danger?: boolean;
}) {
  const { t } = useI18n();
  return (
    <section
      className={`runtime-group is-collapsible${open ? " is-open" : ""}${danger ? " is-danger" : ""}`}
      id={id}
    >
      <button
        type="button"
        className="runtime-group-toggle"
        aria-expanded={open}
        aria-controls={`${id}-body`}
        onClick={onToggle}
      >
        <span className="runtime-group-chevron" aria-hidden="true">
          <ChevronRight size={15} />
        </span>
        <span className="runtime-group-title">
          <strong>{title}</strong>
          <p>{description}</p>
        </span>
        {cardCount != null && cardCount > 0 ? (
          <span className="runtime-group-count">
            {t("ops.runtime.groupCount", { count: cardCount })}
          </span>
        ) : null}
      </button>
      {open ? (
        <div className="runtime-group-body" id={`${id}-body`}>
          {children}
        </div>
      ) : null}
    </section>
  );
}
