import { Check, ChevronDown, Search } from "lucide-react";
import {
	useEffect,
	useId,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type CSSProperties,
} from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../i18n";
import { registerOverlay } from "./overlayStack";
import { focusAfterPopover } from "./overlayFocus";

export type SelectOption = {
	value: string;
	label: string;
	group?: string;
};

/**
 * Self-styled searchable dropdown with optional group headers and a
 * "custom" free-text escape hatch (used by the connection type picker).
 */
export function SearchableSelect({
	options,
	value,
	onChange,
	placeholder,
	allowCustom,
	disabled,
	groups,
	ariaLabel,
}: {
	options: SelectOption[];
	value: string;
	onChange: (value: string) => void;
	placeholder?: string;
	/** When true, selecting the "custom" sentinel reveals a free-text input. */
	allowCustom?: boolean;
	disabled?: boolean;
	/** Order of group ids to render, with a "*" for ungroupped options. */
	groups?: string[];
	/**
	 * Names the control. Without it the trigger reads as its current value,
	 * which is fine for a screen reader walking the options but useless for
	 * a labelled form field ("Backend · OpenAI" never says it picks a model).
	 */
	ariaLabel?: string;
}) {
	const { t } = useI18n();
	const listId = useId();
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [customMode, setCustomMode] = useState(false);
	const [customValue, setCustomValue] = useState("");
	// Decided once when the panel opens, not re-evaluated per keystroke: if
	// the search box disappeared the moment filtering dropped the list below
	// the threshold, typing two letters would unmount the input mid-composition
	// and strand the IME (the "typing Chinese freezes the type picker" bug).
	const [searchVisible, setSearchVisible] = useState(false);
	const rootRef = useRef<HTMLDivElement>(null);
	const triggerRef = useRef<HTMLButtonElement>(null);
	const panelRef = useRef<HTMLDivElement>(null);
	const searchRef = useRef<HTMLInputElement>(null);
	// The panel is fixed-positioned so scroll containers (e.g. Dialog) cannot clip it.
	const [panelStyle, setPanelStyle] = useState<CSSProperties | null>(null);

	// Guards against the panel re-opening right after a selection: keyboard
	// users often hit Enter/Space again (focus sits on the trigger), which
	// would otherwise re-open the dropdown immediately after it closes.
	const suppressOpenUntil = useRef(0);
	const openPanel = () => {
		if (disabled || triggerRef.current?.matches(":disabled")) return;
		if (Date.now() < suppressOpenUntil.current) return;
		setSearchVisible(options.length > 4);
		const trigger = rootRef.current?.querySelector<HTMLElement>(
			".searchable-select-trigger",
		);
		if (!trigger) {
			setOpen(true);
			return;
		}
		const rect = trigger.getBoundingClientRect();
		setPanelStyle({
			position: "fixed",
			top: rect.bottom + 8,
			left: rect.left,
			width: Math.max(rect.width, 240),
		});
		setOpen(true);
	};

	const placePanel = () => {
		const anchor = triggerRef.current?.getBoundingClientRect();
		if (!anchor || !panelRef.current) return;
		const viewportWidth = document.documentElement.clientWidth || window.innerWidth;
		const width = Math.min(Math.max(anchor.width, 240), viewportWidth - 16);
		const maxHeight = Math.min(400, window.innerHeight - 16);
		const height = Math.min(panelRef.current.scrollHeight || 300, maxHeight);
		let top = anchor.bottom + 8;
		if (top + height > window.innerHeight - 8) top = anchor.top - height - 8;
		setPanelStyle({ position: "fixed", width, maxHeight,
			left: Math.max(8, Math.min(anchor.left, viewportWidth - width - 8)),
			top: Math.max(8, Math.min(top, window.innerHeight - height - 8)),
		});
	};
	const placeRef = useRef(placePanel);
	placeRef.current = placePanel;
	useLayoutEffect(() => { if (open) placeRef.current(); }, [open, query, options.length]);

	const customSentinel = useMemo(
		() => options.find((option) => option.value === "__custom__"),
		[options],
	);
	const isCustom =
		customMode ||
		Boolean(customSentinel) &&
			!options.some(
				(option) =>
					option.value === value && option.value !== "__custom__",
			);

	useEffect(() => {
		if (!open) return;
		const unregister = registerOverlay(() => {
			setOpen(false);
			triggerRef.current?.focus({ preventScroll: true });
		}, { modal: false, transient: true, element: panelRef.current });
		const onPointerDown = (event: PointerEvent) => {
			if (rootRef.current?.contains(event.target as Node) || panelRef.current?.contains(event.target as Node)) return;
			setOpen(false);
		};
		const onScrollOrResize = (event: Event) => {
			// Scrolls inside the open panel must not dismiss it (mouse wheel over
			// the option list otherwise closes the dropdown mid-selection).
			const target = event.target as Node | null;
			if (target instanceof Node && (rootRef.current?.contains(target) || panelRef.current?.contains(target))) return;
			if (event.type === "resize") { placeRef.current(); return; }
			setOpen(false);
		};
		document.addEventListener("pointerdown", onPointerDown, true);
		window.addEventListener("scroll", onScrollOrResize, true);
		window.addEventListener("resize", onScrollOrResize);
		return () => {
			unregister();
			document.removeEventListener("pointerdown", onPointerDown, true);
			window.removeEventListener("scroll", onScrollOrResize, true);
			window.removeEventListener("resize", onScrollOrResize);
		};
	}, [open]);

	useEffect(() => {
		if (open) (searchRef.current ?? panelRef.current?.querySelector<HTMLElement>('[role="option"][aria-selected="true"], [role="option"]'))?.focus({ preventScroll: true });
	}, [open]);

	// Entering custom mode hands control to the free-text field.
	useEffect(() => {
		if (customMode) setOpen(false);
	}, [customMode]);

	const selectedLabel = options.find((option) => option.value === value)?.label;

	const filtered = useMemo(() => {
		const needle = query.trim().toLowerCase();
		return options.filter((option) => {
			if (option.value === "__custom__") return false;
			if (!needle) return true;
			return (
				option.label.toLowerCase().includes(needle) ||
				option.value.toLowerCase().includes(needle)
			);
		});
	}, [options, query]);

	const byGroup = useMemo(() => {
		const order = groups ?? [];
		const map = new Map<string, SelectOption[]>();
		for (const option of filtered) {
			const key = option.group ?? "*";
			if (!map.has(key)) map.set(key, []);
			map.get(key)!.push(option);
		}
		const keys = [...map.keys()].sort((a, b) => {
			const ai = order.indexOf(a);
			const bi = order.indexOf(b);
			const aRank = ai < 0 ? order.length : ai;
			const bRank = bi < 0 ? order.length : bi;
			return aRank - bRank || a.localeCompare(b);
		});
		return keys.map((key) => ({ key, options: map.get(key)! }));
	}, [filtered, groups]);

	const commit = (next: string) => {
		if (disabled || triggerRef.current?.matches(":disabled")) return;
		onChange(next);
		setOpen(false);
		setQuery("");
		suppressOpenUntil.current = Date.now() + 300;
		triggerRef.current?.focus({ preventScroll: true });
	};
	const enterCustom = () => {
		if (disabled || triggerRef.current?.matches(":disabled")) return;
		const next = query.trim() || (value === "__custom__" ? "" : value);
		setCustomValue(next);
		setCustomMode(true);
		setQuery("");
		onChange(next);
	};

	const triggerValue = isCustom
		? value || customValue || placeholder || ""
		: selectedLabel ?? value;

	return (
		<div className="searchable-select" ref={rootRef}>
			{!customMode ? (
				<button
					ref={triggerRef}
					type="button"
					className="searchable-select-trigger"
					disabled={disabled}
					onClick={() => (open ? setOpen(false) : openPanel())}
					onKeyDown={(event) => {
						if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); openPanel(); }
					}}
					aria-haspopup="listbox"
					aria-expanded={open}
					aria-controls={open ? listId : undefined}
					aria-label={ariaLabel}
				>
					<span className="truncate">
						{triggerValue || <em className="is-quiet">{placeholder}</em>}
					</span>
					<ChevronDown size={14} />
				</button>
			) : (
				<input
					className="type-custom-input"
					value={customValue}
					onChange={(event) => {
						setCustomValue(event.target.value);
						onChange(event.target.value);
					}}
					placeholder="custom-type-id"
					disabled={disabled}
					autoFocus
				/>
			)}
			{open ? createPortal(
				<div ref={panelRef} className="searchable-select-panel" style={panelStyle ?? undefined}
					onClick={(event) => event.stopPropagation()}
					onKeyDown={(event) => {
						if (event.nativeEvent.isComposing) return;
						if (event.key === "Tab") {
							event.preventDefault(); event.stopPropagation(); setOpen(false);
							focusAfterPopover(triggerRef.current, panelRef.current, event.shiftKey); return;
						}
						const options = Array.from(panelRef.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? []);
						const index = options.indexOf(document.activeElement as HTMLElement);
						let next: HTMLElement | undefined;
						if (event.key === "ArrowDown") next = options[(index + 1) % options.length];
						else if (event.key === "ArrowUp") next = options[index <= 0 ? options.length - 1 : index - 1];
						else if (event.key === "Home" && event.target !== searchRef.current) next = options[0];
						else if (event.key === "End" && event.target !== searchRef.current) next = options[options.length - 1];
						else if (event.key === "Enter" && event.target === searchRef.current) { event.preventDefault(); options[0]?.click(); }
						if (next) { event.preventDefault(); event.stopPropagation(); next.focus({ preventScroll: true }); next.scrollIntoView?.({ block: "nearest" }); }
					}}>
					{searchVisible ? (
						<div className="searchable-select-search">
							<Search size={13} />
							<input
								ref={searchRef}
								value={query}
								onChange={(event) => setQuery(event.target.value)}
								placeholder={placeholder ?? t("select.search")}
								aria-label={placeholder ?? t("select.search")}
								aria-controls={listId}
								spellCheck={false}
							/>
						</div>
					) : null}
					<div className="searchable-select-list" id={listId} role="listbox" aria-label={ariaLabel ?? placeholder ?? t("select.search")}>
						{byGroup.map(({ key, options: groupOptions }) => (
							<div key={key} className="searchable-select-group">
								{key !== "*" && byGroup.length > 1 ? (
									<div className="searchable-select-group-label">{key}</div>
								) : null}
								{groupOptions.map((option) => (
									<button
										type="button"
										key={option.value}
										role="option"
										aria-selected={option.value === value}
										tabIndex={-1}
										className={`searchable-select-item${
											option.value === value ? " is-selected" : ""
										}`}
										onClick={() => {
											if (option.value === "__custom__") {
												enterCustom();
												return;
											}
											commit(option.value);
										}}
									>
										<span className="truncate">{option.label}</span>
										{option.value === value ? <Check size={14} /> : null}
									</button>
								))}
							</div>
						))}
						{allowCustom ? <button type="button" role="option" aria-selected={isCustom} tabIndex={-1} className="searchable-select-item searchable-select-custom" onClick={enterCustom}>
							{customSentinel?.label ?? t("select.custom")}{query.trim() ? ": " + query.trim() : ""}
						</button> : null}
						{filtered.length === 0 && !allowCustom ? (
							<div className="searchable-select-empty">
								{t("select.noMatches")}
							</div>
						) : null}
					</div>
				</div>, document.body
			) : null}
		</div>
	);
}
