import { useQuery } from "@tanstack/react-query"
import { useState } from "react"
import { api } from "../../api/client"
import type { PromptGuardRule } from "../../api/types"
import { useAdminMutation } from "../../hooks/useAdminMutation"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { Button, Dialog, Field, Panel } from "../../components/ui"

export // Sensitive prompt guards: regex rules that mask, reject, or channel-exclude
// request bodies containing sensitive content (API keys, credentials…).
function PromptGuardPanel() {
  const { client } = useSession();
  const service = api(client!);
  const { t } = useI18n();
  const query = useQuery({
    queryKey: ["prompt-guards"],
    queryFn: ({ signal }) => service.promptGuards(signal),
  });
  const [draft, setDraft] = useState<Partial<PromptGuardRule> | null>(null);
  const save = useAdminMutation({
    mutationFn: (value: PromptGuardRule) =>
      value.id
        ? service.updatePromptGuard(value.id, value)
        : service.createPromptGuard(value),
    invalidateKeys: [["prompt-guards"]],
  });
  const remove = useAdminMutation({
    mutationFn: (id: number) => service.deletePromptGuard(id),
    invalidateKeys: [["prompt-guards"]],
  });
  const items = query.data?.items ?? [];
  return (
    <Panel
      className="runtime-card runtime-tool-prompt-guard"
      id="runtime-prompt-guards"
    >
      <div className="panel-header">
        <strong>{t("ops.guard.title")}</strong>
        <button
          type="button"
          className="icon-button"
          title={t("ops.guard.add")}
          onClick={() =>
            setDraft({
              name: "",
              pattern: "",
              action: "mask",
              replacement: "[REDACTED]",
              exclude_channels: "",
              channel_scope: 0,
              enabled: true,
            })
          }
        >
          +
        </button>
      </div>
      <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
        {t("ops.guard.hint")}
      </p>
      {items.length === 0 ? (
        <p className="is-quiet" style={{ fontSize: 12 }}>
          {t("ops.guard.empty")}
        </p>
      ) : (
        <div className="error-rules-list">
          {items.map((rule) => (
            <div key={rule.id} className="error-rule-row">
              <span className={"error-rule-badge is-" + rule.action}>
                {rule.action}
              </span>
              <span className="error-rule-name">{rule.name}</span>
              <code className="error-rule-cond">{rule.pattern}</code>
              {rule.action === "exclude" ? (
                <code className="error-rule-cond">{rule.exclude_channels}</code>
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
        <PromptGuardEditor
          value={draft}
          pending={save.isPending}
          error={save.error instanceof Error ? save.error : null}
          onClose={() => setDraft(null)}
          onSave={(value) => {
            save.mutate(value as PromptGuardRule);
            setDraft(null);
          }}
        />
      ) : null}
    </Panel>
  );
}

function PromptGuardEditor({
  value,
  pending,
  error,
  onClose,
  onSave,
}: {
  value: Partial<PromptGuardRule>;
  pending: boolean;
  error?: Error | null;
  onClose: () => void;
  onSave: (value: Partial<PromptGuardRule>) => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState<Partial<PromptGuardRule>>(value);
  const patch = (p: Partial<PromptGuardRule>) =>
    setForm((current) => ({ ...current, ...p }));
  return (
    <Dialog
      title={form.id ? t("ops.guard.edit") : t("ops.guard.add")}
      onClose={onClose}
    >
      <div className="meta-form">
        <Field label={t("ops.guard.name")}>
          <input
            value={form.name ?? ""}
            onChange={(e) => patch({ name: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("ops.guard.pattern")}>
          <input
            value={form.pattern ?? ""}
            placeholder="sk-[A-Za-z0-9]{16,}"
            onChange={(e) => patch({ pattern: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("ops.guard.action")}>
          <select
            value={form.action ?? "mask"}
            onChange={(e) =>
              patch({
                action: e.target.value as PromptGuardRule["action"],
              })
            }
            disabled={pending}
          >
            <option value="mask">{t("ops.guard.actionMask")}</option>
            <option value="reject">{t("ops.guard.actionReject")}</option>
            <option value="exclude">{t("ops.guard.actionExclude")}</option>
          </select>
        </Field>
        {form.action === "mask" ? (
          <Field label={t("ops.guard.replacement")}>
            <input
              value={form.replacement ?? "[REDACTED]"}
              onChange={(e) => patch({ replacement: e.target.value })}
              disabled={pending}
            />
          </Field>
        ) : null}
        {form.action === "exclude" ? (
          <Field label={t("ops.guard.excludeChannels")}>
            <input
              value={form.exclude_channels ?? ""}
              placeholder="5, 12"
              onChange={(e) => patch({ exclude_channels: e.target.value })}
              disabled={pending}
            />
          </Field>
        ) : null}
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
