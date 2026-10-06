import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Dialog, Field, Loading } from "../components/ui";
import { useI18n } from "../i18n";
import { useSession } from "../session";

type Profile = { username: string; configured: boolean; totp_enabled: boolean };
export function OperatorProfilePanel({
  onSaved,
  onBusyChange,
}: {
  onSaved?: () => void;
  onBusyChange?: (busy: boolean) => void;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const qc = useQueryClient();
  const profile = useQuery({
    queryKey: ["operator-profile"],
    queryFn: ({ signal }) => client!.get<Profile>("/admin/operator-profile", signal),
  });
  const [name, setName] = useState<string | null>(null);
  const [token, setToken] = useState("");
  const [code, setCode] = useState("");
  const save = useMutation({
    mutationFn: () =>
      client!.post("/admin/operator-profile", {
        username: name ?? profile.data?.username,
        token,
        totp_code: code,
      }),
    onSuccess: async () => {
      setToken("");
      setCode("");
      await qc.invalidateQueries({ queryKey: ["operator-profile"] });
      onSaved?.();
    },
  });
  useEffect(() => {
    onBusyChange?.(save.isPending);
  }, [onBusyChange, save.isPending]);
  const username = name ?? profile.data?.username ?? "";
  if (profile.isPending) return <Loading />;
  return (
    <section className="operator-profile-panel">
      <p className="field-hint">{t("operator.usernameHint")}</p>
      {profile.isError ? (
        <p role="alert">{t("operator.loadFailed")}</p>
      ) : (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            if (!save.isPending) save.mutate();
          }}
        >
          <fieldset disabled={save.isPending} className="overlay-fields">
            <Field label={t("app.connect.username")}>
              <input
                autoComplete="username"
                value={username}
                onChange={(e) => setName(e.target.value)}
                required
                minLength={3}
                maxLength={64}
                pattern="[A-Za-z0-9_.-]+"
              />
            </Field>
            <Field label={t("operator.confirmToken")}>
              <input
                type="password"
                autoComplete="current-password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                required
              />
            </Field>
            {profile.data?.totp_enabled ? (
              <Field label={t("app.connect.totp")}>
                <input
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  pattern="[0-9]{6}"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  required
                />
              </Field>
            ) : null}
            <Button type="submit" disabled={!token || !username.trim()}>
              {t("common.save")}
            </Button>
          </fieldset>
        </form>
      )}
      {save.isError ? (
        <p role="alert" className="inline-error">
          {t("operator.saveFailed")}
        </p>
      ) : null}
      {save.isSuccess ? <p role="status">{t("operator.saved")}</p> : null}
    </section>
  );
}

/** Once per browser session until configured; never touches team login identity. */
export function OperatorUpgradePrompt({ onReady }: { onReady?: (ready: boolean) => void } = {}) {
  const { client, role } = useSession();
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [dismissed, setDismissed] = useState(() => {
    try {
      return sessionStorage.getItem("operator-name-dismissed") === "1";
    } catch {
      return false;
    }
  });
  const profile = useQuery({
    queryKey: ["operator-profile"],
    queryFn: ({ signal }) => client!.get<Profile>("/admin/operator-profile", signal),
    enabled: Boolean(client) && role === null && !dismissed,
  });
  const close = () => {
    setDismissed(true);
    try {
      sessionStorage.setItem("operator-name-dismissed", "1");
    } catch {
      /* optional */
    }
  };
  const visible = Boolean(
    client && role === null && !dismissed && profile.data?.username && !profile.data.configured,
  );
  const resolved = dismissed || !client || role !== null || !profile.isPending;
  useEffect(() => {
    onReady?.(resolved && !visible);
  }, [onReady, resolved, visible]);
  if (!client || role !== null || dismissed || !profile.data?.username || profile.data.configured)
    return null;
  return (
    <Dialog title={t("operator.title")} onClose={close} busy={busy}>
      <OperatorProfilePanel onSaved={close} onBusyChange={setBusy} />
      <Button variant="quiet" onClick={close}>
        {t("operator.later")}
      </Button>
    </Dialog>
  );
}
