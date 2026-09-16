import { X } from "lucide-react";
import { useId, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../i18n";
import { useModalFocus } from "./overlayFocus";
import { IconButton } from "./ui";

/**
 * Slide-in panel used for editing entities and nested management surfaces.
 * A right-side drawer is the default; callers can opt into a left-side,
 * offset drawer when they need to keep an editor visible beside it. Escape
 * only closes the top-most mounted drawer.
 */
export function Drawer({
	title,
	children,
	footer,
	onClose,
	width = 520,
	rightOffset,
	plain,
	side = "right",
	busy = false,
	className,
}: {
	title: string;
	children: ReactNode;
	footer?: ReactNode;
	onClose: () => void;
	width?: number;
	/** Right edge offset from the viewport (px) — for stacked drawers. */
	rightOffset?: number;
	/** Transparent backdrop: keep an already-open drawer behind interactive. */
	plain?: boolean;
	/** Which edge the drawer slides in from. Left drawers can stack beside
	 * a right-hand editor without covering it. */
	side?: "left" | "right";
	busy?: boolean;
	className?: string;
}) {
	const { t } = useI18n();
	const titleId = useId();
	const drawerRef = useRef<HTMLElement | null>(null);
	const close = () => { if (!busy) onClose(); };
	useModalFocus(drawerRef, close, !plain);

	return createPortal(
		<div
			className={`drawer-backdrop${plain ? " is-plain" : ""}${side === "left" ? " is-left" : ""}${className ? ` ${className}` : ""}`}
			style={rightOffset != null ? { right: rightOffset } : undefined}
			role="presentation"
			onMouseDown={(event) =>
				!plain && event.target === event.currentTarget && close()
			}
		>
			<aside
				ref={drawerRef}
				tabIndex={-1}
				className={`drawer${side === "left" ? " is-left" : ""}`}
				style={
					side === "left" && rightOffset != null
						? {
								width: `min(${width}px, 100vw)`,
								right: rightOffset,
								["--drawer-offset" as string]: `${rightOffset}px`,
							}
						: { width: `min(${width}px, 100vw)` }
				}
				role="dialog"
				aria-modal={!plain}
				aria-labelledby={titleId}
				aria-busy={busy || undefined}
			>
				<header>
					<h2 id={titleId}>{title}</h2>
					<IconButton label={t("common.close")} onClick={close} disabled={busy}>
						<X size={18} />
					</IconButton>
				</header>
				<div className="drawer-body"><fieldset className="overlay-fields" disabled={busy}>{children}</fieldset></div>
				{footer ? <footer className="drawer-footer"><fieldset className="overlay-actions" disabled={busy}>{footer}</fieldset></footer> : null}
			</aside>
		</div>,
		document.body,
	);
}
