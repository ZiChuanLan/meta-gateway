import { useState } from "react"
import type { ModelMetadata } from "../../api/types"
import { Button, Dialog, Field } from "../../components/ui"
import { useI18n } from "../../i18n"

export // Compact token-count rendering for metadata badges (128000 → 128K).
// Capability annotation editor for one canonical model name (context window,
// modalities, thinking support, vendor, notes).
function ModelMetadataDialog({
  value,
  pending,
  error,
  onClose,
  onSave,
  onDelete,
}: {
  value: ModelMetadata;
  pending: boolean;
  error?: unknown;
  onClose: () => void;
  onSave: (value: ModelMetadata) => void;
  onDelete?: () => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState<ModelMetadata>(value);
  const patch = (partial: Partial<ModelMetadata>) =>
    setForm((current) => ({ ...current, ...partial }));
  const thinkingOptions = [
    { value: -1, label: t("modelsPage.metaThinkingUnknown") },
    { value: 1, label: t("modelsPage.metaThinkingYes") },
    { value: 0, label: t("modelsPage.metaThinkingNo") },
  ];
  return (
    <Dialog
      title={t("modelsPage.metaTitle", { name: value.model_name })}
      onClose={onClose}
    >
      <div className="meta-form">
        <Field label={t("modelsPage.metaCtx")}>
          <input
            type="number"
            min={0}
            step={1000}
            value={form.context_window}
            onChange={(e) =>
              patch({ context_window: Math.max(0, Number(e.target.value) || 0) })
            }
            disabled={pending}
          />
        </Field>
        <Field label={t("modelsPage.metaInput")}>
          <input
            value={form.input_modalities}
            placeholder="text,image,audio"
            onChange={(e) => patch({ input_modalities: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("modelsPage.metaOutput")}>
          <input
            value={form.output_modalities}
            placeholder="text,audio"
            onChange={(e) => patch({ output_modalities: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("modelsPage.metaThinking")}>
          <select
            value={form.supports_thinking}
            onChange={(e) =>
              patch({ supports_thinking: Number(e.target.value) })
            }
            disabled={pending}
          >
            {thinkingOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("modelsPage.metaVendor")}>
          <input
            value={form.vendor}
            placeholder="DeepSeek, Google, OpenAI…"
            onChange={(e) => patch({ vendor: e.target.value })}
            disabled={pending}
          />
        </Field>
        <Field label={t("modelsPage.metaNotes")}>
          <input
            value={form.notes}
            onChange={(e) => patch({ notes: e.target.value })}
            disabled={pending}
          />
        </Field>
      </div>
	  {error ? <div className="inline-error">{String(error)}</div> : null}
      <div className="dialog-actions">
        {onDelete ? (
          <Button
            variant="danger"
            disabled={pending}
            onClick={() => {
              onDelete();
              onClose();
            }}
          >
            {t("common.delete")}
          </Button>
        ) : null}
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
