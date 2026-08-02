import { AlertTriangle, LoaderCircle, X } from "lucide-react";
import { useEffect, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../i18n";
import { formatErrorObject } from "../formatError";

export function Button({
	children,
	variant = "primary",
	icon,
	className,
	...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
	variant?: "primary" | "secondary" | "danger" | "quiet";
	icon?: ReactNode;
}) {
	return (
		<button
			className={[`button button-${variant}`, className]
				.filter(Boolean)
				.join(" ")}
			{...props}
		>
			{icon}
			{children}
		</button>
	);
}

export function IconButton({
	label,
	children,
	className,
	...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
	return (
		<button
			className={["icon-button", className].filter(Boolean).join(" ")}
			aria-label={label}
			title={label}
			{...props}
		>
			{children}
		</button>
	);
}

export function Page({
	title,
	description,
	actions,
	kicker,
	children,
}: {
	title: string;
	description: string;
	actions?: ReactNode;
	/** Small brand label above the title (ops console continuity). */
	kicker?: string;
	children: ReactNode;
}) {
	return (
		<main className="page">
			<header className="page-header">
				<div className="page-heading">
					{kicker ? <p className="page-kicker">{kicker}</p> : null}
					<h1>{title}</h1>
					<p>{description}</p>
				</div>
				{actions && <div className="toolbar">{actions}</div>}
			</header>
			{children}
		</main>
	);
}

export function Panel({
	title,
	actions,
	children,
	className = "",
}: {
	title?: string;
	actions?: ReactNode;
	children: ReactNode;
	className?: string;
}) {
	return (
		<section className={`panel ${className}`.trim()}>
			{(title || actions) && (
				<header className="panel-header">
					{title && <h2>{title}</h2>}
					<div className="toolbar">{actions}</div>
				</header>
			)}
			{children}
		</section>
	);
}

export function Dialog({
	title,
	children,
	onClose,
	actions,
	danger = false,
}: {
	title: string;
	children: ReactNode;
	onClose: () => void;
	actions?: ReactNode;
	danger?: boolean;
}) {
	const { t } = useI18n();
	const dialogRef = useRef<HTMLElement | null>(null);
	const onCloseRef = useRef(onClose);
	onCloseRef.current = onClose;
	useEffect(() => {
		const node = dialogRef.current;
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
		// Move focus into the dialog on open so keyboard users land inside.
		const first = focusables()[0];
		(first ?? node)?.focus();
		const onKeydown = (e: KeyboardEvent) => {
			if (e.key === "Escape") {
				onCloseRef.current();
				return;
			}
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
		window.addEventListener("keydown", onKeydown);
		return () => {
			window.removeEventListener("keydown", onKeydown);
			previous?.focus();
		};
		// Mount-only: onClose is tracked via ref so re-renders do not steal focus.
	}, []);
	// Portal to document.body so fixed backdrop is not clipped by Panel/content
	// overflow:hidden or filtered/transformed ancestors (common after ops shell restyle).
	return createPortal(
		<div
			className="dialog-backdrop"
			role="presentation"
			onMouseDown={(e) => e.target === e.currentTarget && onClose()}
		>
			<section
				ref={dialogRef}
				className="dialog"
				role="dialog"
				aria-modal="true"
				aria-labelledby="dialog-title"
			>
				<header>
					<div className={danger ? "danger-title" : ""}>
						{danger && <AlertTriangle size={18} />}
						<h2 id="dialog-title">{title}</h2>
					</div>
					<IconButton label={t("common.close")} onClick={onClose}>
						<X size={18} />
					</IconButton>
				</header>
				<div className="dialog-body">{children}</div>
				{actions && <footer>{actions}</footer>}
			</section>
		</div>,
		document.body,
	);
}

export function ConfirmDialog({
	title,
	message,
	confirmLabel,
	pending,
	error,
	onConfirm,
	onClose,
}: {
	title: string;
	message: string;
	confirmLabel?: string;
	pending?: boolean;
	error?: unknown;
	onConfirm: () => void;
	onClose: () => void;
}) {
	const { t } = useI18n();
	return (
		<Dialog
			title={title}
			onClose={onClose}
			danger
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button variant="danger" disabled={pending} onClick={onConfirm}>
						{pending
							? t("common.working")
							: (confirmLabel ?? t("common.delete"))}
					</Button>
				</>
			}
		>
			<p>{message}</p>
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}

export function Field({
	label,
	children,
	hint,
}: {
	label: string;
	children: ReactNode;
	hint?: string;
}) {
	return (
		<label className="field">
			<span>{label}</span>
			{children}
			{hint && <small>{hint}</small>}
		</label>
	);
}

export function StatusBadge({ value }: { value: string | boolean }) {
	const { status } = useI18n();
	const raw =
		typeof value === "boolean"
			? value
				? "enabled"
				: "disabled"
			: String(value);
	return (
		<span className={`badge badge-${raw.toLowerCase().replaceAll("_", "-")}`}>
			{status(value)}
		</span>
	);
}

export function Loading() {
	const { t } = useI18n();
	return (
		<div className="state">
			<LoaderCircle className="spin" size={20} />
			<span>{t("common.loading")}</span>
		</div>
	);
}

export function Empty({ children }: { children?: ReactNode }) {
	const { t } = useI18n();
	return (
		<div className="state state-empty">{children ?? t("common.empty")}</div>
	);
}

export function ErrorState({
	error,
	retry,
}: {
	error: unknown;
	retry?: () => void;
}) {
	const { t } = useI18n();
	const formatted = formatErrorObject(error, t);
	return (
		<div className="state state-error">
			<AlertTriangle size={18} />
			<div className="error-state-body">
				<strong>{formatted.title}</strong>
				{formatted.cause ? <span>{formatted.cause}</span> : null}
				{formatted.fix ? <span className="error-state-fix">{formatted.fix}</span> : null}
			</div>
			{retry && (
				<Button variant="secondary" onClick={retry}>
					{t("common.retry")}
				</Button>
			)}
		</div>
	);
}

export function DataTable({
	headers,
	children,
	empty,
}: {
	headers: string[];
	children: ReactNode;
	empty?: boolean;
}) {
	if (empty) return <Empty />;
	return (
		<div className="table-wrap">
			<table>
				<thead>
					<tr>
						{headers.map((h) => (
							<th key={h}>{h}</th>
						))}
					</tr>
				</thead>
				<tbody>{children}</tbody>
			</table>
		</div>
	);
}

export function Tabs({
	items,
	active,
	onChange,
}: {
	items: Array<{ value: string; label: string; icon?: ReactNode }>;
	active: string;
	onChange: (value: string) => void;
}) {
	return (
		<div className="tabs" role="tablist">
			{items.map((item) => (
				<button
					role="tab"
					aria-selected={active === item.value}
					key={item.value}
					onClick={() => onChange(item.value)}
				>
					{item.icon}
					{item.label}
				</button>
			))}
		</div>
	);
}

export function formatDate(value?: string) {
	if (!value) return "-";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return value;
	const lang =
		typeof document !== "undefined"
			? document.documentElement.lang || undefined
			: undefined;
	return new Intl.DateTimeFormat(lang, {
		dateStyle: "medium",
		timeStyle: "short",
	}).format(date);
}

export function formatBytes(value: number) {
	if (value < 1024) return `${value} B`;
	if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`;
	return `${(value / 1024 ** 2).toFixed(1)} MB`;
}
