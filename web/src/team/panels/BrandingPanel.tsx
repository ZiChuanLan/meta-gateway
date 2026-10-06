import { useState, type FormEvent } from "react";
import { TeamField as Field } from "../ui";
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
      // Remount when the stored settings change: the form is a draft, and a
      // remount is what makes an external update visible instead of silently
      // stale in the fields.
      key={JSON.stringify(settings)}
      value={settings}
      busy={busy}
      error={error}
      onSave={(value) => void run("/admin/team/settings", "PUT", value)}
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
  onSave: (value: TeamSettings) => void;
}) {
  const { t, locale } = useUsers();
  const [draft, setDraft] = useState(value);
  const b = draft.branding;
  const patch = (partial: Partial<typeof b>) =>
    setDraft({ ...draft, branding: { ...b, ...partial } });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onSave(draft);
  };
  return (
    <form className="team-section" onSubmit={submit}>
      <fieldset className="team-modal-content" disabled={busy}>
        <div className="team-grid">
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
        <label className="team-check">
          <input
            type="checkbox"
            checked={b.show_usage}
            onChange={(e) => patch({ show_usage: e.target.checked })}
          />
          {t("showUsage")}
        </label>
        <label className="team-check">
          <input
            type="checkbox"
            checked={b.show_routing}
            onChange={(e) => patch({ show_routing: e.target.checked })}
          />
          {t("showRouting")}
        </label>
      </fieldset>
      {error ? (
        <div role="alert" className="team-error">
          {teamError(error, locale)}
        </div>
      ) : null}
      <button className="team-button primary" disabled={busy}>
        {busy ? t("saving") : t("save")}
      </button>
    </form>
  );
}
