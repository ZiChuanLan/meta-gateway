import { useEffect, useState, type FormEvent } from "react";
import { Button, Field, Panel } from "../../components/ui";
import { useUnsavedChanges } from "../../lib/unsavedChanges";
import { teamError } from "../text";
import { useTeamMutation } from "../useTeamMutation";
import { useUsers } from "../UsersContext";
import type { TeamSettings } from "../types";

/**
 * What a member sees when they sign in: the console's title, accent, logo, the
 * API address shown in the connect dialog, and which boards they get.
 *
 * This is the branding half of the team settings record — the operating mode
 * lives on its own board (OverviewPanel). Both write the same document, so the
 * form starts from the settings the shell already loaded rather than from a
 * partial one: that is what keeps a branding save from blanking the mode.
 */
export function BrandingPanel() {
  const { request, t, settings } = useUsers();
  const { busy, error, run } = useTeamMutation(request, t);
  return (
    <BrandingForm
      value={settings}
      busy={busy}
      error={error}
      onSave={async (value) => {
        return run<TeamSettings>("/admin/team/settings", "PUT", value);
      }}
    />
  );
}

function BrandingForm({
  value,
  busy,
  error,
  onSave,
}: {
  value: TeamSettings;
  busy: boolean;
  error?: unknown;
  onSave: (value: TeamSettings) => Promise<TeamSettings | undefined>;
}) {
  const { t, locale } = useUsers();
  const [draft, setDraft] = useState(value);
  const [baseline, setBaseline] = useState(JSON.stringify(value));
  const dirty = JSON.stringify(draft) !== baseline;
  useUnsavedChanges(dirty);
  useEffect(() => {
    if (!dirty && !busy) {
      setDraft(value);
      setBaseline(JSON.stringify(value));
    }
  }, [value]); // eslint-disable-line react-hooks/exhaustive-deps
  const b = draft.branding;
  const patch = (partial: Partial<typeof b>) =>
    setDraft({ ...draft, branding: { ...b, ...partial } });
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const result = await onSave(draft);
    if (result) {
      setDraft(result);
      setBaseline(JSON.stringify(result));
    }
  };
  return (
    <form onSubmit={submit}>
      <Panel title={t("branding")}>
        <fieldset className="overlay-fields" disabled={busy}>
          <div className="meta-form">
            <Field label={t("publicName")}>
              <input
                value={b.name}
                maxLength={80}
                required
                onChange={(e) => patch({ name: e.target.value })}
              />
            </Field>
            <Field label={t("accent")}>
              <input
                type="color"
                value={b.accent}
                onChange={(e) => patch({ accent: e.target.value })}
              />
            </Field>
            <Field label={t("logo")}>
              <input
                type="url"
                value={b.logo_url}
                onChange={(e) => patch({ logo_url: e.target.value })}
              />
            </Field>
            <Field label={t("apiURL")}>
              <input
                type="url"
                value={b.api_base_url}
                placeholder="https://gateway.example/v1"
                onChange={(e) => patch({ api_base_url: e.target.value })}
              />
            </Field>
          </div>
          <Field label={t("loginDescription")}>
            <textarea
              value={b.login_description}
              maxLength={1000}
              onChange={(e) => patch({ login_description: e.target.value })}
            />
          </Field>
          <Field label={t("notice")}>
            <textarea
              value={b.notice}
              maxLength={2000}
              onChange={(e) => patch({ notice: e.target.value })}
            />
          </Field>
          <label className="check marginless">
            <input
              type="checkbox"
              checked={b.show_usage}
              onChange={(e) => patch({ show_usage: e.target.checked })}
            />
            <span>{t("showUsage")}</span>
          </label>
          <label className="check marginless">
            <input
              type="checkbox"
              checked={b.show_routing}
              onChange={(e) => patch({ show_routing: e.target.checked })}
            />
            <span>{t("showRouting")}</span>
          </label>
        </fieldset>
        {error ? (
          <p role="alert" className="inline-error">
            {teamError(error, locale)}
          </p>
        ) : null}
        <div className="form-actions">
          <Button type="submit" loading={busy} disabled={!dirty}>
            {t("save")}
          </Button>
        </div>
      </Panel>
    </form>
  );
}
