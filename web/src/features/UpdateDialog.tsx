import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Download, ExternalLink, RefreshCw } from "lucide-react";
import { api } from "../api/client";
import { useSession } from "../session";
import { useI18n } from "../i18n";
import { Button, Dialog, Field, ErrorState } from "../components/ui";
import { useOneClickUpdate } from "../hooks/useOneClickUpdate";
import type { UpdateCheckStatus } from "../api/types";
import { updateState } from "../lib/updateState";

/**
 * The one-click update dialog behind the top-bar version pill.
 *
 * The pill used to link out to GitHub; clicking it now opens THIS dialog: what
 * the new version changes (the release body, verbatim from GitHub), and an
 * apply button that starts the container handoff. The link out stays available
 * as a secondary action for the full release page.
 *
 * The channel is a fact here, not a control. Which releases a deployment
 * receives is its image tag, and that lives in the deployment file — a switch in
 * this dialog could only change which releases the console *looks at*, and the
 * server refuses to install across tracks, so the operator would set a
 * preference that changed nothing. The update check reads the tag instead
 * (httpapi/router.go), which is why the two can no longer disagree.
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
  const { watch, apply, failure, availability } = useOneClickUpdate();
  const [applyError, setApplyError] = useState<unknown>(null);
  const [pending, setPending] = useState(false);

  // Refetch on open: the pill may have been rendered from a cache that is
  // hours old, and the notes shown here are the whole point of the dialog.
  const fresh = useQuery({
    queryKey: ["update-check"],
    queryFn: ({ signal }) => service!.updateCheck(signal),
    enabled: Boolean(service),
    staleTime: 5 * 60_000,
  });
  const status = fresh.data ?? update;
  const notes = (status.notes ?? "").trim();
  // What the executor can actually install. In watchtower mode it updates one
  // floating tag, and the console cannot change which tag that is — the tag is
  // declared in the deployment file. An install that crosses tracks is refused
  // by the server, so it is prepared for here.
  const executor = availability?.mode === "watchtower" ? availability : undefined;
  const trackedTag = executor?.tracking_tag;
  // The channel the check read, and the tag behind it. Both come from the
  // deployment: the server derives the check's channel from the same tag.
  const trackedChannel = availability?.tracking_channel;
  const deployChannel = trackedChannel || status.channel;
  // A tag that is neither channel (a pinned version) can only be re-pulled by
  // the executor, so no release the console names would ever be installed.
  const pinnedTag = Boolean(executor?.tracking_tag && !executor?.tracking_channel);
  const trackMismatch = Boolean(
    trackedChannel && status.channel && trackedChannel !== status.channel,
  );
  const lastFailure =
    failure ??
    (!watch && !applyError && availability?.phase === "failed" && availability.error
      ? { target: availability.target ?? status.latest, reason: availability.error }
      : null);
  // A failed handoff says why. "Permission denied" on the Docker socket is the
  // one that actually happens, and the raw message names neither the cause nor
  // the fix.
  const permissionFailure = /permission denied/i.test(lastFailure?.reason ?? "");
  // Running a build that is NEWER than the selected channel's latest is the
  // trap: the dialog used to say "no upgrade available" and nothing else, so an
  // operator who had just switched to stable read that as "I am on stable".
  const state = updateState(status);
  const aheadOfChannel = state === "ahead";

  return (
    <Dialog
      title={
        status.has_update && !executor
          ? t("app.updateDialogTitle", { version: status.latest })
          : t("updates.dialogTitle")
      }
      busy={pending}
      onClose={onClose}
    >
      <div className="update-dialog-body">
        {["disabled", "unchecked", "uncomparable", "checkFailed"].includes(state) ? (
          <p role="status">{t(`updates.state.${state}`)}</p>
        ) : null}
        {fresh.isError ? <ErrorState error={fresh.error} retry={() => fresh.refetch()} /> : null}
        {status.error ? <p role="alert">{status.error}</p> : null}
        {availability && !availability.available ? (
          <p role="status">{t("updates.manualRequired")}</p>
        ) : null}
        {/* The channel the deployment receives, read from its image tag. Shown
            rather than offered: only the deployment file can change it. */}
        <Field label={t("updates.channel")}>
          <span className="update-channel-fact">
            {t(deployChannel === "beta" ? "updates.beta" : "updates.stable")}
          </span>
        </Field>
        <p className="field-hint">
          {trackedTag
            ? t("updates.channelFromDeploy", { tag: trackedTag })
            : t("updates.channelUnknown")}
        </p>
        {deployChannel === "beta" ? <p className="field-hint">{t("updates.betaWarning")}</p> : null}
        {pinnedTag ? (
          <p className="field-hint" role="status">
            {t("updates.pinnedTag", { tag: trackedTag ?? "—" })}
          </p>
        ) : null}
        {trackMismatch ? (
          <p className="field-hint" role="status">
            {t("updates.trackLocked", {
              tag: trackedTag ?? "—",
              channel: t(trackedChannel === "beta" ? "updates.beta" : "updates.stable"),
            })}
          </p>
        ) : null}
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
      {lastFailure ? (
        <div role="alert" className="inline-error">
          {/* No reason means the watch ran out of time without the updater ever
					    reporting a failure — a different situation from one it diagnosed. */}
          {lastFailure.reason ? (
            <>
              <strong>{t("updates.failedReason")}</strong>
              <span>
                {" "}
                {lastFailure.code === "notNewer"
                  ? t("updates.notNewer", { detail: lastFailure.reason })
                  : lastFailure.reason}
              </span>
            </>
          ) : (
            <span>{t("updates.timeout")}</span>
          )}
          {permissionFailure ? <p className="field-hint">{t("updates.socketPermission")}</p> : null}
        </div>
      ) : null}
      {applyError ? <ErrorState error={applyError} /> : null}
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
          disabled={
            !availability?.available ||
            availability.running ||
            Boolean(watch) ||
            pending ||
            fresh.isFetching ||
            fresh.isError ||
            trackMismatch ||
            pinnedTag ||
            !status.enabled ||
            !status.has_update ||
            Boolean(status.error)
          }
          onClick={async () => {
            setPending(true);
            setApplyError(null);
            try {
              await apply(status.latest, {
                from: status.current,
                tracked: trackedTag,
              });
            } catch (error) {
              setApplyError(error);
            } finally {
              setPending(false);
            }
          }}
        >
          {watch
            ? t("app.updateRunning")
            : trackedTag && status.has_update
              ? t("updates.applyTracked", { tag: trackedTag })
              : status.has_update
                ? t("app.updateApply", { version: status.latest })
                : t("updates.nothingToInstall")}
        </Button>
      </div>
    </Dialog>
  );
}
