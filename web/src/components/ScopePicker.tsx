import { Check, ChevronDown } from "lucide-react";
import {
	useEffect,
	useRef,
	useState,
	type CSSProperties,
} from "react";
import { useI18n } from "../i18n";

/**
 * Well-known downstream scopes, matching internal/auth.knownScopes.
 * "relay" is a superset: it grants every /v1 relay endpoint.
 */
export const SCOPE_IDS = [
	"relay",
	"models",
	"chat",
	"completions",
	"embeddings",
	"responses",
	"messages",
	"images",
	"audio",
	"moderations",
] as const;

export type ScopeId = (typeof SCOPE_IDS)[number];

/**
 * Dropdown multi-select for downstream key scopes. The trigger shows the
 * current selection (normalized to "relay" when empty); clicking opens a
 * fixed-position panel with one checkbox row per scope, each with a
 * plain-language explanation.
 */
export function ScopePicker({
	value,
	onChange,
	disabled,
}: {
	value: string[];
	onChange: (next: string[]) => void;
	disabled?: boolean;
}) {
	const { t } = useI18n();
	const [open, setOpen] = useState(false);
	const rootRef = useRef<HTMLDivElement>(null);
	const [panelStyle, setPanelStyle] = useState<CSSProperties | null>(null);
	const selectedSet = new Set(value);
	const display = value.length > 0 ? value.join(", ") : "relay";

	const openPanel = () => {
		const trigger = rootRef.current?.querySelector<HTMLElement>(
			".scope-picker-trigger",
		);
		if (!trigger) {
			setOpen(true);
			return;
		}
		const rect = trigger.getBoundingClientRect();
		setPanelStyle({
			position: "fixed",
			top: rect.bottom + 6,
			left: rect.left,
			width: Math.max(rect.width, 340),
			maxHeight: "min(320px, 60vh)",
		});
		setOpen(true);
	};

	useEffect(() => {
		if (!open) return;
		const onPointerDown = (event: MouseEvent) => {
			if (rootRef.current?.contains(event.target as Node)) return;
			setOpen(false);
		};
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") setOpen(false);
		};
		const onScrollOrResize = (event: Event) => {
			// Scrolls inside the open panel must not dismiss it (mouse wheel
			// over the option list otherwise closes the dropdown mid-selection).
			const target = event.target as Node | null;
			if (target && rootRef.current?.contains(target)) return;
			setOpen(false);
		};
		document.addEventListener("mousedown", onPointerDown);
		document.addEventListener("keydown", onKeyDown);
		window.addEventListener("scroll", onScrollOrResize, true);
		window.addEventListener("resize", onScrollOrResize);
		return () => {
			document.removeEventListener("mousedown", onPointerDown);
			document.removeEventListener("keydown", onKeyDown);
			window.removeEventListener("scroll", onScrollOrResize, true);
			window.removeEventListener("resize", onScrollOrResize);
		};
	}, [open]);

	const toggle = (scope: string) => {
		if (selectedSet.has(scope)) {
			onChange(value.filter((entry) => entry !== scope));
		} else {
			onChange([...value, scope]);
		}
	};

	return (
		<div ref={rootRef} className="scope-picker">
			<button
				type="button"
				className="scope-picker-trigger"
				disabled={disabled}
				onClick={() => (open ? setOpen(false) : openPanel())}
				aria-haspopup="listbox"
				aria-expanded={open}
			>
				<span className="mono truncate">{display}</span>
				<ChevronDown size={14} aria-hidden="true" />
			</button>
			{open && panelStyle ? (
				<div
					className="scope-picker-panel"
					style={panelStyle}
					role="listbox"
					aria-label={t("common.scopes")}
				>
					{SCOPE_IDS.map((scope) => {
						const checked = selectedSet.has(scope);
						return (
							<label
								key={scope}
								className={`scope-picker-item${scope === "relay" ? " is-relay" : ""}${checked ? " is-checked" : ""}`}
								role="option"
								aria-selected={checked}
							>
								<input
									type="checkbox"
									checked={checked}
									onChange={() => toggle(scope)}
								/>
								<span className="scope-picker-main">
									<strong className="mono">{scope}</strong>
									<small>{t(`keys.scope.${scope}`)}</small>
								</span>
								{checked ? (
									<Check size={14} className="scope-picker-check" aria-hidden="true" />
								) : null}
							</label>
						);
					})}
				</div>
			) : null}
		</div>
	);
}
