import { Play } from "lucide-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useMemo, useState } from "react"
import { api } from "../../api/client"
import { useAdminMutation } from "../../hooks/useAdminMutation"
import { useClientPagination } from "../../hooks/useClientPagination"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { useModules } from "../../hooks/useModules"
import { useToast } from "../../toast"
import { SCHEDULE_PRESETS, scheduleFromSettings, settingsFromSchedule, type SchedulePresetId } from "../../lib/schedulePresets"
import { PaginationBar } from "../../components/PaginationBar"
import { Button, ConfirmDialog, DataTable, ErrorState, Loading, Panel, InfoTip, StatusBadge, formatDate } from "../../components/ui"
import { CheckinTimePicker } from "./CheckinTimePicker"

/** Human-readable check-in category; falls back to the raw code. */
function checkinCategoryLabel(
  category: string,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  const key = `ops.checkinCategory.${category}`;
  const translated = t(key);
  // i18n returns the key itself when missing.
  if (translated === key) return category;
  return translated;
}

function checkinDetailText(
  log: { category: string; message?: string },
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  const message = (log.message || "").trim();
  const label = checkinCategoryLabel(log.category, t);
  if (!message) return label;
  // Avoid "Label — Label" when message already matches a short English category phrase.
  if (message.toLowerCase() === log.category.toLowerCase()) return label;
  if (message === label) return label;
  return `${label} — ${message}`;
}

function siteDisplayName(
  siteId: number,
  sitesById: Map<number, { name: string; base_url: string }>,
  t: (key: string, vars?: Record<string, string | number>) => string,
): { title: string; subtitle: string } {
  const site = sitesById.get(siteId);
  if (!site) {
    return {
      title: t("common.siteId", { id: siteId }),
      subtitle: "",
    };
  }
  const name =
    site.name.trim() ||
    site.base_url.trim() ||
    t("common.siteId", { id: siteId });
  let host = "";
  try {
    host = site.base_url ? new URL(site.base_url).host : "";
  } catch {
    host = site.base_url;
  }
  const subtitleParts = [`#${siteId}`];
  if (host && host !== name) subtitleParts.push(host);
  return {
    title: name,
    subtitle: subtitleParts.join(" · "),
  };
}

export function CheckinsPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const toast = useToast();
  const { checkinEnabled, ready: modulesReady } = useModules();
  const s = api(client!);
  const qc = useQueryClient();
  const [status, setStatus] = useState("");
  const [confirmRun, setConfirmRun] = useState(false);
  const runtime = useQuery({
    queryKey: ["runtime-settings"],
    queryFn: ({ signal }) => s.runtimeSettings(signal),
    enabled: modulesReady && checkinEnabled,
  });
  const [scheduleDraft, setScheduleDraft] = useState<{
    preset: SchedulePresetId;
    cron: string;
  } | null>(null);
  useEffect(() => {
    if (scheduleDraft) return;
    if (!runtime.data?.editable) return;
    setScheduleDraft(
      scheduleFromSettings({
        enabled: runtime.data.editable.checkin_enabled,
        cron: runtime.data.editable.checkin_cron,
      }),
    );
  }, [runtime.data, scheduleDraft]);
  const saveSchedule = useAdminMutation({
    mutationFn: (next: { enabled: boolean; cron: string }) => {
      const editable = runtime.data?.editable;
      if (!editable) throw new Error("runtime settings unavailable");
      return s.updateRuntimeSettings({
        ...editable,
        checkin_enabled: next.enabled,
        checkin_cron: next.cron,
      });
    },
    invalidateKeys: [["runtime-settings"]],
    onSuccess: () => {
      toast.push({ tone: "success", message: t("ops.checkin.scheduleSaved") });
    },
  });
  const logs = useQuery({
    queryKey: ["checkin-logs", status],
    queryFn: ({ signal }) =>
      s.checkinLogs(`?limit=100${status ? `&status=${status}` : ""}`, signal),
    enabled: modulesReady && checkinEnabled,
    retry: (failureCount, error) => {
      const message = error instanceof Error ? error.message : String(error);
      if (/plugin_disabled/i.test(message)) return false;
      const statusCode = (error as { status?: number } | null)?.status;
      if (statusCode === 404) return false;
      return failureCount < 2;
    },
  });
  const sites = useQuery({
    queryKey: ["sites"],
    queryFn: ({ signal }) => s.sites(signal),
    enabled: modulesReady && checkinEnabled,
  });
  const sitesById = useMemo(() => {
    const map = new Map<number, { name: string; base_url: string }>();
    for (const site of sites.data ?? []) {
      map.set(site.id, { name: site.name, base_url: site.base_url });
    }
    return map;
  }, [sites.data]);
  const run = useMutation({
    mutationFn: s.runAllCheckins,
    onSuccess: () => {
      setConfirmRun(false);
      void qc.invalidateQueries({ queryKey: ["checkin-logs"] });
      void qc.invalidateQueries({ queryKey: ["credentials"] });
      void qc.invalidateQueries({ queryKey: ["channel-overviews"] });
    },
    onError: () => setConfirmRun(false),
  });
  const checkinRows = logs.data ?? [];
  const checkinPagination = useClientPagination(checkinRows, 15);

  if (modulesReady && !checkinEnabled) {
    return (
      <Panel>
        <p className="detail-empty">{t("ops.checkinModuleOff")}</p>
      </Panel>
    );
  }

  const schedule = scheduleDraft ?? { preset: "off" as const, cron: "" };
  const scheduleDirty =
    runtime.data != null &&
    scheduleDraft != null &&
    (scheduleDraft.preset === "off"
      ? runtime.data.editable.checkin_enabled
      : !runtime.data.editable.checkin_enabled ||
        runtime.data.editable.checkin_cron !== scheduleDraft.cron);

  return (
    <>
      <Panel
        title={t("ops.checkin.scheduleTitle")}
        titleHelp={t("ops.checkin.scheduleHint")}
      >
        <label
          className="check"
        >
          <input
            type="checkbox"
            disabled={saveSchedule.isPending || scheduleDraft == null}
            checked={schedule.preset !== "off"}
            onChange={(e) => {
              if (!scheduleDraft) return;
              setScheduleDraft(
                e.target.checked
                  ? { preset: "daily", cron: "0 8 * * *" }
                  : { preset: "off", cron: scheduleDraft.cron },
              );
            }}
          />
          <span>{t("ops.checkin.scheduleEnabled")}</span>
        </label>
        {schedule.preset !== "off" ? (
          <div className="form-grid" style={{ marginTop: 12 }}>
            <label className="field">
              <span>{t("ops.checkin.schedulePreset")}</span>
              <select
                disabled={saveSchedule.isPending}
                value={schedule.preset}
                onChange={(e) => {
                  const preset = e.target.value as SchedulePresetId;
                  if (!scheduleDraft) return;
                  const known = SCHEDULE_PRESETS.find(
                    (item) => item.id === preset,
                  );
                  setScheduleDraft({
                    preset,
                    cron:
                      preset === "custom"
                        ? scheduleDraft.cron
                        : (known?.cron ?? scheduleDraft.cron),
                  });
                }}
              >
                {SCHEDULE_PRESETS.filter((item) => item.id !== "off").map(
                  (item) => (
                    <option key={item.id} value={item.id}>
                      {t(`ops.schedule.preset.${item.id}`)}
                    </option>
                  ),
                )}
              </select>
            </label>
            {schedule.preset === "custom" || schedule.preset === "daily" ? (
              <label className="field">
                <span>{t("ops.checkin.scheduleCron")}</span>
                <CheckinTimePicker
                  value={schedule.cron}
                  disabled={saveSchedule.isPending}
                  onChange={(cron) =>
                    setScheduleDraft({ preset: "custom", cron })
                  }
                />
              </label>
            ) : (
              <div className="field">
                <span>{t("ops.checkin.scheduleCron")}</span>
                <input
                  className="mono"
                  disabled
                  value={schedule.cron}
                  readOnly
                />
              </div>
            )}
          </div>
        ) : null}
        <div style={{ marginTop: 4 }}>
          <Button
            variant="secondary"
            disabled={
              !scheduleDirty || saveSchedule.isPending || scheduleDraft == null
            }
            onClick={() =>
              saveSchedule.mutate(settingsFromSchedule(scheduleDraft!))
            }
          >
            {saveSchedule.isPending
              ? t("common.working")
              : t("ops.checkin.scheduleSave")}
          </Button>
        </div>
      </Panel>

      <Panel
        actions={
          <>
            <select
              aria-label={t("ops.statusFilter")}
              value={status}
              onChange={(e) => setStatus(e.target.value)}
            >
              <option value="">{t("ops.allStatuses")}</option>
              <option value="success">success</option>
              <option value="failed">failed</option>
              <option value="skipped">skipped</option>
            </select>
            <Button
              icon={<Play size={16} />}
              disabled={run.isPending || !checkinEnabled}
              onClick={() => setConfirmRun(true)}
            >
              {run.isPending ? t("ops.running") : t("ops.runEnabled")}
            </Button>
          </>
        }
      >
        <div className="ops-panel-context">
          <span>{t("ops.checkinHint")}</span>
          <InfoTip label={t("ops.checkinHint")} />
        </div>
        {run.error && <ErrorState error={run.error} />}
        {run.data && (
          <div className="result-strip">
            <StatusBadge
              value={run.data.failure_count > 0 ? "failed" : "success"}
            />
            <span>
              {t("ops.checkinSummary", {
                success: run.data.success_count,
                failure: run.data.failure_count,
                skipped: run.data.skipped_count,
              })}
            </span>
          </div>
        )}
        {logs.isPending ? (
          <Loading />
        ) : logs.isError ? (
          <ErrorState error={logs.error} />
        ) : (
          <>
            <DataTable
              headers={[
                t("common.site"),
                t("common.status"),
                t("common.source"),
                t("ops.checkinDetail"),
                t("common.reward"),
                t("common.latency"),
                t("common.time"),
              ]}
              empty={!checkinRows.length}
            >
              {checkinPagination.pageItems.map((l) => (
                <tr key={l.id}>
                  <td>
                    {(() => {
                      const display = siteDisplayName(l.site_id, sitesById, t);
                      return (
                        <>
                          <strong title={display.subtitle || undefined}>
                            {display.title}
                          </strong>
                          <small>
                            {display.subtitle
                              ? `${display.subtitle} · ${t("common.credentialId", { id: l.credential_id })}`
                              : t("common.credentialId", {
                                  id: l.credential_id,
                                })}
                          </small>
                        </>
                      );
                    })()}
                  </td>
                  <td>
                    <StatusBadge value={l.status} />
                  </td>
                  <td>{l.source}</td>
                  <td>
                    <span title={l.category}>{checkinDetailText(l, t)}</span>
                  </td>
                  <td>{l.reward || "-"}</td>
                  <td>{t("common.ms", { n: l.latency_ms })}</td>
                  <td>{formatDate(l.ran_at)}</td>
                </tr>
              ))}
            </DataTable>
            <PaginationBar
              page={checkinPagination.page}
              totalPages={checkinPagination.totalPages}
              total={checkinPagination.total}
              pageSize={checkinPagination.pageSize}
              rangeStart={checkinPagination.rangeStart}
              rangeEnd={checkinPagination.rangeEnd}
              hasPrev={checkinPagination.hasPrev}
              hasNext={checkinPagination.hasNext}
              onPageChange={checkinPagination.setPage}
              onPageSizeChange={checkinPagination.setPageSize}
            />
          </>
        )}
        {confirmRun ? (
          <ConfirmDialog
            title={t("ops.runEnabled")}
            message={t("ops.runEnabledConfirm")}
            confirmLabel={t("ops.runEnabled")}
            pending={run.isPending}
            error={run.error}
            onClose={() => setConfirmRun(false)}
            onConfirm={() => run.mutate()}
          />
        ) : null}
      </Panel>
    </>
  );
}
