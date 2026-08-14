import { X } from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../i18n";
import { IconButton } from "./ui";

/**
 * Right-side slide-in panel used for editing entities (channels, etc.).
 * Keeps the master-detail context visible while the edit form is open —
 * closer to new-api's side editors than a centered modal.
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
	/** Which edge the drawer slides in from. Left drawers stack beside a
	 *  right-hand editor without covering it (model manager, key manager). */
	side?: "left" | "right";
}) {
	const { t } = useI18n();

	useEffect(() => {
		const close = (event: KeyboardEvent) => {
			if (event.key === "Escape") onClose();
		};
		window.addEventListener("keydown", close);
		// Lock body scroll while the drawer is open.
		const previous = document.body.style.overflow;
		document.body.style.overflow = "hidden";
		return () => {
			window.removeEventListener("keydown", close);
			document.body.style.overflow = previous;
		};
	}, [onClose]);

	return createPortal(
		<div
			className={`drawer-backdrop${plain ? " is-plain" : ""}${side === "left" ? " is-left" : ""}`}
			style={rightOffset != null ? { right: rightOffset } : undefined}
			role="presentation"
			onMouseDown={(event) =>
				!plain && event.target === event.currentTarget && onClose()
			}
		>
			<aside
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
				aria-modal="true"
				aria-labelledby="drawer-title"
			>
				<header>
					<h2 id="drawer-title">{title}</h2>
					<IconButton label={t("common.close")} onClick={onClose}>
						<X size={18} />
					</IconButton>
				</header>
				<div className="drawer-body">{children}</div>
				{footer ? <footer className="drawer-footer">{footer}</footer> : null}
			</aside>
		</div>,
		document.body,
	);
}
