import { useState } from "react"
import type { Route, RoutingCandidate } from "../../api/types"
import { Button, Dialog, ErrorState, Field, InfoTip } from "../../components/ui"
import { useI18n } from "../../i18n"

export function RouteDialog({
  value,
  members,
  pending,
  error,
  onClose,
  onSave,
}: {
  value: Partial<Route>;
  members: RoutingCandidate[];
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (value: Partial<Route> & { pin_priority?: boolean }) => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState(value);
  const allPinned =
    members.length > 0 &&
    members.every((candidate) => candidate.member.manual_override);
  const [pinPriority, setPinPriority] = useState(allPinned);
  return (
    <Dialog
      title={value.id ? t("routing.editRoute") : t("routing.addRoute")}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={pending || !form.model_pattern}
            onClick={() => onSave({ ...form, pin_priority: pinPriority })}
          >
            {pending ? t("common.working") : t("common.save")}
          </Button>
        </>
      }
    >
			<div className="ops-panel-context">
				<span>{t("modelsPage.addRouteHint")}</span>
			</div>
      <Field label={t("routing.exactModel")}>
        <input
          value={form.model_pattern ?? ""}
          onChange={(event) =>
            setForm({ ...form, model_pattern: event.target.value })
          }
          placeholder="gpt-4o-mini"
        />
      </Field>
      <label className="check">
        <input
          type="checkbox"
          checked={form.enabled ?? false}
          onChange={(event) =>
            setForm({ ...form, enabled: event.target.checked })
          }
        />
        <span>{t("routing.routeEnabled")}</span>
      </label>
      <div className="ops-panel-context" style={{ marginTop: 12 }}>
        <span>{t("routing.retryOverrideTitle")}</span>
      </div>
      <div className="form-grid">
        <Field
          label={t("routing.retryRounds")}
          hint={t("routing.retryRoundsHint")}
        >
          <input
            type="number"
            min={0}
            max={100}
            placeholder={t("routing.retryFollowGlobal")}
            value={form.retry_times ?? ""}
            onChange={(event) => {
              const v = event.target.value;
              setForm({
                ...form,
                retry_times: v === "" ? null : Number(v),
              });
            }}
          />
        </Field>
        <Field
          label={t("routing.channelRetry")}
          hint={t("routing.channelRetryHint")}
        >
          <input
            type="number"
            min={0}
            max={5}
            placeholder={t("routing.retryFollowGlobal")}
            value={form.channel_retry_times ?? ""}
            onChange={(event) => {
              const v = event.target.value;
              setForm({
                ...form,
                channel_retry_times: v === "" ? null : Number(v),
              });
            }}
          />
        </Field>
      </div>
      {members.length > 0 ? (
        <label className="check check-with-hint">
          <input
            type="checkbox"
            checked={pinPriority}
            onChange={(event) => setPinPriority(event.target.checked)}
          />
          <span>
									<strong>{t("routing.independentLabel")}</strong>
									<InfoTip label={t("routing.independentHint")} />
          </span>
        </label>
      ) : null}
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}
