import { Children, cloneElement, isValidElement, useEffect, useId, useRef, type ReactNode } from "react";
export function TeamModal({
  title,
  children,
  busy,
  onClose,
}: {
  title: string;
  children: ReactNode;
  busy?: boolean;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (el && !el.open) el.showModal();
    return () => {
      if (el?.open) el.close();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className="team-modal"
      aria-label={title}
      onCancel={(e) => {
        e.preventDefault();
        if (!busy) onClose();
      }}
    >
      <header>
        <h2>{title}</h2>
        <button
          type="button"
          disabled={busy}
          onClick={onClose}
          aria-label="Close / 关闭"
        >
          ×
        </button>
      </header>
      <fieldset className="team-modal-content" disabled={busy}>
        {children}
      </fieldset>
    </dialog>
  );
}
export function TeamField({
  label,
  hint,
  children,
}: {
  label: string;
  /** A short explanation under the control, for fields whose meaning is not
   *  obvious from the label (quota units, "0 means unlimited"). */
  hint?: string;
  children: ReactNode;
}) {
  const labelID = useId();
  return (
    <label className="team-field">
      <span id={labelID}>{label}</span>
      {Children.map(children, child =>
        isValidElement<{ "aria-labelledby"?: string }>(child) &&
        typeof child.type === "string" && ["input","select","textarea"].includes(child.type)
          ? cloneElement(child, { "aria-labelledby": labelID })
          : child
      )}
      {hint ? <small className="team-hint">{hint}</small> : null}
    </label>
  );
}
