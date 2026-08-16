import { useQuery } from "@tanstack/react-query"
import { useState } from "react"
import { api } from "../../api/client"
import type { ErrorPassRule } from "../../api/types"
import { useAdminMutation } from "../../hooks/useAdminMutation"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { Button, Dialog, Field, Panel } from "../../components/ui"

export // Error passthrough rules: status/keyword → passthrough / rewrite /
// ignore_monitor. Read live on every request, so edits apply instantly.
function ErrorRulesPanel() {
  const { client } = useSession();
  const service = api(client!);
  const { t } = useI18n();
  const query = useQuery({
    queryKey: ["error-rules"],
    queryFn: ({ signal }) => service.errorRules(signal),
  });
  const [draft, setDraft] = useState<Partial<ErrorPassRule> | null>(null);
  const save = useAdminMutation({
    mutationFn: (value: ErrorPassRule) =>
      value.id
        ? service.updateErrorRule(value.id, value)
        : service.createErrorRule(value),
    invalidateKeys: [["error-rules"]],
  });
  const remove = useAdminMutation({
    mutationFn: (id: number) => service.deleteErrorRule(id),
    invalidateKeys: [["error-rules"]],
  });
  const items = query.data?.items ?? [];
  return (
    <Panel
      className="runtime-card runtime-tool-error-rules"
      id="runtime-error-rules"
    >
      <div className="panel-header">
        <strong>{t("ops.errorRules.title")}</strong>
        <button
          type="button"
          className="icon-button"
          title={t("ops.errorRules.add")}
          onClick={() =>
            setDraft({
              name: "",
              status_code: 0,
              keyword: "",
              model_glob: "",
              channel_id: 0,
              action: "passthrough",
              rewrite_to: 0,
              enabled: true,
            })
          }
        >
          +
        </button>
      </div>
      <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
        {t("ops.errorRules.hint")}
      </p>
      {items.length === 0 ? (
        <p className="is-quiet" style={{ fontSize: 12 }}>
          {t("ops.errorRules.empty")}
        </p>
      ) : (
        <div className="error-rules-list">
          {items.map((rule) => (
            <div key={rule.id} className="error-rule-row">
              <span className={"error-rule-badge is-" + rule.action}>
                {rule.action}
              </span>
              <span className="error-rule-name">{rule.name}</span>
              <code className="error-rule-cond">
                {rule.status_code || "any"} · {rule.keyword || "*"}
              </code>
              {rule.model_glob ? (
                <code className="error-rule-cond">{rule.model_glob}</code>
              ) : null}
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
		  <ErrorRuleEditor
			value={draft}
			pending={save.isPending}
			error={save.error as Error | null}
			onClose={() => setDraft(null)}
          onSave={(value) => {
            save.mutate(value as ErrorPassRule);
            setDraft(null);
          }}
        />
      ) : null}
    </Panel>
  );
}

function ErrorRuleEditor({
  value,
  pending,
  error,
  onClose,
  onSave,
}: {
  value: Partial<ErrorPassRule>;
  pending: boolean;
  error?: Error | null;
  onClose: () => void;
  onSave: (value: Partial<ErrorPassRule>) => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState<Partial<ErrorPassRule>>(value);
  const patch = (p: Partial<ErrorPassRule>) =>
    setForm((current) => ({ ...current, ...p }));
  return (
	<Dialog
		title={form.id ? t("ops.errorRules.edit") : t("ops.errorRules.add")}
		onClose={onClose}
	>
      <div className="meta-form">
        <Field label={t("ops.errorRules.name")}>
          <input
            value={form.name ?? ""}
            onChange={(e) => patch({ name: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("ops.errorRules.status")}>
          <input
            type="number"
            min={0}
            max={599}
            value={form.status_code ?? 0}
            onChange={(e) => patch({ status_code: Number(e.target.value) || 0 })}
            disabled={pending}
          />
        </Field>
        <Field label={t("ops.errorRules.keyword")}>
          <input
            value={form.keyword ?? ""}
            placeholder="rate limit, insufficient_quota…"
            onChange={(e) => patch({ keyword: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("ops.errorRules.modelGlob")}>
          <input
            value={form.model_glob ?? ""}
            placeholder="gpt-*"
            onChange={(e) => patch({ model_glob: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("ops.errorRules.action")}>
		  <select
			value={form.action ?? "passthrough"}
			onChange={(e) =>
			  patch({
				action: e.target.value as "passthrough" | "rewrite" | "ignore_monitor",
			  })
			}
			disabled={pending}
		  >
            <option value="passthrough">
              {t("ops.errorRules.actionPassthrough")}
            </option>
            <option value="rewrite">{t("ops.errorRules.actionRewrite")}</option>
            <option value="ignore_monitor">
              {t("ops.errorRules.actionIgnore")}
            </option>
          </select>
        </Field>
        {form.action === "rewrite" ? (
          <Field label={t("ops.errorRules.rewriteTo")}>
            <input
              type="number"
              min={100}
              max={599}
              value={form.rewrite_to ?? 0}
              onChange={(e) => patch({ rewrite_to: Number(e.target.value) || 0 })}
              disabled={pending}
            />
          </Field>
        ) : null}
        <label className="check" style={{ marginTop: 4 }}>
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
