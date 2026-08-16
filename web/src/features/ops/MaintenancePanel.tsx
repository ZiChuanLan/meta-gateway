import { useQuery } from "@tanstack/react-query"
import { api } from "../../api/client"
import { useAdminMutation } from "../../hooks/useAdminMutation"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { Button, Panel, formatBytes } from "../../components/ui"

export // Database maintenance: scheduled orphan GC + VACUUM (cron) and a manual
// run button with the last pass summary.
function MaintenancePanel() {
  const { client } = useSession();
  const service = api(client!);
  const { t } = useI18n();
  const last = useQuery({
    queryKey: ["db-gc-last"],
    queryFn: ({ signal }) => service.lastDBGC(signal),
    refetchInterval: 60_000,
  });
  const run = useAdminMutation({
    mutationFn: () => service.runDBGC(),
    invalidateKeys: [["db-gc-last"]],
  });
  const res = last.data?.result;
  const total = res
	    ? res.route_members + res.proxy_logs + res.discovered_models +
	      res.checkin_logs + res.usage_records + res.balance_history +
	      res.decision_snapshots + res.channel_health_history +
	      res.channel_model_blocks +
	      res.redemption_codes + res.error_passthrough_rules
	    : 0;
  return (
    <Panel
      className="runtime-card runtime-tool-maintenance"
      id="runtime-db-maintenance"
    >
      <div className="panel-header">
        <strong>{t("ops.maintenance.title")}</strong>
        <span className="flex-spacer" />
        <Button
          variant="secondary"
          disabled={run.isPending}
          onClick={() => run.mutate()}
        >
          {run.isPending ? t("common.working") : t("ops.maintenance.run")}
        </Button>
      </div>
      <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
        {t("ops.maintenance.hint")}
      </p>
      {run.data ? (
        <p className="maintenance-result">
          {t("ops.maintenance.result", {
	            total: run.data.route_members + run.data.proxy_logs + run.data.discovered_models + run.data.checkin_logs + run.data.usage_records + run.data.balance_history + run.data.decision_snapshots + run.data.channel_health_history + run.data.channel_model_blocks + run.data.redemption_codes + run.data.error_passthrough_rules,
            vacuumed: run.data.vacuumed
              ? t("ops.maintenance.vacuumed", {
                  bytes: formatBytes(run.data.vacuum_freed_bytes),
                })
              : t("ops.maintenance.noVacuum"),
          })}
        </p>
      ) : res ? (
        <p className="muted" style={{ fontSize: 12 }}>
          {t("ops.maintenance.lastRun", {
            total,
            at: last.data?.ran_at
              ? new Date(last.data.ran_at).toLocaleString()
              : "—",
          })}
        </p>
	      ) : null}
    </Panel>
  );
}
