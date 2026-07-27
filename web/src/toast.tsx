import {
	createContext,
	useCallback,
	useContext,
	useMemo,
	useRef,
	useState,
	type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { AlertTriangle, CheckCircle2, Info, X } from "lucide-react";
import { formatErrorMessage } from "./formatError";
import { useI18n } from "./i18n";

export type ToastTone = "error" | "success" | "info";

export type ToastItem = {
	id: string;
	tone: ToastTone;
	message: string;
	durationMs: number;
};

type ToastContextValue = {
	push: (input: {
		tone?: ToastTone;
		message: string;
		durationMs?: number;
	}) => void;
	pushError: (error: unknown, durationMs?: number) => void;
	dismiss: (id: string) => void;
};

const ToastContext = createContext<ToastContextValue | null>(null);

const DEFAULT_DURATION_MS = 5600;
const MAX_VISIBLE = 4;

export function ToastProvider({ children }: { children: ReactNode }) {
	const { t } = useI18n();
	const [items, setItems] = useState<ToastItem[]>([]);
	const timers = useRef<Map<string, number>>(new Map());

	const dismiss = useCallback((id: string) => {
		const timer = timers.current.get(id);
		if (timer !== undefined) {
			window.clearTimeout(timer);
			timers.current.delete(id);
		}
		setItems((current) => current.filter((item) => item.id !== id));
	}, []);

	const push = useCallback(
		(input: { tone?: ToastTone; message: string; durationMs?: number }) => {
			const message = input.message.trim();
			if (!message) return;
			const id = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
			const durationMs = input.durationMs ?? DEFAULT_DURATION_MS;
			const item: ToastItem = {
				id,
				tone: input.tone ?? "info",
				message,
				durationMs,
			};
			setItems((current) => {
				const next = [...current, item];
				return next.length > MAX_VISIBLE ? next.slice(next.length - MAX_VISIBLE) : next;
			});
			if (durationMs > 0) {
				const timer = window.setTimeout(() => dismiss(id), durationMs);
				timers.current.set(id, timer);
			}
		},
		[dismiss],
	);

	const pushError = useCallback(
		(error: unknown, durationMs?: number) => {
			push({
				tone: "error",
				message: formatErrorMessage(error, t),
				durationMs: durationMs ?? 7200,
			});
		},
		[push, t],
	);

	const value = useMemo(
		() => ({ push, pushError, dismiss }),
		[push, pushError, dismiss],
	);

	return (
		<ToastContext.Provider value={value}>
			{children}
			<ToastViewport items={items} onDismiss={dismiss} />
		</ToastContext.Provider>
	);
}

export function useToast(): ToastContextValue {
	const value = useContext(ToastContext);
	if (!value) {
		throw new Error("useToast must be used within ToastProvider");
	}
	return value;
}

function ToastViewport({
	items,
	onDismiss,
}: {
	items: ToastItem[];
	onDismiss: (id: string) => void;
}) {
	const { t } = useI18n();
	if (typeof document === "undefined") return null;
	return createPortal(
		<div className="toast-viewport" aria-live="polite" aria-relevant="additions text">
			{items.map((item) => (
				<div
					key={item.id}
					className={`toast toast-${item.tone}`}
					role={item.tone === "error" ? "alert" : "status"}
				>
					<span className="toast-icon" aria-hidden="true">
						{item.tone === "error" ? (
							<AlertTriangle size={16} />
						) : item.tone === "success" ? (
							<CheckCircle2 size={16} />
						) : (
							<Info size={16} />
						)}
					</span>
					<p className="toast-message">{item.message}</p>
					<button
						type="button"
						className="toast-dismiss"
						aria-label={t("common.close")}
						onClick={() => onDismiss(item.id)}
					>
						<X size={14} />
					</button>
				</div>
			))}
		</div>,
		document.body,
	);
}
