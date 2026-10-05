import { useState, type FormEvent } from "react";
import { Button, Dialog, Field } from "../components/ui";
import { useI18n } from "../i18n";
import { teamError, type TeamText } from "../team/text";
import type { Plan, UserKey } from "../team/types";

export const blankKey = (): UserKey => ({
  id: 0,
  name: "",
  enabled: true,
  hint: "",
  models: "",
  expires_at: "",
  allowed_ips: "",
  plan_id: 0,
  created_at: "",
});

/** `datetime-local` wants local wall-clock text; the API wants RFC3339. */
function toLocalField(raw: string) {
  const date = new Date(raw);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
}

/**
 * Create or edit one API key.
 *
 * Everything the server accepts is here except the token itself: rotation and
 * deletion live in the footer because they invalidate credentials the user may
 * already have pasted into a client, and both ask for confirmation.
 */
export function KeyDialog({
  apiKey,
  plans,
  plansAllowed,
  busy,
  error,
  t,
  onClose,
  onSave,
  onRotate,
  onDelete,
}: {
  apiKey: UserKey;
  plans: Plan[];
  plansAllowed: boolean;
  busy: boolean;
  error: unknown;
  t: TeamText;
  onClose: () => void;
  onSave: (value: UserKey) => void;
  onRotate: (key: UserKey) => void;
  onDelete: (key: UserKey) => void;
}) {
  const { locale } = useI18n();
  const [draft, setDraft] = useState<UserKey>({ ...apiKey });
  const editing = draft.id > 0;
  const patch = (values: Partial<UserKey>) => setDraft({ ...draft, ...values });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSave(draft);
  }

  return (
    <Dialog
      title={t(editing ? "edit" : "createKey")}
      onClose={onClose}
      busy={busy}
      actions={
        <>
          {editing && (
            <>
              <Button
                variant="quiet"
                onClick={() => {
                  if (confirm(t("rotationWarning"))) onRotate(draft);
                }}
              >
                {t("rotate")}
              </Button>
              <Button
                variant="danger"
                onClick={() => {
                  if (confirm(t("deletionWarning"))) onDelete(draft);
                }}
              >
                {t("delete")}
              </Button>
            </>
          )}
          <Button type="submit" form="key-form" loading={busy} disabled={busy}>
            {busy ? t("saving") : t("save")}
          </Button>
        </>
      }
    >
      <form id="key-form" onSubmit={submit}>
        <div className="form-grid">
          <Field label={t("name")} className="wide">
            <input
              value={draft.name}
              required
              maxLength={80}
              autoFocus
              onChange={(e) => patch({ name: e.target.value })}
            />
          </Field>
          <Field label={t("keyModels")} className="wide">
            <input
              value={draft.models}
              placeholder={t("defaultRoute")}
              onChange={(e) => patch({ models: e.target.value })}
            />
          </Field>
          <Field label={t("expires")}>
            <input
              type="datetime-local"
              value={draft.expires_at ? toLocalField(draft.expires_at) : ""}
              onChange={(e) =>
                patch({
                  expires_at: e.target.value
                    ? new Date(e.target.value).toISOString()
                    : "",
                })
              }
            />
          </Field>
          {(plansAllowed || draft.plan_id > 0) && (
            <Field label={t("selectPlan")}>
              <select
                value={draft.plan_id}
                onChange={(e) => patch({ plan_id: Number(e.target.value) })}
              >
                <option value={0}>{t("defaultRoute")}</option>
                {draft.plan_id > 0 &&
                  !plans.some((plan) => plan.id === draft.plan_id) && (
                    <option value={draft.plan_id}>
                      #{draft.plan_id} · {t("accessDenied")}
                    </option>
                  )}
                {plans.map((plan) => (
                  <option key={plan.id} value={plan.id}>
                    {plan.name}
                  </option>
                ))}
              </select>
            </Field>
          )}
          <Field label={t("ips")} className="wide">
            <textarea
              rows={3}
              value={draft.allowed_ips}
              onChange={(e) => patch({ allowed_ips: e.target.value })}
            />
          </Field>
          {editing && (
            <label className="user-check" style={{ gridColumn: "1/-1" }}>
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(e) => patch({ enabled: e.target.checked })}
              />
              <span>{t("enable")}</span>
            </label>
          )}
        </div>
        {error ? (
          <div className="team-error" role="alert">
            {teamError(error, locale)}
          </div>
        ) : null}
      </form>
    </Dialog>
  );
}
