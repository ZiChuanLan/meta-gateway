import { useEffect, useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Field, Panel } from "../components/ui";
import { formatCost } from "../lib/format";
import { accountRequest } from "../team/transport";
import { teamError, type TeamText } from "../team/text";
import type { Account, PreferencesView, RequestPreferences } from "../team/types";
import { useI18n } from "../i18n";
import { APPEARANCES, type Appearance } from "../appearance";
import { UI_THEMES } from "../themes/registry";

type Session = {
  id: string;
  created_at: number;
  expires_at: number;
  current: boolean;
};

/**
 * Personal settings: request controls, display preferences and the account.
 *
 * Request controls are the only team-level knob a member may touch (the owner
 * grants `allow_request_preferences` on the policy), and they can only tighten
 * the site rules — never loosen them. The account panel is where a member
 * changes their own password and revokes sessions, which is also why changing
 * the password signs every session out.
 */
export function SettingsPage({
  account,
  appearance,
  onAppearance,
  scheme,
  onScheme,
  density,
  onDensity,
  t,
  onDisconnected,
}: {
  account: Account;
  appearance: Appearance;
  onAppearance: (value: Appearance) => void;
  scheme: string;
  onScheme: (value: string) => void;
  density: string;
  onDensity: (value: string) => void;
  t: TeamText;
  onDisconnected: () => void;
}) {
  const { locale, t: consoleT } = useI18n();
  const userID = account.user.id;
  const [draft, setDraft] = useState<RequestPreferences>({
    failover: "inherit",
    max_retries: null,
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [saved, setSaved] = useState(false);

  const preferences = useQuery({
    queryKey: ["user", userID, "preferences"],
    queryFn: ({ signal }) => accountRequest<PreferencesView>("/me/preferences", { signal }),
  });
  const [redeemCode, setRedeemCode] = useState("");
  const [redeemNotice, setRedeemNotice] = useState("");
  const [credit, setCredit] = useState(account.credit);
  useEffect(() => {
    if (preferences.data) setDraft(preferences.data.preferences);
  }, [preferences.data]);

  const sessions = useQuery({
    queryKey: ["user", userID, "sessions"],
    queryFn: ({ signal }) => accountRequest<Session[]>("/me/sessions", { signal }),
  });

  async function savePreferences(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      await accountRequest("/me/preferences", {
        method: "PUT",
        body: JSON.stringify(draft),
      });
      await preferences.refetch();
      setSaved(true);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  async function run(path: string, method: string, body?: unknown) {
    setBusy(true);
    setError(null);
    try {
      await accountRequest(path, {
        method,
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      await sessions.refetch();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="page">
      <header className="page-header">
        <div className="page-heading">
          <h1>{t("personalSettings")}</h1>
          <p>{t("personalSettingsHint")}</p>
        </div>
      </header>

      <Panel title={t("credit")} titleHelp={t("creditHint")}>
        <div className="user-credit">
          <div>
            <span className="workspace-caption">{t("creditRemaining")}</span>
            <strong>{credit.unlimited ? t("unlimited") : credit.available.toLocaleString()}</strong>
          </div>
          <div>
            <span className="workspace-caption">{t("creditUsed")}</span>
            <strong>{credit.used.toLocaleString()}</strong>
          </div>
          <div>
            <span className="workspace-caption">{t("creditTotal")}</span>
            <strong>{credit.unlimited ? t("unlimited") : credit.total.toLocaleString()}</strong>
          </div>
          {/* The spend budget is a separate allowance: a member can be capped
              by money while tokens are unlimited, and vice versa. */}
          <div>
            <span className="workspace-caption">{t("creditCostRemaining")}</span>
            <strong>
              {credit.cost_unlimited ? t("unlimited") : formatCost(credit.cost_available)}
            </strong>
          </div>
          <div>
            <span className="workspace-caption">{t("creditCostUsed")}</span>
            <strong>{formatCost(credit.cost_used)}</strong>
          </div>
        </div>
        <form
          className="user-redeem"
          onSubmit={(event) => {
            event.preventDefault();
            setBusy(true);
            setError(null);
            setRedeemNotice("");
            void accountRequest<{
              granted: number;
              cost_granted: number;
              quota_total: number;
              quota_used: number;
              quota_available: number;
              cost_total: number;
              cost_used: number;
            }>("/me/redeem", {
              method: "POST",
              body: JSON.stringify({ code: redeemCode }),
            })
              .then((result) => {
                setCredit({
                  total: result.quota_total,
                  used: result.quota_used,
                  available: result.quota_available,
                  unlimited: result.quota_total <= 0,
                  cost_total: result.cost_total,
                  cost_used: result.cost_used,
                  cost_available: Math.max(result.cost_total - result.cost_used, 0),
                  cost_unlimited: result.cost_total <= 0,
                });
                setRedeemCode("");
                setRedeemNotice(
                  result.cost_granted > 0
                    ? t("redeemSuccessCost", {
                        amount: formatCost(result.cost_granted),
                      })
                    : t("redeemSuccess", { n: result.granted }),
                );
              })
              .catch(setError)
              .finally(() => setBusy(false));
          }}
        >
          <Field label={t("redeemCode")} hint={t("redeemHint")}>
            <input
              value={redeemCode}
              onChange={(e) => setRedeemCode(e.target.value)}
              placeholder="XXXX-XXXX-XXXX-XXXX"
              required
              maxLength={64}
              autoComplete="off"
            />
          </Field>
          <Button type="submit" loading={busy} disabled={busy || !redeemCode}>
            {t("redeem")}
          </Button>
        </form>
        {redeemNotice ? (
          <p role="status" className="workspace-note">
            {redeemNotice}
          </p>
        ) : null}
      </Panel>

      <Panel
        title={t("requestControls")}
        titleHelp={t("requestControlsHint")}
        actions={
          preferences.data?.can_edit ? (
            <Button type="submit" form="preferences-form" loading={busy} disabled={busy}>
              {t("save")}
            </Button>
          ) : undefined
        }
      >
        {preferences.isPending ? (
          <p className="workspace-note">{t("load")}</p>
        ) : preferences.error ? (
          <div className="team-error" role="alert">
            {teamError(preferences.error, locale)}{" "}
            <Button variant="quiet" onClick={() => void preferences.refetch()}>
              {t("retry")}
            </Button>
          </div>
        ) : preferences.data ? (
          <>
            <p className="workspace-note" style={{ marginTop: 0 }}>
              {preferences.data.can_edit ? t("requestControlsHint") : t("managedByOwner")}
            </p>
            <form id="preferences-form" onSubmit={savePreferences}>
              <fieldset
                className="form-grid"
                disabled={busy || !preferences.data.can_edit}
                style={{ border: 0, margin: 0, padding: 0 }}
              >
                <label className="field">
                  <span className="field-label">{t("failover")}</span>
                  <select
                    value={draft.failover}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        failover: e.target.value as RequestPreferences["failover"],
                      })
                    }
                  >
                    <option value="inherit">{t("inherit")}</option>
                    <option value="on">{t("failoverOn")}</option>
                    <option value="off">{t("failoverOff")}</option>
                  </select>
                </label>
                <label className="field">
                  <span className="field-label">{t("retryLimit")}</span>
                  <input
                    type="number"
                    min={0}
                    max={100}
                    placeholder={t("inherit")}
                    value={draft.max_retries ?? ""}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        max_retries: e.target.value === "" ? null : Number(e.target.value),
                      })
                    }
                  />
                </label>
              </fieldset>
              {error ? (
                <div className="team-error" role="alert">
                  {teamError(error, locale)}
                </div>
              ) : null}
              {saved ? (
                <p role="status" className="workspace-note">
                  {t("success")}
                </p>
              ) : null}
              <div className="user-setting-effective">
                <span>
                  {t("siteDefault")}: {preferences.data.site.failover_enabled ? t("on") : t("off")}{" "}
                  · {preferences.data.site.retry_times}
                </span>
                <span>
                  {t("effectiveDefault")}:{" "}
                  {preferences.data.effective.failover_enabled ? t("on") : t("off")} ·{" "}
                  {preferences.data.effective.retry_times}
                </span>
              </div>
              <p className="workspace-note">{t("retrySafetyHint")}</p>
            </form>
          </>
        ) : null}
      </Panel>

      <Panel title={t("displayPreferences")}>
        <div className="form-grid">
          {/* The member app wears the console's themes, so the picker offers
              the same two sheets by name. */}
          <label className="field">
            <span className="field-label">{t("interfaceTheme")}</span>
            <select value={appearance} onChange={(e) => onAppearance(e.target.value as Appearance)}>
              {APPEARANCES.map((style) => (
                <option key={style} value={style}>
                  {/* The theme names live in the console's shared dictionary
                      (they are the same sheets the console offers), not in
                      the team copy — teamText would render them blank. */}
                  {consoleT(UI_THEMES[style].nameKey)}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span className="field-label">{t("colorScheme")}</span>
            <select value={scheme} onChange={(e) => onScheme(e.target.value)}>
              <option value="light">{t("light")}</option>
              <option value="dark">{t("dark")}</option>
            </select>
          </label>
          <label className="field">
            <span className="field-label">{t("density")}</span>
            <select value={density} onChange={(e) => onDensity(e.target.value)}>
              <option value="comfortable">{t("comfortable")}</option>
              <option value="compact">{t("compact")}</option>
            </select>
          </label>
        </div>
      </Panel>

      <Panel title={t("account")}>
        <div
          className="user-setting-effective"
          style={{ marginTop: 0, paddingTop: 0, borderTop: 0 }}
        >
          <span>
            {account.user.name} · {account.user.username}
          </span>
          <span>
            {t("role")}: {t(account.user.role)} · {t("policy")}: {account.policy.name}
          </span>
        </div>
        <form
          className="form-grid"
          onSubmit={(event) => {
            event.preventDefault();
            const form = new FormData(event.currentTarget);
            setBusy(true);
            setError(null);
            void accountRequest("/me/password", {
              method: "POST",
              body: JSON.stringify({
                current_password: form.get("current"),
                password: form.get("password"),
              }),
            })
              .then(() => onDisconnected())
              .catch(setError)
              .finally(() => setBusy(false));
          }}
        >
          <label className="field">
            <span className="field-label">{t("currentPassword")}</span>
            <input name="current" type="password" required autoComplete="current-password" />
          </label>
          <label className="field">
            <span className="field-label">{t("password")}</span>
            <input name="password" type="password" required autoComplete="new-password" />
            <span className="field-hint">{t("passwordHint")}</span>
          </label>
          <div className="wide">
            <Button type="submit" variant="secondary" loading={busy} disabled={busy}>
              {t("changePassword")}
            </Button>
          </div>
        </form>
        {error ? (
          <div className="team-error" role="alert">
            {teamError(error, locale)}
          </div>
        ) : null}
        <h3 style={{ margin: "26px 0 10px", fontSize: 13 }}>{t("sessions")}</h3>
        {sessions.error ? (
          <div className="team-error" role="alert">
            {teamError(sessions.error, locale)}
          </div>
        ) : null}
        <div className="table-wrap" data-columns="3">
          <table>
            <thead>
              <tr>
                <th>{t("created")}</th>
                <th>{t("status")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {(sessions.data ?? []).map((session) => (
                <tr key={session.id}>
                  <td>{new Date(session.created_at * 1000).toLocaleString(locale)}</td>
                  <td>{session.current ? t("currentSession") : "—"}</td>
                  <td>
                    <Button
                      variant="secondary"
                      disabled={busy}
                      onClick={() =>
                        void run(`/me/sessions/${session.id}`, "DELETE").then(() => {
                          if (session.current) onDisconnected();
                        })
                      }
                    >
                      {t("revoke")}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>
    </div>
  );
}
