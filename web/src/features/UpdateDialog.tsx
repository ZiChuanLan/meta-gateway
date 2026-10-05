import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, ExternalLink, RefreshCw } from "lucide-react";
import { api } from "../api/client";
import { useSession } from "../session";
import { useI18n } from "../i18n";
import { Button, Dialog, Field } from "../components/ui";
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
	const { watch, apply, failure } = useOneClickUpdate();
 const [applyError,setApplyError]=useState(false);
 const [pending,setPending]=useState(false);
 const qc=useQueryClient();
 const channel=useQuery({queryKey:["update-channel"],queryFn:({signal})=>client!.get<{channel:"stable"|"beta";mode:string;tracking_tag:string}>("/admin/update-channel",signal),enabled:Boolean(client)});
 const changeChannel=useMutation({mutationFn:async(value:string)=>{
  await client!.put("/admin/update-channel",{channel:value});
  const checked=await service!.refreshUpdateCheck();
  qc.setQueryData(["update-check-dialog"],checked);
  qc.setQueryData(["update-check"],checked);
 },onSuccess:()=>qc.invalidateQueries({queryKey:["update-channel"]})});

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
	// A failed handoff says why. "Permission denied" on the Docker socket is the
	// one that actually happens, and the raw message names neither the cause nor
	// the fix.
	const permissionFailure = /permission denied/i.test(failure?.reason ?? "");
	// Running a build that is NEWER than the selected channel's latest is the
	// trap: the dialog used to say "no upgrade available" and nothing else, so an
	// operator who had just switched to stable read that as "I am on stable".
	const aheadOfChannel =
		!status.has_update &&
		Boolean(status.current) &&
		Boolean(status.latest) &&
		status.current !== status.latest;

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
			title={
				status.has_update
					? t("app.updateDialogTitle", { version: status.latest })
					: t("updates.dialogTitle")
			}
            busy={Boolean(watch) || pending || changeChannel.isPending}
			onClose={() => {
				// The dialog cannot close mid-handoff: the container is being torn
				// down and the only way out is forward (or the 3-minute timeout).
				if (!watch) onClose();
			}}
		>
			<div className="update-dialog-body">
                <Field label={t("updates.channel")}>
                  <select value={channel.data?.channel ?? status.channel ?? "stable"} disabled={!channel.data||Boolean(watch)||pending||changeChannel.isPending} onChange={event=>{setApplyError(false);changeChannel.mutate(event.target.value);}}>
                    <option value="stable">{t("updates.stable")}</option><option value="beta">Beta</option>
                  </select>
                </Field>
                {channel.data?.channel==="beta" ? <p className="field-hint">{t("updates.betaWarning")}</p> : null}
                {channel.data?.mode==="watchtower" ? <p className="field-hint">{t("updates.watchtowerHint",{tag:channel.data.tracking_tag||"—"})}</p> : null}
                {changeChannel.isError ? <p role="alert">{t("updates.failed")}</p> : null}
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
						<p className="muted update-current">
							{t("updates.currentVersion", { version: status.current || "—" })}
						</p>
						{notes ? (
							<pre className="update-notes">{notes}</pre>
						) : (
							<p className="muted">{t("app.updateNoNotes")}</p>
						)}
						{aheadOfChannel ? (
							<p className="field-hint" role="status">
								{t("updates.channelIsNewer", {
									current: status.current,
									channel: t(status.channel === "beta" ? "updates.beta" : "updates.stable"),
									latest: status.latest,
								})}
							</p>
						) : !status.has_update ? (
							<p className="muted">{t("updates.noUpgrade")}</p>
						) : null}
 <p className="muted update-restart-hint">{t("app.updateRestartHint")}</p>
					</>
				)}
			</div>
			{failure ? (
				<div role="alert" className="inline-error">
					{/* No reason means the watch ran out of time without the updater ever
					    reporting a failure — a different situation from one it diagnosed. */}
					{failure.reason ? (
						<>
							<strong>{t("updates.failedReason")}</strong>
							<span> {failure.reason}</span>
						</>
					) : (
						<span>{t("updates.timeout")}</span>
					)}
					{permissionFailure ? (
						<p className="field-hint">{t("updates.socketPermission")}</p>
					) : null}
				</div>
			) : null}
 {applyError ? <p role="alert">{t("updates.applyFailed")}</p> : null}
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
					disabled={Boolean(watch) || pending || changeChannel.isPending || changeChannel.isError || fresh.isFetching || !channel.data || (status.channel !== undefined && channel.data.channel !== status.channel) || !status.enabled || !status.has_update || Boolean(status.error)}
					onClick={async () => {setPending(true);setApplyError(false);try{await apply(status.latest);}catch{setApplyError(true);}finally{setPending(false);}}}
				>
					{watch
						? t("app.updateRunning")
						: status.has_update
							? t("app.updateApply", { version: status.latest })
							: t("updates.nothingToInstall")}
				</Button>
			</div>
		</Dialog>
	);
}
