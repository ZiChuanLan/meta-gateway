import { X } from "lucide-react";
import { useEffect, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../i18n";
import { registerOverlay } from "./overlayStack";
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
}) {
	const { t } = useI18n();
	const drawerRef = useRef<HTMLElement | null>(null);
	const onCloseRef = useRef(onClose);
	onCloseRef.current = onClose;

	useEffect(() => {
		const node = drawerRef.current;
		// Same focus contract as Dialog: move focus in on open, trap Tab
		// inside, restore focus on close.
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
		// Lock body scroll while the drawer is open.
		const previousOverflow = document.body.style.overflow;
		document.body.style.overflow = "hidden";
		window.addEventListener("keydown", onKeydown);
		return () => {
			unregister();
			window.removeEventListener("keydown", onKeydown);
			document.body.style.overflow = previousOverflow;
			previous?.focus();
		};
	}, []);

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
