import {
	ArrowDown,
	ArrowUp,
	Boxes,
	Cable,
	CornerDownLeft,
	KeyRound,
	ScrollText,
	Search,
	X,
	type LucideIcon,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
	useEffect,
	useMemo,
	useRef,
	useState,
	type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import { useSession } from "../session";
import { useI18n } from "../i18n";

type NavDest = { to: string; label: string; icon: LucideIcon };

type Command = {
	key: string;
	group: string;
	icon: ReactNode;
	title: string;
	meta?: string;
	run: () => void;
};

/**
 * ⌘K command palette: jump to any page instantly, or fuzzy-search the whole
 * gateway (channels / models / keys / logs) and teleport to the result.
 * Full keyboard control: ↑↓ move, Enter run, Esc close.
 */
export function CommandPalette({
	open,
	onClose,
	nav,
}: {
	open: boolean;
	onClose: () => void;
	nav: NavDest[];
}) {
	const { client } = useSession();
	const service = api(client!);
	const { t } = useI18n();
	const navigate = useNavigate();

	const [query, setQuery] = useState("");
	const [debounced, setDebounced] = useState("");
	const [active, setActive] = useState(0);
	const inputRef = useRef<HTMLInputElement>(null);
	const listRef = useRef<HTMLDivElement>(null);

	// Reset on every open.
	useEffect(() => {
		if (open) {
			setQuery("");
			setDebounced("");
			setActive(0);
			// Focus after the panel paints so the browser scrolls to it.
			requestAnimationFrame(() => inputRef.current?.focus());
		}
	}, [open]);

	useEffect(() => {
		const handle = setTimeout(() => setDebounced(query.trim()), 200);
		return () => clearTimeout(handle);
	}, [query]);

	const results = useQuery({
		queryKey: ["command-search", debounced],
		queryFn: ({ signal }) => service.globalSearch(debounced, signal),
		enabled: open && debounced.length > 0,
	});

	const q = debounced.toLowerCase();

	// Build the flat, keyboard-navigable command list.
	const commands = useMemo<Command[]>(() => {
		const go = (to: string) => {
			onClose();
			navigate(to);
		};

		const navCommands: Command[] = nav
			.filter((n) => !q || n.label.toLowerCase().includes(q))
			.map((n) => ({
				key: `nav:${n.to}`,
				group: t("command.go"),
				icon: <n.icon size={16} />,
				title: n.label,
				run: () => go(n.to),
			}));

		const hits = results.data ?? {
			channels: [],
			routes: [],
			credentials: [],
			logs: [],
		};
		const hitCommands: Command[] = [];
		if (q) {
			for (const c of hits.channels) {
				hitCommands.push({
					key: `ch:${c.id}`,
					group: t("search.channels"),
					icon: <Cable size={16} />,
					title: c.name,
					meta: c.url,
					run: () => go(`/channels?search=${encodeURIComponent(c.name)}`),
				});
			}
			for (const r of hits.routes) {
				hitCommands.push({
					key: `rt:${r.id}`,
					group: t("search.models"),
					icon: <Boxes size={16} />,
					title: r.model,
					meta: r.status,
					run: () => go(`/models?model=${encodeURIComponent(r.model)}`),
				});
			}
			for (const k of hits.credentials) {
				hitCommands.push({
					key: `cr:${k.id}`,
					group: t("search.keys"),
					icon: <KeyRound size={16} />,
					title: k.name,
					meta: k.kind,
					run: () => go(`/keys?search=${encodeURIComponent(k.name)}`),
				});
			}
			for (const l of hits.logs) {
				hitCommands.push({
					key: `lg:${l.id}`,
					group: t("search.logs"),
					icon: <ScrollText size={16} />,
					title: l.model || l.request_id,
					meta: l.request_id.slice(0, 24),
					run: () =>
						go(
							`/logs?upstream_request_id=${encodeURIComponent(
								l.upstream_request_id || l.request_id,
							)}`,
						),
				});
			}
		}
		return [...navCommands, ...hitCommands];
	}, [nav, q, results.data, navigate, onClose, t]);

	// Keep the active index in bounds as the list shrinks/grows.
	useEffect(() => {
		setActive((current) => Math.min(current, Math.max(0, commands.length - 1)));
	}, [commands.length]);

	// Scroll the active row into view.
	useEffect(() => {
		const node = listRef.current;
		const row = node?.querySelector<HTMLElement>(`[data-index="${active}"]`);
		row?.scrollIntoView({ block: "nearest" });
	}, [active]);

	// Global keyboard handling.
	useEffect(() => {
		if (!open) return;
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape") {
				e.preventDefault();
				onClose();
			} else if (e.key === "ArrowDown") {
				e.preventDefault();
				setActive((i) => (commands.length ? (i + 1) % commands.length : 0));
			} else if (e.key === "ArrowUp") {
				e.preventDefault();
				setActive((i) =>
					commands.length ? (i - 1 + commands.length) % commands.length : 0,
				);
			} else if (e.key === "Enter") {
				e.preventDefault();
				commands[active]?.run();
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [open, commands, active, onClose]);

	// Lock body scroll.
	useEffect(() => {
		if (!open) return;
		const prev = document.body.style.overflow;
		document.body.style.overflow = "hidden";
		return () => {
			document.body.style.overflow = prev;
		};
	}, [open]);

	if (!open) return null;

	// Group commands for display while retaining the flat index for keyboard.
	let flatIndex = -1;
	const rows: ReactNode[] = [];
	let lastGroup = "";
	for (const command of commands) {
		flatIndex += 1;
		if (command.group !== lastGroup) {
			lastGroup = command.group;
			rows.push(
				<div className="command-group-label" key={`g:${command.group}`}>
					{command.group}
				</div>,
			);
		}
		rows.push(
			<button
				key={command.key}
				type="button"
				className={flatIndex === active ? "command-item is-active" : "command-item"}
				data-index={flatIndex}
				onMouseEnter={() => setActive(flatIndex)}
				onClick={command.run}
			>
				<span className="command-item-icon">{command.icon}</span>
				<span className="command-item-title">{command.title}</span>
				{command.meta ? (
					<span className="command-item-meta">{command.meta}</span>
				) : null}
			</button>,
		);
	}

	return createPortal(
		<div
			className="command-backdrop"
			role="presentation"
			onMouseDown={(e) => e.target === e.currentTarget && onClose()}
		>
			<section className="command-panel" role="dialog" aria-modal="true" aria-label="Command palette">
				<div className="command-search-box">
					<Search size={16} className="command-search-icon" />
					<input
						ref={inputRef}
						value={query}
						placeholder={t("command.placeholder")}
						onChange={(e) => setQuery(e.target.value)}
					/>
					<button
						type="button"
						className="command-search-clear"
						onClick={() => {
							setQuery("");
							inputRef.current?.focus();
						}}
						aria-label={t("common.close")}
					>
						<X size={14} />
					</button>
				</div>
				<div className="command-list" ref={listRef}>
					{results.isFetching && !results.data && q ? (
						<div className="command-empty">{t("search.searching")}</div>
					) : commands.length === 0 ? (
						<div className="command-empty">{t("command.empty")}</div>
					) : (
						rows
					)}
				</div>
				<footer className="command-footer">
					<span>
						<ArrowUp size={12} />
						<ArrowDown size={12} />
						{t("command.move")}
					</span>
					<span>
						<CornerDownLeft size={12} />
						{t("command.open")}
					</span>
					<span className="command-footer-esc">ESC · {t("command.close")}</span>
				</footer>
			</section>
		</div>,
		document.body,
	);
}