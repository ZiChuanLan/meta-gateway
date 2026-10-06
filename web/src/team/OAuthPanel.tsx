import { useEffect, useState } from "react";
import { teamError, type TeamText } from "./text";
import type { OAuthSettings, Policy, TeamRequest } from "./types";

/**
 * Sign-in providers: which third parties may authenticate a member, and what
 * happens the first time one arrives.
 *
 * A provider is only offered on the login page once it is enabled AND has a
 * client id + secret — the console says so explicitly, because a half-filled
 * card is the most likely reason a button does not appear.
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
  const [secrets, setSecrets] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [copied, setCopied] = useState("");

  useEffect(() => {
    let active = true;
    void request<OAuthSettings>("/admin/team/oauth")
      .then((data) => {
        if (active) setDraft(data);
      })
      .catch((e) => {
        if (active) setError(e);
      });
    return () => {
      active = false;
    };
  }, [request]);

  if (error && !draft) {
    return (
      <div className="team-error" role="alert">
        {teamError(error, locale)}
      </div>
    );
  }
  if (!draft) return <p>{t("load")}</p>;

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
        body: JSON.stringify({
          auto_register: draft.auto_register,
          default_policy_id: draft.default_policy_id,
          providers: draft.providers.map((item) => ({
            id: item.id,
            enabled: item.enabled,
            client_id: item.client_id,
            // Left blank on purpose: the server keeps the stored secret.
            client_secret: secrets[item.id] ?? "",
            authorize_url: item.authorize_url,
            token_url: item.token_url,
            userinfo_url: item.userinfo_url,
            scopes: item.scopes,
          })),
        }),
      });
      setDraft(result);
      setSecrets({});
      setNotice(t("success"));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="team-oauth">
      <div className="team-head">
        <div>
          <h3 style={{ margin: 0 }}>{t("oauth")}</h3>
          <p className="team-muted" style={{ margin: "4px 0 0" }}>
            {t("oauthHint")}
          </p>
        </div>
        <button className="team-button primary" disabled={busy} onClick={() => void save()}>
          {busy ? t("saving") : t("save")}
        </button>
      </div>

      {error ? (
        <div className="team-error" role="alert">
          {teamError(error, locale)}
        </div>
      ) : null}
      {notice ? (
        <div className="team-notice" role="status">
          {notice}
        </div>
      ) : null}

      <fieldset className="team-section">
        <legend>{t("oauthRegistration")}</legend>
        <label className="check">
          <input
            type="checkbox"
            checked={draft.auto_register}
            onChange={(e) => setDraft({ ...draft, auto_register: e.target.checked })}
          />
          <span>{t("oauthAutoRegister")}</span>
        </label>
        <p className="team-muted">{t("oauthAutoRegisterHint")}</p>
        <label className="team-field">
          <span>{t("oauthDefaultPolicy")}</span>
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
        </label>
      </fieldset>

      {draft.providers.map((provider) => {
        const ready = provider.enabled && provider.client_id && provider.has_secret;
        return (
          <fieldset className="team-section team-oauth-card" key={provider.id}>
            <legend>{provider.label}</legend>
            <label className="check">
              <input
                type="checkbox"
                checked={provider.enabled}
                onChange={(e) => patch(provider.id, { enabled: e.target.checked })}
              />
              <span>{t("oauthEnable", { name: provider.label })}</span>
            </label>

            <div className="team-callback">
              <code>{provider.callback_url}</code>
              <button
                className="team-button quiet"
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
              </button>
            </div>
            <p className="team-muted">{t("oauthCallbackHint")}</p>

            <div className="team-grid">
              <label className="team-field">
                <span>{t("oauthClientID")}</span>
                <input
                  value={provider.client_id}
                  maxLength={200}
                  autoComplete="off"
                  onChange={(e) => patch(provider.id, { client_id: e.target.value })}
                />
              </label>
              <label className="team-field">
                <span>
                  {t("oauthClientSecret")}
                  {provider.has_secret ? ` · ${t("oauthSecretStored")}` : ""}
                </span>
                <input
                  type="password"
                  autoComplete="new-password"
                  placeholder={provider.has_secret ? t("oauthSecretKeep") : ""}
                  value={secrets[provider.id] ?? ""}
                  onChange={(e) => setSecrets({ ...secrets, [provider.id]: e.target.value })}
                />
              </label>
              <label className="team-field">
                <span>{t("oauthScopes")}</span>
                <input
                  value={provider.scopes}
                  placeholder={provider.default_scopes}
                  onChange={(e) => patch(provider.id, { scopes: e.target.value })}
                />
              </label>
            </div>

            <details className="team-oauth-advanced">
              <summary>{t("oauthAdvanced")}</summary>
              <p className="team-muted">{t("oauthAdvancedHint")}</p>
              <div className="team-grid">
                <label className="team-field">
                  <span>{t("oauthAuthorizeURL")}</span>
                  <input
                    value={provider.authorize_url}
                    placeholder={provider.default_authorize_url}
                    onChange={(e) => patch(provider.id, { authorize_url: e.target.value })}
                  />
                </label>
                <label className="team-field">
                  <span>{t("oauthTokenURL")}</span>
                  <input
                    value={provider.token_url}
                    placeholder={provider.default_token_url}
                    onChange={(e) => patch(provider.id, { token_url: e.target.value })}
                  />
                </label>
                <label className="team-field">
                  <span>{t("oauthUserInfoURL")}</span>
                  <input
                    value={provider.userinfo_url}
                    placeholder={provider.default_userinfo_url}
                    onChange={(e) => patch(provider.id, { userinfo_url: e.target.value })}
                  />
                </label>
              </div>
            </details>

            <p className="team-muted">{ready ? t("oauthReady") : t("oauthNotReady")}</p>
          </fieldset>
        );
      })}
    </section>
  );
}
