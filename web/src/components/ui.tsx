import { AlertTriangle, Info, LoaderCircle, X } from "lucide-react";
import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../i18n";
import { formatErrorObject } from "../formatError";
import { registerOverlay } from "./overlayStack";

export function Button({
  children,
  variant = "primary",
  icon,
  className,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "danger" | "quiet";
  icon?: ReactNode;
}) {
  return (
    <button
      className={[`button button-${variant}`, className]
        .filter(Boolean)
        .join(" ")}
      {...props}
    >
      {icon}
      {children}
    </button>
  );
}

export function IconButton({
  label,
  children,
  className,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      className={["icon-button", className].filter(Boolean).join(" ")}
      aria-label={label}
      title={label}
      {...props}
    >
      {children}
    </button>
  );
}

export function Page({
  title,
  description,
  actions,
  kicker,
  children,
}: {
  title: string;
  description: string;
  actions?: ReactNode;
  /** Small brand label above the title (ops console continuity). */
  kicker?: string;
  children: ReactNode;
}) {
  return (
    <main className="page">
      <header className="page-header">
        <div className="page-heading">
          {kicker ? <p className="page-kicker">{kicker}</p> : null}
          <h1>{title}</h1>
          <p>{description}</p>
        </div>
        {actions && <div className="toolbar">{actions}</div>}
      </header>
      {children}
    </main>
  );
}

export function Panel({
  title,
  titleHelp,
  actions,
  children,
  className = "",
  id,
}: {
  title?: string;
  titleHelp?: string;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  /** Optional DOM id (used by in-page section navigation). */
  id?: string;
}) {
  return (
    <section id={id} className={`panel ${className}`.trim()}>
      {(title || actions) && (
        <header className="panel-header">
          {title ? (
            <div className="panel-title">
              <h2>{title}</h2>
              {titleHelp ? <InfoTip label={titleHelp} /> : null}
            </div>
          ) : null}
          <div className="toolbar">{actions}</div>
        </header>
      )}
      {children}
    </section>
  );
}

export function Dialog({
  title,
  children,
  onClose,
  actions,
  danger = false,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  actions?: ReactNode;
  danger?: boolean;
}) {
  const { t } = useI18n();
  const dialogRef = useRef<HTMLElement | null>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  useEffect(() => {
    const node = dialogRef.current;
    const previous =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const focusables = () =>
      Array.from(
        node?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ) ?? [],
      );
    // Move focus into the dialog on open so keyboard users land inside.
    const first = focusables()[0];
    (first ?? node)?.focus();
    const onKeydown = (e: KeyboardEvent) => {
      if (e.key !== "Tab" || !node) return;
      const items = focusables();
      if (items.length === 0) return;
      const firstItem = items[0];
      const lastItem = items[items.length - 1];
      const active = document.activeElement;
      if (e.shiftKey && (active === firstItem || active === node)) {
        e.preventDefault();
        lastItem?.focus();
      } else if (!e.shiftKey && active === lastItem) {
        e.preventDefault();
        firstItem?.focus();
      }
    };
    const unregister = registerOverlay(() => onCloseRef.current());
    window.addEventListener("keydown", onKeydown);
    return () => {
      unregister();
      window.removeEventListener("keydown", onKeydown);
      previous?.focus();
    };
    // Mount-only: onClose is tracked via ref so re-renders do not steal focus.
  }, []);
  // Portal to document.body so fixed backdrop is not clipped by Panel/content
  // overflow:hidden or filtered/transformed ancestors (common after ops shell restyle).
  return createPortal(
    <div
      className="dialog-backdrop"
      role="presentation"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <section
        ref={dialogRef}
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="dialog-title"
      >
        <header>
          <div className={danger ? "danger-title" : ""}>
            {danger && <AlertTriangle size={18} />}
            <h2 id="dialog-title">{title}</h2>
          </div>
          <IconButton label={t("common.close")} onClick={onClose}>
            <X size={18} />
          </IconButton>
        </header>
        <div className="dialog-body">{children}</div>
        {actions && <footer>{actions}</footer>}
      </section>
    </div>,
    document.body,
  );
}

export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  pending,
  error,
  onConfirm,
  onClose,
}: {
  title: string;
  message: string;
  confirmLabel?: string;
  pending?: boolean;
  error?: unknown;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const { t } = useI18n();
  return (
    <Dialog
      title={title}
      onClose={onClose}
      danger
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="danger" disabled={pending} onClick={onConfirm}>
            {pending
              ? t("common.working")
              : (confirmLabel ?? t("common.delete"))}
          </Button>
        </>
      }
    >
      <p>{message}</p>
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}

type InfoTipPlacement = "above" | "below" | "left" | "right";

type InfoTipPosition = {
  top: number;
  left: number;
  placement: InfoTipPlacement;
};

export function InfoTip({ label }: { label: string }) {
  const triggerRef = useRef<HTMLSpanElement | null>(null);
  const bubbleRef = useRef<HTMLSpanElement | null>(null);
  const tooltipId = useId();
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [position, setPosition] = useState<InfoTipPosition | null>(null);
  const open = hovered || focused;

  const placeBubble = useCallback(() => {
    const trigger = triggerRef.current;
    const bubble = bubbleRef.current;
    if (!trigger || !bubble) return;

    const triggerRect = trigger.getBoundingClientRect();
    const bubbleRect = bubble.getBoundingClientRect();
    const gap = 8;
    const padding = 12;
    const centeredLeft = triggerRect.left + (triggerRect.width - bubbleRect.width) / 2;
    const centeredTop = triggerRect.top + (triggerRect.height - bubbleRect.height) / 2;
    const candidates: Array<InfoTipPosition> = [
      {
        placement: "right",
        top: centeredTop,
        left: triggerRect.right + gap,
      },
      {
        placement: "left",
        top: centeredTop,
        left: triggerRect.left - bubbleRect.width - gap,
      },
      {
        placement: "below",
        top: triggerRect.bottom + gap,
        left: centeredLeft,
      },
      {
        placement: "above",
        top: triggerRect.top - bubbleRect.height - gap,
        left: centeredLeft,
      },
    ];
    const fits = (candidate: InfoTipPosition) =>
      candidate.left >= padding &&
      candidate.top >= padding &&
      candidate.left + bubbleRect.width <= window.innerWidth - padding &&
      candidate.top + bubbleRect.height <= window.innerHeight - padding;
    const chosen = candidates.find(fits) ?? candidates[2]!;
    const left = Math.max(
      padding,
      Math.min(
        chosen.left,
        window.innerWidth - bubbleRect.width - padding,
      ),
    );
    const top = Math.max(
      padding,
      Math.min(
        chosen.top,
        window.innerHeight - bubbleRect.height - padding,
      ),
    );
    setPosition({ top, left, placement: chosen.placement });
  }, []);

  useLayoutEffect(() => {
    if (!open) {
      setPosition(null);
      return;
    }
    placeBubble();
    const frame = window.requestAnimationFrame(placeBubble);
    const reposition = () => placeBubble();
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    return () => {
      window.cancelAnimationFrame(frame);
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  }, [open, placeBubble]);

  useEffect(() => {
    if (!open) return;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      setHovered(false);
      setFocused(false);
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [open]);

  const bubble =
    open && typeof document !== "undefined"
      ? createPortal(
          <span
            ref={bubbleRef}
            id={tooltipId}
            className="info-tip-bubble"
            data-placement={position?.placement ?? "below"}
            data-visible={position ? "true" : "false"}
            style={
              position
                ? { top: position.top, left: position.left }
                : { top: 0, left: 0 }
            }
            role="tooltip"
          >
            {label}
          </span>,
          document.body,
        )
      : null;

  return (
    <>
      <span
        ref={triggerRef}
        className="info-tip"
        tabIndex={0}
        role="img"
        aria-label={label}
        aria-describedby={open ? tooltipId : undefined}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
      >
        <Info size={13} aria-hidden="true" />
      </span>
      {bubble}
    </>
  );
}

export function Field({
  label,
  children,
  hint,
  className,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
  className?: string;
}) {
  return (
    <label className={["field", className].filter(Boolean).join(" ")}>
      <span className="field-label">
        <span>{label}</span>
        {hint ? <InfoTip label={hint} /> : null}
      </span>
      {children}
    </label>
  );
}

export function StatusBadge({ value }: { value: string | boolean }) {
  const { status } = useI18n();
  const raw =
    typeof value === "boolean"
      ? value
        ? "enabled"
        : "disabled"
      : String(value);
  return (
    <span className={`badge badge-${raw.toLowerCase().replaceAll("_", "-")}`}>
      {status(value)}
    </span>
  );
}

export function Loading() {
  const { t } = useI18n();
  return (
    <div className="state">
      <LoaderCircle className="spin" size={20} />
      <span>{t("common.loading")}</span>
    </div>
  );
}

export function Empty({ children }: { children?: ReactNode }) {
  const { t } = useI18n();
  return (
    <div className="state state-empty">{children ?? t("common.empty")}</div>
  );
}

export function ErrorState({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  const { t } = useI18n();
  const formatted = formatErrorObject(error, t);
  const cause =
    formatted.cause.trim().toLocaleLowerCase() ===
    formatted.title.trim().toLocaleLowerCase()
      ? ""
      : formatted.cause;
  return (
    <div className="state state-error">
      <AlertTriangle size={18} />
      <div className="error-state-body">
        <strong>{formatted.title}</strong>
        {cause ? <span>{cause}</span> : null}
        {formatted.fix ? (
          <span className="error-state-fix">{formatted.fix}</span>
        ) : null}
      </div>
      {retry && (
        <Button variant="secondary" onClick={retry}>
          {t("common.retry")}
        </Button>
      )}
    </div>
  );
}

export function DataTable({
  headers,
  children,
  empty,
}: {
  headers: string[];
  children: ReactNode;
  empty?: boolean;
}) {
  if (empty) return <Empty />;
	return (
		<div className="table-wrap" data-columns={headers.length}>
      <table>
        <thead>
          <tr>
            {headers.map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

export function Tabs({
  items,
  active,
  onChange,
}: {
  items: Array<{ value: string; label: string; icon?: ReactNode }>;
  active: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="tabs" role="tablist">
      {items.map((item) => (
        <button
          role="tab"
          aria-selected={active === item.value}
          key={item.value}
          onClick={() => onChange(item.value)}
        >
          {item.icon}
          {item.label}
        </button>
      ))}
    </div>
  );
}

export function formatDate(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const lang =
    typeof document !== "undefined"
      ? document.documentElement.lang || undefined
      : undefined;
  return new Intl.DateTimeFormat(lang, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

export function formatBytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 ** 2).toFixed(1)} MB`;
}
