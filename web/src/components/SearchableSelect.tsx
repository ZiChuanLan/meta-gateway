import { Check, ChevronDown, Search } from "lucide-react";
import {
	useEffect,
	useMemo,
	useRef,
	useState,
	type CSSProperties,
} from "react";

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
}) {
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [customMode, setCustomMode] = useState(false);
	const [customValue, setCustomValue] = useState("");
	const rootRef = useRef<HTMLDivElement>(null);
	const searchRef = useRef<HTMLInputElement>(null);
	// The panel is fixed-positioned so scroll containers (e.g. Dialog) cannot clip it.
	const [panelStyle, setPanelStyle] = useState<CSSProperties | null>(null);

	// Guards against the panel re-opening right after a selection: keyboard
	// users often hit Enter/Space again (focus sits on the trigger), which
	// would otherwise re-open the dropdown immediately after it closes.
	const suppressOpenUntil = useRef(0);
	const openPanel = () => {
		if (Date.now() < suppressOpenUntil.current) return;
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
		const onPointerDown = (event: MouseEvent) => {
			if (rootRef.current?.contains(event.target as Node)) return;
			setOpen(false);
		};
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") setOpen(false);
		};
		const onScrollOrResize = (event: Event) => {
			// Scrolls inside the open panel must not dismiss it (mouse wheel over
			// the option list otherwise closes the dropdown mid-selection).
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

	useEffect(() => {
		if (open) searchRef.current?.focus();
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
		onChange(next);
		setOpen(false);
		setQuery("");
		suppressOpenUntil.current = Date.now() + 300;
		// Move focus off the trigger so a following Enter/Space cannot re-open.
		(document.activeElement as HTMLElement | null)?.blur?.();
	};

	const triggerValue = isCustom
		? value || customValue || placeholder || ""
		: selectedLabel ?? value;

	return (
		<div className="searchable-select" ref={rootRef}>
			{!customMode ? (
				<button
					type="button"
					className="searchable-select-trigger"
					disabled={disabled}
					onClick={() => (open ? setOpen(false) : openPanel())}
					aria-haspopup="listbox"
					aria-expanded={open}
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
			{open ? (
				<div className="searchable-select-panel" style={panelStyle ?? undefined} role="listbox">
					{filtered.length > 4 ? (
						<div className="searchable-select-search">
							<Search size={13} />
							<input
								ref={searchRef}
								value={query}
								onChange={(event) => setQuery(event.target.value)}
								placeholder={placeholder ?? "Search…"}
								spellCheck={false}
							/>
						</div>
					) : null}
					<div className="searchable-select-list">
						{byGroup.map(({ key, options: groupOptions }) => (
							<div key={key} className="searchable-select-group">
								{key !== "*" && byGroup.length > 1 ? (
									<div className="searchable-select-group-label">{key}</div>
								) : null}
								{groupOptions.map((option) => (
									<button
										type="button"
										key={option.value}
										className={`searchable-select-item${
											option.value === value ? " is-selected" : ""
										}`}
										onClick={() => {
											if (option.value === "__custom__") {
												setCustomMode(true);
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
						{filtered.length === 0 ? (
							<div className="searchable-select-empty">
								{allowCustom ? (
									<button
										type="button"
										className="searchable-select-item"
										onClick={() => setCustomMode(true)}
									>
										{query.trim() || "Custom…"}
									</button>
								) : (
									"Nothing found"
								)}
							</div>
						) : null}
					</div>
				</div>
			) : null}
		</div>
	);
}
