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
  const [backup, setBackup] = useState<string | undefined>(undefined);

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
  // What the executor can actually install. A tag-following executor (the
  // compose updater, or the watchtower companion) installs whatever the
  // deployment's tag points to at that moment — not the release the console
  // named — so its tracking fields decide what may be offered. Socket mode can
  // install a named release, so it is exempt.
  const tagFollower = availability?.mode === "compose" || availability?.mode === "watchtower";
  const executor = tagFollower ? availability : undefined;
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
  // The previous run, when it failed. The executor is a separate container, so
  // its failure is not this process's error: without this the dialog would show
  // nothing at all and the version would simply not change.
  const updaterFailure =
    availability?.last_result && availability.last_result.exit_code !== 0
      ? availability.last_result
      : undefined;
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
        {/* A failed run happens in the executor's container, so this process has
            no error of its own to show; the result file is the only record. */}
        {updaterFailure ? (
          <details className="update-last-failure" open>
            <summary role="alert">
              {t("updates.lastFailure", { code: updaterFailure.exit_code })}
            </summary>
            <pre>{updaterFailure.log || t("updates.lastFailureNoLog")}</pre>
          </details>
        ) : null}
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
        {/* What the executor does with the deployment file — the one difference
            an operator needs to know: only the compose updater re-reads it, so
            only it brings env changes along. */}
        {availability?.mode === "compose" ? (
          <p className="field-hint">
            {t("updates.modeCompose", { project: availability.updater_project || "—" })}
          </p>
        ) : null}
        {availability?.mode === "watchtower" ? (
          <p className="field-hint">{t("updates.modeWatchtower")}</p>
        ) : null}
        {watch ? (
          <div className="update-progress" role="status">
            <RefreshCw size={16} className="is-spinning" />
            <div>
              <strong>{t("app.updateRunning")}</strong>
              <p>{t("app.updateRunningHint")}</p>
              {/* The snapshot this upgrade can be rolled back to. Naming it here
                  is the difference between "an update is running" and "and here
                  is how to undo it". */}
              {backup ? (
                <p className="update-progress-backup">
                  {t("updates.backupTaken", { name: backup })}
                </p>
              ) : null}
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
            {/* Say it before the click, not after: this is why nobody has to
                copy a volume by hand first. */}
            {status.has_update ? (
              <p className="field-hint">{t("updates.backupBeforeApply")}</p>
            ) : null}
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
              const name = await apply(status.latest, {
                from: status.current,
                tracked: trackedTag,
              });
              if (name) setBackup(name);
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
