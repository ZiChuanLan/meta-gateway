import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { Download, ExternalLink, RefreshCw } from "lucide-react";
import { api } from "../api/client";
import { useSession } from "../session";
import { useI18n } from "../i18n";
import { Button, Dialog } from "../components/ui";
import { useOneClickUpdate } from "../hooks/useOneClickUpdate";
import type { UpdateCheckStatus } from "../api/types";

/**
 * The one-click update dialog behind the top-bar version pill.
 *
 * The pill used to link out to GitHub; clicking it now opens THIS dialog: what
 * the new version changes (the release body, verbatim from GitHub), and an
 * apply button that starts the container handoff. The link out stays available
 * as a secondary action for the full release page.
 *
 * `update` is the parent's already-fetched check result; the dialog refetches
 * only when opened, so a stale badge does not show stale notes.
 */
export function UpdateDialog({
	update,
	onClose,
}: {
	update: UpdateCheckStatus;
	onClose: () => void;
}) {
	const { t } = useI18n();
	const { client } = useSession();
	const service = client ? api(client) : null;
	const { watch, apply } = useOneClickUpdate();

	// Refetch on open: the pill may have been rendered from a cache that is
	// hours old, and the notes shown here are the whole point of the dialog.
	const fresh = useQuery({
		queryKey: ["update-check-dialog"],
		queryFn: ({ signal }) => service!.updateCheck(signal),
		enabled: Boolean(service),
		staleTime: 5 * 60_000,
	});
	const status = fresh.data ?? update;
	const notes = (status.notes ?? "").trim();

	// While the handoff is running this dialog IS the progress display: the
	// container will stop answering mid-poll, which reads as "restarting", not
	// as an error.
	useEffect(() => {
		if (!watch) return;
		const started = Date.now();
		const timer = window.setInterval(async () => {
			try {
				const res = await fetch("/healthz");
				const body = (await res.json()) as { version?: string };
				if (body.version === watch.target) {
					window.clearInterval(timer);
					window.setTimeout(() => window.location.reload(), 1200);
					return;
				}
			} catch {
				// Container restarting — keep polling; the running state below
				// already tells the operator what is happening.
			}
			if (Date.now() - started > 180_000) {
				window.clearInterval(timer);
			}
		}, 3000);
		return () => window.clearInterval(timer);
	}, [watch]);

	return (
		<Dialog
			title={t("app.updateDialogTitle", { version: status.latest })}
			onClose={() => {
				// The dialog cannot close mid-handoff: the container is being torn
				// down and the only way out is forward (or the 3-minute timeout).
				if (!watch) onClose();
			}}
		>
			<div className="update-dialog-body">
				{watch ? (
					<div className="update-progress" role="status">
						<RefreshCw size={16} className="is-spinning" />
						<div>
							<strong>{t("app.updateRunning")}</strong>
							<p>{t("app.updateRunningHint")}</p>
						</div>
					</div>
				) : (
					<>
						{notes ? (
							<pre className="update-notes">{notes}</pre>
						) : (
							<p className="muted">{t("app.updateNoNotes")}</p>
						)}
						<p className="muted update-restart-hint">{t("app.updateRestartHint")}</p>
					</>
				)}
			</div>
			<div className="update-dialog-actions">
				{status.release_url ? (
					<a
						className="update-release-link"
						href={status.release_url}
						target="_blank"
						rel="noreferrer"
					>
						<ExternalLink size={13} />
						{t("app.updateReleasePage")}
					</a>
				) : null}
				<Button
					icon={<Download size={14} />}
					disabled={Boolean(watch) || fresh.isPending}
					onClick={() => apply(status.latest)}
				>
					{watch ? t("app.updateRunning") : t("app.updateApply", { version: status.latest })}
				</Button>
			</div>
		</Dialog>
	);
}
