import { useQuery } from "@tanstack/react-query"
import { useState } from "react"
import { api } from "../../api/client"
import type { AlertRule } from "../../api/types"
import { useAdminMutation } from "../../hooks/useAdminMutation"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { Button, Dialog, Field, Panel } from "../../components/ui"

export // Alert rules: metric/operator/threshold/window/sustained → webhook.
function AlertRulesPanel() {
  const { client } = useSession();
  const service = api(client!);
  const { t } = useI18n();
  const query = useQuery({
    queryKey: ["alert-rules"],
    queryFn: ({ signal }) => service.alertRules(signal),
  });
  const [draft, setDraft] = useState<Partial<AlertRule> | null>(null);
  const save = useAdminMutation({
    mutationFn: (value: AlertRule) =>
      value.id
        ? service.updateAlertRule(value.id, value)
        : service.createAlertRule(value),
    invalidateKeys: [["alert-rules"]],
  });
  const remove = useAdminMutation({
    mutationFn: (id: number) => service.deleteAlertRule(id),
    invalidateKeys: [["alert-rules"]],
  });
  const items = query.data?.items ?? [];
  const metrics = query.data?.metrics ?? {};
  return (
    <Panel
      className="runtime-card runtime-tool-alert-rules"
      id="runtime-alert-rules"
    >
      <div className="panel-header">
        <strong>{t("ops.alertRules.title")}</strong>
        <button
          type="button"
          className="icon-button"
          title={t("ops.alertRules.add")}
          onClick={() =>
            setDraft({
              name: "",
              metric: "request_fail_rate",
              operator: "gt",
              threshold: 0.5,
              window_seconds: 3600,
              sustained_seconds: 300,
              cooldown_seconds: 900,
              level: "warning",
              enabled: true,
            })
          }
        >
          +
        </button>
      </div>
      <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
        {t("ops.alertRules.hint")}
      </p>
      {items.length === 0 ? (
        <p className="is-quiet" style={{ fontSize: 12 }}>
          {t("ops.alertRules.empty")}
        </p>
      ) : (
        <div className="error-rules-list">
          {items.map((rule) => (
            <div key={rule.id} className="error-rule-row">
              <span className={"error-rule-badge " + (rule.enabled ? "is-passthrough" : "is-off")}>
                {rule.metric}
              </span>
              <span className="error-rule-name">{rule.name}</span>
              <code className="error-rule-cond">
                {rule.operator} {rule.threshold}
              </code>
              <code className="error-rule-cond">
                {rule.sustained_seconds}s / {rule.cooldown_seconds}s
              </code>
              {!rule.enabled ? (
                <span className="error-rule-off">{t("common.disabled")}</span>
              ) : null}
              <span className="flex-spacer" />
              <button
                type="button"
                className="error-rule-edit"
                onClick={() => setDraft({ ...rule })}
              >
                {t("common.edit")}
              </button>
              <button
                type="button"
                className="error-rule-del"
                onClick={() => remove.mutate(rule.id!)}
              >
                {t("common.delete")}
              </button>
            </div>
          ))}
        </div>
      )}
      {draft ? (
        <AlertRuleEditor
          value={draft}
          metrics={metrics}
		  pending={save.isPending}
		  error={save.error instanceof Error ? save.error : null}
		  onClose={() => setDraft(null)}
          onSave={(value) => {
            save.mutate(value as AlertRule);
            setDraft(null);
          }}
        />
      ) : null}
    </Panel>
  );
}

function AlertRuleEditor({
  value,
  metrics,
  pending,
  error,
  onClose,
  onSave,
}: {
  value: Partial<AlertRule>;
  metrics: Record<string, string>;
  pending: boolean;
  error?: Error | null;
  onClose: () => void;
  onSave: (value: Partial<AlertRule>) => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState<Partial<AlertRule>>(value);
  const patch = (p: Partial<AlertRule>) =>
    setForm((current) => ({ ...current, ...p }));
  return (
    <Dialog
      title={form.id ? t("ops.alertRules.edit") : t("ops.alertRules.add")}
      onClose={onClose}
    >
      <div className="meta-form">
        <Field label={t("ops.alertRules.name")}>
          <input
            value={form.name ?? ""}
            onChange={(e) => patch({ name: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("ops.alertRules.metric")}>
          <select
            value={form.metric}
            onChange={(e) => patch({ metric: e.target.value })}
            disabled={pending}
          >
            {Object.entries(metrics).map(([key, desc]) => (
              <option key={key} value={key} title={desc}>
                {key}
              </option>
            ))}
          </select>
        </Field>
        <div className="alert-rule-row2">
          <Field label={t("ops.alertRules.operator")}>
            <select
              value={form.operator}
              onChange={(e) =>
                patch({
                  operator: e.target.value as AlertRule["operator"],
                })
              }
              disabled={pending}
            >
              {["gt", "gte", "lt", "lte", "eq", "neq"].map((op) => (
                <option key={op} value={op}>
                  {op}
                </option>
              ))}
            </select>
          </Field>
          <Field label={t("ops.alertRules.threshold")}>
            <input
              type="number"
              step={0.05}
              value={form.threshold ?? 0}
              onChange={(e) =>
                patch({ threshold: Number(e.target.value) || 0 })
              }
              disabled={pending}
            />
          </Field>
        </div>
        <div className="alert-rule-row3">
          <Field label={t("ops.alertRules.window")}>
            <input
              type="number"
              min={60}
              value={form.window_seconds ?? 3600}
              onChange={(e) =>
                patch({ window_seconds: Number(e.target.value) || 3600 })
              }
              disabled={pending}
            />
          </Field>
          <Field label={t("ops.alertRules.sustained")}>
            <input
              type="number"
              min={0}
              value={form.sustained_seconds ?? 0}
              onChange={(e) =>
                patch({ sustained_seconds: Number(e.target.value) || 0 })
              }
              disabled={pending}
            />
          </Field>
          <Field label={t("ops.alertRules.cooldown")}>
            <input
              type="number"
              min={60}
              value={form.cooldown_seconds ?? 900}
              onChange={(e) =>
                patch({ cooldown_seconds: Number(e.target.value) || 900 })
              }
              disabled={pending}
            />
          </Field>
        </div>
        <label className="check">
          <input
            type="checkbox"
            checked={form.enabled ?? true}
            onChange={(e) => patch({ enabled: e.target.checked })}
            disabled={pending}
          />
          <span>{t("common.enabled")}</span>
        </label>
      </div>
      {error ? <div className="inline-error">{error.message}</div> : null}
      <div className="dialog-actions">
        <span className="flex-spacer" />
        <Button variant="secondary" disabled={pending} onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button disabled={pending} onClick={() => onSave(form)}>
          {pending ? t("common.working") : t("common.save")}
        </Button>
      </div>
    </Dialog>
  );
}
