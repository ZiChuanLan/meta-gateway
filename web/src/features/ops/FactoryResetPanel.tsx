import { useQueryClient } from "@tanstack/react-query"
import { useState } from "react"
import { api } from "../../api/client"
import { useAdminMutation } from "../../hooks/useAdminMutation"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { Button, Panel } from "../../components/ui"

export // Factory reset: wipe all business data (channels, keys, routes, logs,
// histories, rules) while preserving configuration. Requires typing RESET.
function FactoryResetPanel() {
  const { client } = useSession();
  const service = api(client!);
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [arm, setArm] = useState(false);
  const [confirm, setConfirm] = useState("");
  const reset = useAdminMutation({
    mutationFn: () => service.factoryReset(confirm),
    onSuccess: () => {
      queryClient.clear();
      setArm(false);
      setConfirm("");
    },
  });
  return (
    <Panel
      className="runtime-card runtime-tool-factory-reset is-danger-zone"
      id="runtime-factory-reset"
    >
      <div className="panel-header">
        <strong>{t("ops.factoryReset.title")}</strong>
      </div>
      <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
        {t("ops.factoryReset.hint")}
      </p>
      {!arm ? (
        <Button variant="danger" onClick={() => setArm(true)}>
          {t("ops.factoryReset.start")}
        </Button>
      ) : (
        <div className="factory-reset-arm">
          <input
            type="text"
            placeholder={t("ops.factoryReset.typeConfirm")}
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            disabled={reset.isPending}
          />
          <Button
            variant="danger"
            disabled={confirm !== "RESET" || reset.isPending}
            onClick={() => reset.mutate()}
          >
            {reset.isPending ? t("common.working") : t("ops.factoryReset.confirm")}
          </Button>
          <Button variant="secondary" disabled={reset.isPending} onClick={() => setArm(false)}>
            {t("common.cancel")}
          </Button>
        </div>
      )}
      {reset.isSuccess ? (
        <p className="factory-reset-done">
          {t("ops.factoryReset.done")}
        </p>
      ) : null}
      {reset.error instanceof Error ? (
        <div className="inline-error">{reset.error.message}</div>
      ) : null}
    </Panel>
  );
}
