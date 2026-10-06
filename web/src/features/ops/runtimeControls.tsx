import { useId, type InputHTMLAttributes, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { InfoTip } from "../../components/ui";
import { useI18n } from "../../i18n";

// The presentational controls the runtime settings page is built from. They were
// the first ~150 lines of RuntimeSettingsPanel.tsx, which is otherwise a single
// 1,400-line component: the page keeps its state, the controls keep their looks,
// and nothing here reads or writes settings.

/** One labelled setting: the label plus its hint as an InfoTip. */
export function SettingLabel({ label, hint }: { label: string; hint: string }) {
  return (
    <span className="setting-label">
      <span>{label}</span>
      <InfoTip label={hint} />
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

/** Masonry container: CSS columns balance the cards by height. */
export function RuntimeSettingsColumns({ children }: { children: ReactNode }) {
  return <div className="runtime-settings-grid">{children}</div>;
}

/**
 * One collapsible settings group. The page renders every group collapsed by
 * default — the old always-open layout was ~20 cards of form controls in one
 * scroll, and the section nav was the only way to make sense of it. A group's
 * header is the toggle: chevron, title, description, and how many cards live
 * inside, so a collapsed row still says what it is hiding. Which groups are
 * open is page state, not per-group state, so the section nav can open a group
 * from anywhere.
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
