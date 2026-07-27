import { ChevronDown, MoreHorizontal } from "lucide-react";
import {
	useEffect,
	useId,
	useLayoutEffect,
	useRef,
	useState,
	type CSSProperties,
	type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { Button } from "./ui";

export type ActionMenuItem = {
	key: string;
	label: string;
	icon?: ReactNode;
	danger?: boolean;
	disabled?: boolean;
	onSelect: () => void;
};

type MenuPosition = { top: number; left: number };

/**
 * Secondary actions menu.
 * Panel always portals to document.body with position:fixed so table/panel
 * overflow containers cannot clip it (New API style row ⋯ menus).
 */
export function ActionMenu({
	label,
	disabled,
	items,
	compact = false,
	open: openControlled,
	onOpenChange,
	position,
}: {
	label: string;
	disabled?: boolean;
	items: ActionMenuItem[];
	/** Icon-only ⋯ trigger for dense table rows. */
	compact?: boolean;
	open?: boolean;
	onOpenChange?: (open: boolean) => void;
	/** When set with open, render as a fixed context menu (no trigger required). */
	position?: MenuPosition;
}) {
	const [openUncontrolled, setOpenUncontrolled] = useState(false);
	const open = openControlled ?? openUncontrolled;
	const setOpen = (next: boolean) => {
		onOpenChange?.(next);
		if (openControlled === undefined) setOpenUncontrolled(next);
	};

	const rootRef = useRef<HTMLDivElement | null>(null);
	const triggerRef = useRef<HTMLButtonElement | null>(null);
	const panelRef = useRef<HTMLDivElement | null>(null);
	const menuId = useId();
	const isContext = Boolean(position);
	const [panelStyle, setPanelStyle] = useState<CSSProperties | undefined>(
		undefined,
	);

	const close = () => {
		onOpenChange?.(false);
		if (openControlled === undefined) setOpenUncontrolled(false);
	};

	useEffect(() => {
		if (!open) return;
		const onPointer = (event: MouseEvent) => {
			const target = event.target as Node;
			if (rootRef.current?.contains(target)) return;
			if (panelRef.current?.contains(target)) return;
			close();
		};
		const onKey = (event: KeyboardEvent) => {
			if (event.key === "Escape") close();
		};
		// Scroll/resize repositions dropdown; context menu just closes.
		const onViewportChange = () => {
			if (isContext) {
				close();
				return;
			}
			// Force layout effect to re-run by toggling style via rAF measure.
			measureAndPlace();
		};
		window.addEventListener("mousedown", onPointer);
		window.addEventListener("keydown", onKey);
		window.addEventListener("scroll", onViewportChange, true);
		window.addEventListener("resize", onViewportChange);
		return () => {
			window.removeEventListener("mousedown", onPointer);
			window.removeEventListener("keydown", onKey);
			window.removeEventListener("scroll", onViewportChange, true);
			window.removeEventListener("resize", onViewportChange);
		};
		// eslint-disable-next-line react-hooks/exhaustive-deps -- close/measure stable enough for open lifecycle
	}, [open, isContext, onOpenChange, openControlled]);

	function measureAndPlace() {
		const pad = 8;
		const panel = panelRef.current;
		if (!panel) return;

		const panelRect = panel.getBoundingClientRect();
		const panelWidth = panelRect.width || 200;
		const panelHeight = panelRect.height || 240;

		if (isContext && position) {
			let top = position.top;
			let left = position.left;
			if (top + panelHeight > window.innerHeight - pad) {
				top = Math.max(pad, window.innerHeight - panelHeight - pad);
			}
			if (left + panelWidth > window.innerWidth - pad) {
				left = Math.max(pad, window.innerWidth - panelWidth - pad);
			}
			setPanelStyle({ top, left });
			return;
		}

		const trigger =
			triggerRef.current ??
			(rootRef.current?.querySelector("button") as HTMLElement | null);
		if (!trigger) return;
		const triggerRect = trigger.getBoundingClientRect();

		// Prefer below-right aligned to trigger; flip above / left if needed.
		let top = triggerRect.bottom + 6;
		let left = triggerRect.right - panelWidth;
		if (top + panelHeight > window.innerHeight - pad) {
			top = Math.max(pad, triggerRect.top - panelHeight - 6);
		}
		if (left < pad) {
			left = Math.min(
				triggerRect.left,
				window.innerWidth - panelWidth - pad,
			);
		}
		if (left + panelWidth > window.innerWidth - pad) {
			left = Math.max(pad, window.innerWidth - panelWidth - pad);
		}
		if (top < pad) top = pad;
		setPanelStyle({ top, left });
	}

	useLayoutEffect(() => {
		if (!open) {
			setPanelStyle(undefined);
			return;
		}
		// First paint may have zero size; measure twice for accurate height.
		measureAndPlace();
		const frame = window.requestAnimationFrame(() => measureAndPlace());
		return () => window.cancelAnimationFrame(frame);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [open, position?.top, position?.left, items.length, isContext]);

	const panel =
		open && typeof document !== "undefined" ? (
			createPortal(
				<div
					ref={panelRef}
					id={menuId}
					className={
						isContext
							? "action-menu-panel action-menu-panel-floating action-menu-panel-context"
							: "action-menu-panel action-menu-panel-floating"
					}
					role="menu"
					style={panelStyle}
				>
					{items.map((item) => (
						<button
							key={item.key}
							type="button"
							role="menuitem"
							disabled={item.disabled}
							className={
								item.danger
									? "action-menu-item is-danger"
									: "action-menu-item"
							}
							onClick={() => {
								setOpen(false);
								item.onSelect();
							}}
						>
							{item.icon}
							<span>{item.label}</span>
						</button>
					))}
				</div>,
				document.body,
			)
		) : null;

	// Context-only mode: no trigger.
	if (isContext) {
		if (!open) return null;
		return panel;
	}

	return (
		<div className="action-menu" ref={rootRef}>
			{compact ? (
				<button
					ref={triggerRef}
					type="button"
					className="icon-button action-menu-trigger"
					aria-label={label}
					title={label}
					aria-expanded={open}
					aria-haspopup="menu"
					aria-controls={open ? menuId : undefined}
					disabled={disabled}
					onClick={(event) => {
						event.stopPropagation();
						setOpen(!open);
					}}
				>
					<MoreHorizontal size={16} />
				</button>
			) : (
				<Button
					variant="secondary"
					disabled={disabled}
					aria-expanded={open}
					aria-haspopup="menu"
					aria-controls={open ? menuId : undefined}
					icon={<MoreHorizontal size={14} />}
					onClick={(event) => {
						event.stopPropagation();
						// Store the event target as measure fallback via root.
						setOpen(!open);
					}}
				>
					{label}
					<ChevronDown size={14} />
				</Button>
			)}
			{panel}
		</div>
	);
}
