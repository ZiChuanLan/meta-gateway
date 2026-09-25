import { Component, type ErrorInfo, type ReactNode } from "react";
import { useI18n } from "../i18n";
import { Button } from "./ui";

/**
 * Whole-app render guard.
 *
 * A thrown render error used to unmount the entire tree and leave a blank
 * page with no way back — the console reads fields straight off API payloads
 * (`.models.length` on a refresh result), so one unexpected null was enough.
 * The payloads are fixed at the source; this keeps the *next* surprise from
 * being a white screen.
 */
function BoundaryFallback({
	error,
	onReload,
}: {
	error: Error;
	onReload: () => void;
}) {
	const { t } = useI18n();
	const detail = `${error.message}\n${error.stack ?? ""}`.trim();
	const copy = () => {
		void navigator.clipboard?.writeText(detail).catch(() => undefined);
	};
	return (
		<div className="crash-screen" role="alert">
			<div className="crash-card">
				<p className="crash-kicker">{t("crash.kicker")}</p>
				<h1>{t("crash.title")}</h1>
				<p className="crash-body">{t("crash.body")}</p>
				<pre className="crash-detail mono">{detail}</pre>
				<div className="crash-actions">
					<Button variant="secondary" onClick={onReload}>
						{t("crash.reload")}
					</Button>
					<Button variant="quiet" onClick={copy}>
						{t("crash.copy")}
					</Button>
				</div>
			</div>
		</div>
	);
}

class RenderBoundary extends Component<
	{ children: ReactNode; fallback: (error: Error, reload: () => void) => ReactNode },
	{ error: Error | null }
> {
	state: { error: Error | null } = { error: null };

	static getDerivedStateFromError(error: Error) {
		return { error };
	}

	componentDidCatch(error: Error, info: ErrorInfo) {
		// Keep the stack in the browser console for bug reports.
		console.error("console render failed", error, info.componentStack);
	}

	reload = () => {
		window.location.reload();
	};

	render() {
		if (this.state.error) return this.props.fallback(this.state.error, this.reload);
		return this.props.children;
	}
}

export function ErrorBoundary({ children }: { children: ReactNode }) {
	return (
		<RenderBoundary
			fallback={(error, reload) => (
				<BoundaryFallback error={error} onReload={reload} />
			)}
		>
			{children}
		</RenderBoundary>
	);
}
