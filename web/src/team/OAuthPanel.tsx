import { useEffect, useRef, useState } from "react";
import { Button, ErrorState, Field, Panel } from "../components/ui";
import { useUnsavedChanges } from "../lib/unsavedChanges";
import { teamError, type TeamText } from "./text";
import type { OAuthSettings, Policy, TeamRequest } from "./types";

/**
 * Exactly the fields `save` sends. "Unsaved" is defined by this shape, so the
 * dirty flag can never disagree with what the next PUT would change: a blank
 * secret (the server keeps the stored one) is not an edit, and a save that
 * succeeds returns this payload to clean in one step.
 */
function oauthPayload(settings: OAuthSettings, secrets: Record<string, string>) {
  return {
    auto_register: settings.auto_register,
    default_policy_id: settings.default_policy_id,
    providers: settings.providers.map((provider) => ({
      id: provider.id,
      enabled: provider.enabled,
      client_id: provider.client_id,
      client_secret: secrets[provider.id] ?? "",
      authorize_url: provider.authorize_url,
      token_url: provider.token_url,
      userinfo_url: provider.userinfo_url,
      scopes: provider.scopes,
    })),
  };
}

/**
 * Sign-in providers: which third parties may authenticate a member, and what
 * happens the first time one arrives.
 *
 * A provider is only offered on the login page once it is enabled AND has a
 * client id + secret — the console says so explicitly, because a half-filled
 * card is the most likely reason a button does not appear.
 *
 * The board is a Panel whose header carries the save action, so the operator does
 * not have to scroll back to the top to commit a change made in the last card;
 * the provider cards stay fieldsets, because that is what they are (a named group
 * of settings), but every control inside them is the shared one.
 */
export function OAuthPanel({
  request,
  policies,
  locale,
  t,
}: {
  request: TeamRequest;
  policies: Policy[];
  locale: string;
  t: TeamText;
}) {
  const [draft, setDraft] = useState<OAuthSettings | null>(null);
  // The last server-confirmed document; dirty means the draft drifted from it.
  const [baseline, setBaseline] = useState<OAuthSettings | null>(null);
  const [secrets, setSecrets] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [copied, setCopied] = useState("");
  // A document that lands while the operator is mid-edit must not overwrite
  // their input; only the first payload — or one arriving while nothing is
  // unsaved — is adopted.
  const editedRef = useRef(false);

  useEffect(() => {
    let active = true;
    void request<OAuthSettings>("/admin/team/oauth")
      .then((data) => {
        if (!active || editedRef.current) return;
        setDraft(data);
        setBaseline(data);
      })
      .catch((e) => {
        if (active) setError(e);
      });
    return () => {
      active = false;
    };
  }, [request]);

  const dirty =
    draft !== null &&
    baseline !== null &&
    JSON.stringify(oauthPayload(draft, secrets)) !== JSON.stringify(oauthPayload(baseline, {}));
  editedRef.current = dirty;
  // Leaving this board — through the sidebar, the command palette or any other
  // in-app move — must not throw the half-filled form away without a word.
  useUnsavedChanges(dirty);

  if (error && !draft) {
    return <ErrorState error={error} />;
  }
  if (!draft) return <p className="panel-hint">{t("load")}</p>;

  const patch = (providerId: string, values: Partial<OAuthSettings["providers"][number]>) =>
    setDraft({
      ...draft,
      providers: draft.providers.map((item) =>
        item.id === providerId ? { ...item, ...values } : item,
      ),
    });

  async function save() {
    if (!draft) return;
    setBusy(true);
    setError(null);
    setNotice("");
    try {
      const result = await request<OAuthSettings>("/admin/team/oauth", {
        method: "PUT",
        body: JSON.stringify(oauthPayload(draft, secrets)),
      });
      setDraft(result);
      setBaseline(result);
      setSecrets({});
      setNotice(t("success"));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="team-board">
      <Panel
        title={t("oauth")}
        titleHelp={t("oauthHint")}
        actions={
          <Button loading={busy} disabled={busy} onClick={() => void save()}>
            {t("save")}
          </Button>
        }
      >
        {error ? (
          <p className="inline-error" role="alert">
            {teamError(error, locale)}
          </p>
        ) : null}
        {notice ? (
          <p className="notice" role="status">
            {notice}
          </p>
        ) : null}

        <fieldset className="team-section">
          <legend>{t("oauthRegistration")}</legend>
          <label className="check marginless">
            <input
              type="checkbox"
              checked={draft.auto_register}
              onChange={(e) => setDraft({ ...draft, auto_register: e.target.checked })}
            />
            <span>{t("oauthAutoRegister")}</span>
          </label>
          <p className="panel-hint">{t("oauthAutoRegisterHint")}</p>
          <Field label={t("oauthDefaultPolicy")}>
            <select
              value={draft.default_policy_id}
              disabled={!draft.auto_register}
              onChange={(e) => setDraft({ ...draft, default_policy_id: Number(e.target.value) })}
            >
              <option value={0}>{t("oauthFirstPolicy")}</option>
              {policies.map((policy) => (
                <option key={policy.id} value={policy.id}>
                  {policy.name}
                </option>
              ))}
            </select>
          </Field>
        </fieldset>

        {draft.providers.map((provider) => {
          const ready = provider.enabled && provider.client_id && provider.has_secret;
          return (
            <fieldset className="team-section team-oauth-card" key={provider.id}>
              <legend>{provider.label}</legend>
              <label className="check marginless">
                <input
                  type="checkbox"
                  checked={provider.enabled}
                  onChange={(e) => patch(provider.id, { enabled: e.target.checked })}
                />
                <span>{t("oauthEnable", { name: provider.label })}</span>
              </label>

              <div className="team-callback">
                <code>{provider.callback_url}</code>
                <Button
                  variant="quiet"
                  onClick={() => {
                    void navigator.clipboard
                      .writeText(provider.callback_url)
                      .then(() => {
                        setCopied(provider.id);
                        window.setTimeout(() => setCopied(""), 1600);
                      })
                      .catch(() => setCopied(""));
                  }}
                >
                  {copied === provider.id ? t("copied") : t("copy")}
                </Button>
              </div>
              <p className="panel-hint">{t("oauthCallbackHint")}</p>

              <div className="meta-form">
                <Field label={t("oauthClientID")}>
                  <input
                    value={provider.client_id}
                    maxLength={200}
                    autoComplete="off"
                    onChange={(e) => patch(provider.id, { client_id: e.target.value })}
                  />
                </Field>
                <Field
                  label={
                    provider.has_secret
                      ? `${t("oauthClientSecret")} · ${t("oauthSecretStored")}`
                      : t("oauthClientSecret")
                  }
                >
                  <input
                    type="password"
                    autoComplete="new-password"
                    placeholder={provider.has_secret ? t("oauthSecretKeep") : ""}
                    value={secrets[provider.id] ?? ""}
                    onChange={(e) => setSecrets({ ...secrets, [provider.id]: e.target.value })}
                  />
                </Field>
                <Field label={t("oauthScopes")}>
                  <input
                    value={provider.scopes}
                    placeholder={provider.default_scopes}
                    onChange={(e) => patch(provider.id, { scopes: e.target.value })}
                  />
                </Field>
              </div>

              <details className="team-oauth-advanced">
                <summary>{t("oauthAdvanced")}</summary>
                <p className="panel-hint">{t("oauthAdvancedHint")}</p>
                <div className="meta-form">
                  <Field label={t("oauthAuthorizeURL")}>
                    <input
                      value={provider.authorize_url}
                      placeholder={provider.default_authorize_url}
                      onChange={(e) => patch(provider.id, { authorize_url: e.target.value })}
                    />
                  </Field>
                  <Field label={t("oauthTokenURL")}>
                    <input
                      value={provider.token_url}
                      placeholder={provider.default_token_url}
                      onChange={(e) => patch(provider.id, { token_url: e.target.value })}
                    />
                  </Field>
                  <Field label={t("oauthUserInfoURL")}>
                    <input
                      value={provider.userinfo_url}
                      placeholder={provider.default_userinfo_url}
                      onChange={(e) => patch(provider.id, { userinfo_url: e.target.value })}
                    />
                  </Field>
                </div>
              </details>

              <p className="panel-hint">{ready ? t("oauthReady") : t("oauthNotReady")}</p>
            </fieldset>
          );
        })}
      </Panel>
    </div>
  );
}
