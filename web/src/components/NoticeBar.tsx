import type { ReactNode } from "react";
import { Info, TriangleAlert, X } from "lucide-react";

/**
 * A single line of the operator's or the deployment's own voice, sitting above
 * the content it applies to.
 *
 * Two things wanted the same object: the operator announcing something to the
 * people using this gateway, and the console explaining a rule of its own
 * ("prices already include your group's multiplier"). Both are one sentence with
 * a tone, an optional way in, and an optional way to make it go away — so it is
 * one component rather than two bars that drift apart.
 *
 * Tone is carried by the icon and a tinted surface, never by a thick colored
 * edge: a 4px left border on a callout is costume, not hierarchy.
 */
export type NoticeTone = "info" | "warn";

export function NoticeBar({
  tone = "info",
  title,
  body,
  action,
  onDismiss,
  className,
}: {
  tone?: NoticeTone;
  title: ReactNode;
  body?: ReactNode;
  /** The way in: a link or button the reader can act on. */
  action?: ReactNode;
  /** Renders a dismiss control when given; the owner decides whether to remember it. */
  onDismiss?: () => void;
  className?: string;
}) {
  const Icon = tone === "warn" ? TriangleAlert : Info;
  return (
    <div className={`notice-bar is-${tone}${className ? ` ${className}` : ""}`} role="status">
      <span className="notice-bar-icon" aria-hidden="true">
        <Icon size={15} />
      </span>
      <div className="notice-bar-text">
        <strong>{title}</strong>
        {body ? <span>{body}</span> : null}
      </div>
      {action ? <div className="notice-bar-action">{action}</div> : null}
      {onDismiss ? (
        <button
          type="button"
          className="notice-bar-dismiss"
          aria-label="dismiss"
          onClick={onDismiss}
        >
          <X size={13} />
        </button>
      ) : null}
    </div>
  );
}
