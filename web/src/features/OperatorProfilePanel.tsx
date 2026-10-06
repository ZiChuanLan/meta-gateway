import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Dialog, Field, Loading } from "../components/ui";
import { useI18n } from "../i18n";
import { useSession } from "../session";

/**
 * The deployment owner's own credential: one username plus a stored password,
 * instead of the admin token typed as a password.
 *
 * `required` is answered by the server (no owner account exists yet), which is
 * what makes the upgrade window close by itself: the same panel is the claim
 * form on a fresh deployment and the "change my username / password" form once
 * the account exists.
 */
type Onboarding = {
  required: boolean;
  username: string;
  has_owner: boolean;
  token_login: boolean;
  totp_login: boolean;
  mode: string;
};

export function OperatorClaimPanel({
  onSaved,
  onBusyChange,
}: {
  onSaved?: () => void;
  onBusyChange?: (busy: boolean) => void;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const qc = useQueryClient();
  const state = useQuery({
    queryKey: ["operator-onboarding"],
    queryFn: ({ signal }) => client!.get<Onboarding>("/admin/operator/onboarding", signal),
  });
  const [name, setName] = useState<string | null>(null);
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [token, setToken] = useState("");
  const [code, setCode] = useState("");
  const save = useMutation({
    mutationFn: () =>
      client!.post("/admin/operator/claim", {
        username: name ?? state.data?.username,
        password,
        token,
        totp_code: code,
      }),
    onSuccess: async () => {
      setPassword("");
      setConfirm("");
      setToken("");
      setCode("");
      await qc.invalidateQueries({ queryKey: ["operator-onboarding"] });
      onSaved?.();
    },
  });
  useEffect(() => {
    onBusyChange?.(save.isPending);
  }, [onBusyChange, save.isPending]);
  const username = name ?? state.data?.username ?? "";
  const mismatch = confirm.length > 0 && password !== confirm;
  if (state.isPending) return <Loading />;
  const claiming = state.data?.required ?? false;
  return (
    <section className="operator-profile-panel">
      <p className="field-hint">{claiming ? t("operator.claimHint") : t("operator.changeHint")}</p>
      {state.isError ? (
        <p role="alert">{t("operator.loadFailed")}</p>
      ) : (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            if (!save.isPending && !mismatch) save.mutate();
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
            <Field label={t("operator.newPassword")}>
              <input
                type="password"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </Field>
            <Field label={t("operator.confirmNewPassword")}>
              <input
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                required
                aria-invalid={mismatch || undefined}
              />
            </Field>
            {/* Visible text, not Field's `hint`: that one renders an InfoTip, and
                "the passwords differ" must be readable without hovering. */}
            {mismatch ? (
              <p role="alert" className="inline-error">
                {t("operator.passwordMismatch")}
              </p>
            ) : null}
            <p className="field-hint">{t("operator.confirmTokenHint")}</p>
            <Field label={t("operator.confirmToken")}>
              <input
                type="password"
                autoComplete="current-password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                required
              />
            </Field>
            {state.data?.totp_login ? (
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
            <Button type="submit" disabled={!token || !username.trim() || !password || mismatch}>
              {claiming ? t("operator.claimSubmit") : t("common.save")}
            </Button>
          </fieldset>
        </form>
      )}
      {save.isError ? (
        <p role="alert" className="inline-error">
          {t("operator.saveFailed")}
        </p>
      ) : null}
      {save.isSuccess ? <p role="status">{t("operator.claimed")}</p> : null}
    </section>
  );
}

/**
 * The first-run prompt: once per browser session, while the deployment has no
 * owner account. It is the console's half of the upgrade path — the sign-in
 * page's entry gets the operator in with the token, this one gives the gateway a
 * real credential, and the token stops being a password afterwards.
 */
export function OperatorUpgradePrompt({ onReady }: { onReady?: (ready: boolean) => void } = {}) {
  const { client, role } = useSession();
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [dismissed, setDismissed] = useState(() => {
    try {
      return sessionStorage.getItem("operator-claim-dismissed") === "1";
    } catch {
      return false;
    }
  });
  const state = useQuery({
    queryKey: ["operator-onboarding"],
    queryFn: ({ signal }) => client!.get<Onboarding>("/admin/operator/onboarding", signal),
    // Only the deployment principal can claim; a member or a limited admin has
    // nothing to answer here.
    enabled: Boolean(client) && (role === null || role === "owner") && !dismissed,
  });
  const close = () => {
    setDismissed(true);
    try {
      sessionStorage.setItem("operator-claim-dismissed", "1");
    } catch {
      /* optional */
    }
  };
  const visible = Boolean(
    client &&
    (role === null || role === "owner") &&
    !dismissed &&
    !state.isError &&
    state.data?.required,
  );
  const resolved = dismissed || !client || (role !== null && role !== "owner") || !state.isPending;
  useEffect(() => {
    onReady?.(resolved && !visible);
  }, [onReady, resolved, visible]);
  if (!visible) return null;
  return (
    <Dialog title={t("operator.claimTitle")} onClose={close} busy={busy}>
      <OperatorClaimPanel onSaved={close} onBusyChange={setBusy} />
      <Button variant="quiet" onClick={close}>
        {t("operator.later")}
      </Button>
    </Dialog>
  );
}
