import { useState } from "react"
import type { RouteMember } from "../../api/types"
import { Button, Dialog, ErrorState, Field, InfoTip } from "../../components/ui"
import { useI18n } from "../../i18n"

export function MemberDialog({
  value,
  channels,
  pending,
  error,
  onClose,
  onSave,
}: {
  value: Partial<RouteMember>;
  channels: Array<{ id: number; name: string }>;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (value: Partial<RouteMember>) => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState(value);
  // Editing priority/weight makes the member independent of the connection
  // defaults (otherwise a later connection edit or model re-sync overwrites
  // the values). Keeping the checkbox off and saving without touching the
  // values preserves the previous state.
  const [valuesTouched, setValuesTouched] = useState(false);
  const markTouched = () => setValuesTouched(true);
  return (
    <Dialog
      title={value.id ? t("routing.editMember") : t("routing.addMember")}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={pending || !form.channel_id}
            onClick={() =>
              onSave({
                ...form,
                manual_override: (form.manual_override ?? false) || valuesTouched,
              })
            }
          >
            {pending ? t("common.working") : t("common.save")}
          </Button>
        </>
      }
    >
      {!value.id ? (
        <Field label={t("common.channel")}>
          <select
            value={form.channel_id ?? ""}
            onChange={(event) =>
              setForm({
                ...form,
                channel_id: Number(event.target.value) || undefined,
              })
            }
          >
            <option value="">{t("common.select")}</option>
            {channels.map((channel) => (
              <option key={channel.id} value={channel.id}>
                {channel.name}
              </option>
            ))}
          </select>
        </Field>
      ) : null}
			<div className="ops-panel-context">
				<span>{t("routing.memberDialogIntro")}</span>
			</div>
      <div className="form-grid">
        <Field label={t("routing.priorityLabel")}>
          <input
            type="number"
            value={form.priority ?? 0}
            onChange={(event) => {
              markTouched();
              setForm({ ...form, priority: Number(event.target.value) });
            }}
          />
          <InfoTip label={t("routing.priorityHint")} />
        </Field>
        <Field label={t("routing.weightLabel")}>
          <input
            type="number"
            min={0}
            value={form.weight ?? 100}
            onChange={(event) => {
              markTouched();
              setForm({ ...form, weight: Number(event.target.value) });
            }}
          />
          <InfoTip label={t("routing.weightHint")} />
        </Field>
      </div>
      <label className="check check-with-hint">
        <input
          type="checkbox"
          checked={form.enabled ?? true}
          onChange={(event) =>
            setForm({ ...form, enabled: event.target.checked })
          }
        />
        <span>
          <strong>{t("routing.enabledLabel")}</strong>
          <InfoTip label={t("routing.enabledHint")} />
        </span>
      </label>
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}
