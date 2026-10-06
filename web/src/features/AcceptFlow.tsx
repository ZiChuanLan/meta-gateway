import { useState, type FormEvent } from "react";
import { Button, Field } from "../components/ui";
import { useI18n } from "../i18n";
import { accountRequest } from "../team/transport";
import { teamError, type TeamText } from "../team/text";

/**
 * Accepting an invitation, redeeming a code, or setting a new password.
 *
 * These three are one form: they differ in which fields exist and which
 * endpoint the values go to, not in layout. They used to live in the member
 * app's sign-in page; with one console they belong on the console's own, which
 * is where the links now point.
 *
 * The component owns its request and reports success upward, so the host only
 * has to adopt the session it just created.
 */
export function AcceptFlow({
  invite,
  recovery,
  manual,
  t,
  onDone,
  onBack,
}: {
  /** Invitation token from the link, if any. */
  invite: string;
  /** Recovery token from the link, if any. */
  recovery: string;
  /** The visitor chose "I have a code" instead of arriving through a link. */
  manual: boolean;
  t: TeamText;
  /** Called once the credentials were accepted: the session exists. */
  onDone: () => void;
  /** Back to the ordinary sign-in form. */
  onBack: () => void;
}) {
  const { locale } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const redeeming = !recovery && !invite && manual;
  const signingUp = Boolean(invite) || redeeming;
  const title = t(recovery ? "recover" : "accept");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError(null);
    try {
      if (recovery) {
        await accountRequest("/auth/recover", {
          method: "POST",
          body: JSON.stringify({
            token: recovery,
            password: form.get("password"),
          }),
        });
      } else {
        await accountRequest("/auth/accept", {
          method: "POST",
          body: JSON.stringify({
            username: form.get("username"),
            name: form.get("name"),
            password: form.get("password"),
            token: invite || form.get("code"),
          }),
        });
      }
      onDone();
    } catch (failure) {
      setError(failure);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} aria-busy={busy}>
      <fieldset disabled={busy} style={{ border: 0, padding: 0, margin: 0, display: "contents" }}>
        {!recovery && (
          <Field label={t("username")} hint={t("userHint")}>
            <input name="username" required minLength={3} maxLength={64} autoComplete="username" />
          </Field>
        )}
        {signingUp && (
          <Field label={t("name")}>
            <input name="name" required maxLength={80} autoComplete="name" />
          </Field>
        )}
        {redeeming && (
          <Field label={t("code")} hint={t("codeHint")}>
            <input
              name="code"
              required
              maxLength={64}
              autoComplete="off"
              placeholder="XXXX-XXXX-XXXX-XXXX"
            />
          </Field>
        )}
        <Field label={t("password")} hint={signingUp || recovery ? t("passwordHint") : undefined}>
          <input
            name="password"
            type="password"
            required
            autoComplete={signingUp || recovery ? "new-password" : "current-password"}
          />
        </Field>
        {error ? (
          <div className="inline-error" role="alert">
            {teamError(error, locale)}
          </div>
        ) : null}
        <Button type="submit" loading={busy} disabled={busy} className="login-submit">
          {busy ? t("load") : title}
        </Button>
        <button type="button" className="login-back" disabled={busy} onClick={onBack}>
          {t("backLogin")}
        </button>
      </fieldset>
    </form>
  );
}
