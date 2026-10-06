import { Button } from "../../components/ui";
import { useI18n } from "../../i18n";

/**
 * Cancel/save footer for the ops rule editors (alert rules, error passthrough
 * rules, prompt guards). The three editors had byte-identical copies of this
 * block; what stays per-panel is the request executor and the field set, which
 * is where the identity and validation boundaries actually live.
 *
 * The shared `Dialog` owns close protection through its `busy` prop, so this
 * only has to keep the two buttons from firing a second submission.
 */
export function RuleEditorFooter({
  pending,
  error,
  onCancel,
  onSave,
}: {
  pending: boolean;
  error?: Error | null;
  onCancel: () => void;
  onSave: () => void;
}) {
  const { t } = useI18n();
  return (
    <>
      {error ? <div className="inline-error">{error.message}</div> : null}
      <div className="dialog-actions">
        <span className="flex-spacer" />
        <Button variant="secondary" disabled={pending} onClick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button disabled={pending} onClick={onSave}>
          {pending ? t("common.working") : t("common.save")}
        </Button>
      </div>
    </>
  );
}
