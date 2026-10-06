import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
  const qc = useQueryClient();
  const channel = useQuery({
    queryKey: ["update-channel"],
    queryFn: ({ signal }) =>
      client!.get<{
        channel: "stable" | "beta";
        mode: string;
        tracking_tag: string;
      }>("/admin/update-channel", signal),
    enabled: Boolean(client),
  });
  const changeChannel = useMutation({
    mutationFn: async (value: string) => {
      await client!.put("/admin/update-channel", { channel: value });
      const checked = await service!.refreshUpdateCheck();

      qc.setQueryData(["update-check"], checked);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["update-channel"] }),
  });

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
  // floating tag, and the console's channel preference cannot change which tag
  // that is — the preference lives in the deployment file. An install that
  // crosses tracks is refused by the server, so it is prepared for here.
  const executor = availability?.mode === "watchtower" ? availability : undefined;
  const trackedTag = executor?.tracking_tag;
  const trackedChannel = executor?.tracking_channel;
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
      busy={pending || changeChannel.isPending}
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
        <Field label={t("updates.channel")}>
          <select
            value={channel.data?.channel ?? status.channel ?? "stable"}
            disabled={!channel.data || Boolean(watch) || pending || changeChannel.isPending}
            onChange={(event) => {
              setApplyError(null);
              changeChannel.mutate(event.target.value);
            }}
          >
            <option value="stable">{t("updates.stable")}</option>
            <option value="beta">Beta</option>
          </select>
        </Field>
        {channel.data?.channel === "beta" ? (
          <p className="field-hint">{t("updates.betaWarning")}</p>
        ) : null}
        {channel.data?.mode === "watchtower" ? (
          <p className="field-hint">
            {t("updates.watchtowerHint", {
              tag: channel.data.tracking_tag || "—",
            })}
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
            changeChannel.isPending ||
            changeChannel.isError ||
            fresh.isFetching ||
            fresh.isError ||
            trackMismatch ||
            !channel.data ||
            (status.channel !== undefined && channel.data.channel !== status.channel) ||
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
