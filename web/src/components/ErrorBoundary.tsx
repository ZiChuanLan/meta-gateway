import { Component, type ErrorInfo, type ReactNode } from "react";
import { useI18n } from "../i18n";
import { Button } from "./ui";
import { recentPreloadFailure } from "../lib/preloadRecovery";

/**
 * Whole-app render guard.
 *
 * A thrown render error used to unmount the entire tree and leave a blank
 * page with no way back — the console reads fields straight off API payloads
 * (`.models.length` on a refresh result), so one unexpected null was enough.
 * The payloads are fixed at the source; this keeps the *next* surprise from
 * being a white screen.
 */
/**
 * A lazily imported chunk that no longer exists — the signature of a tab that
 * outlived a deploy, not of a bug in this build. Vite words this differently
 * across browsers and versions, hence the three spellings.
 */
export function isStaleChunkError(error: unknown): boolean {
	const message = error instanceof Error ? error.message : String(error ?? "");
	if (
		/Failed to fetch dynamically imported module|Importing a module script failed|error loading dynamically imported module/i.test(
			message,
		)
	) {
		return true;
	}
	// The browser does not always keep the original wording: when a lazy chunk
	// never arrives, React surfaces its own error, so the reliable signal is the
	// stamp the preload handler just wrote.
	return recentPreloadFailure();
}

/**
 * The stale-bundle face of the boundary: it says what happened and offers the
 * one action that helps, instead of a stack trace the user cannot use.
 */
function StaleBundleFallback({ onReload }: { onReload: () => void }) {
	const { t } = useI18n();
	return (
		<div className="crash-screen" role="alert">
			<div className="crash-card">
				<p className="crash-kicker">{t("app.staleKicker")}</p>
				<h1>{t("app.staleTitle")}</h1>
				<p className="crash-body">{t("app.staleBundle")}</p>
				<div className="crash-actions">
					<Button variant="secondary" onClick={onReload}>
						{t("crash.reload")}
					</Button>
				</div>
			</div>
		</div>
	);
}

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
		if (this.state.error) {
			// A stale chunk is recovered by reloading, which the preload handler
			// already tried once: landing here means the reload did not help, so
			// the honest answer is "refresh", not a stack trace.
			if (isStaleChunkError(this.state.error)) {
				return <StaleBundleFallback onReload={this.reload} />;
			}
			return this.props.fallback(this.state.error, this.reload);
		}
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
