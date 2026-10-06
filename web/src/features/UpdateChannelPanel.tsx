import { useState } from "react";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import { Button, Field } from "../components/ui";
import { useSession } from "../session";
import { useI18n } from "../i18n";
import { api } from "../api/client";
import { UpdateDialog } from "./UpdateDialog";

type Channel = {
  channel: "stable" | "beta";
  mode: string;
  tracking_tag: string;
  /** What the tracked tag delivers; "" for a pinned tag. Watchtower installs
   *  whatever this tag points to, so the preference cannot cross tracks. */
  tracking_channel: "stable" | "beta" | "";
};
export function UpdateChannelPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const qc = useQueryClient();
  const [draft, setDraft] = useState<"stable" | "beta" | null>(null);
  const [open, setOpen] = useState(false);
  const current = useQuery({
    queryKey: ["update-channel"],
    queryFn: ({ signal }) => client!.get<Channel>("/admin/update-channel", signal),
  });
  const save = useMutation({
    mutationFn: () => client!.put("/admin/update-channel", { channel: draft }),
    onSuccess: async () => {
      setDraft(null);
      await qc.invalidateQueries({ queryKey: ["update-channel"] });
      await qc.invalidateQueries({ queryKey: ["update-check"] });
    },
  });
  const check = useMutation({
    mutationFn: () => api(client!).refreshUpdateCheck(),
    onSuccess: (data) => {
      qc.setQueryData(["update-check"], data);
      setOpen(true);
    },
  });
  const choice = draft ?? current.data?.channel ?? "stable";
  // The tag the executor updates is set in the deployment file, so a channel
  // the tag cannot deliver is not installable here — say why instead of saving
  // a preference that changes nothing.
  const trackLocked = Boolean(
    current.data?.mode === "watchtower" &&
    current.data.tracking_channel &&
    current.data.tracking_channel !== choice,
  );
  return (
    <section>
      <p className="field-hint">{t("updates.channelHint")}</p>
      <Field label={t("updates.channel")}>
        <select
          value={choice}
          disabled={!current.data || save.isPending}
          onChange={(e) => setDraft(e.target.value as "stable" | "beta")}
        >
          <option value="stable">{t("updates.stable")}</option>
          <option value="beta">Beta</option>
        </select>
      </Field>
      {choice === "beta" ? <p className="field-hint">{t("updates.betaWarning")}</p> : null}
      {current.data?.mode === "watchtower" ? (
        <p className="field-hint">
          {t("updates.watchtowerHint", {
            tag: current.data.tracking_tag || "—",
          })}
        </p>
      ) : null}
      {trackLocked ? (
        <p className="field-hint" role="status">
          {t("updates.trackLocked", {
            tag: current.data?.tracking_tag ?? "—",
            channel: t(
              current.data?.tracking_channel === "beta" ? "updates.beta" : "updates.stable",
            ),
          })}
        </p>
      ) : null}
      <Button
        disabled={!draft || draft === current.data?.channel || save.isPending || trackLocked}
        onClick={() => save.mutate()}
      >
        {t("common.save")}
      </Button>{" "}
      <Button
        variant="secondary"
        disabled={
          !current.data ||
          save.isPending ||
          check.isPending ||
          trackLocked ||
          Boolean(draft && draft !== current.data?.channel)
        }
        onClick={() => check.mutate()}
      >
        {t("updates.check")}
      </Button>
      {save.isError || current.isError || check.isError ? (
        <p role="alert">{t("updates.failed")}</p>
      ) : null}
      {open && check.data ? (
        <UpdateDialog update={check.data} onClose={() => setOpen(false)} />
      ) : null}
    </section>
  );
}
