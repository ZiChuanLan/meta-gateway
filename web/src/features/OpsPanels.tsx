import { DatabaseBackup, Play, RefreshCw, ShieldCheck } from "lucide-react";
import {
  useMutation,
  useQuery,
  useQueryClient,
  type QueryKey,
} from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type {
	AlertRule,
	ErrorPassRule,
  PromptGuardRule,
  RuntimeEditableSettings,
} from "../api/types";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { useModules } from "../hooks/useModules";
import { useToast } from "../toast";
import {
  SCHEDULE_PRESETS,
  scheduleFromSettings,
  settingsFromSchedule,
  type SchedulePresetId,
} from "../lib/schedulePresets";
import { PaginationBar } from "../components/PaginationBar";
import {
  Button,
  ConfirmDialog,
  DataTable,
  Dialog,
  Empty,
  ErrorState,
  Field,
  Loading,
  Panel,
  InfoTip,
  StatusBadge,
  formatBytes,
  formatDate,
} from "../components/ui";

const DISCOVERY_INVALIDATE_KEYS: QueryKey[] = [
  ["models"],
  ["channels"],
  ["channel-overviews"],
  ["routes"],
  ["route-overviews"],
  ["members"],
  ["explain"],
];

export function DiscoveryPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const s = api(client!);
  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: ({ signal }) => s.channels(signal),
  });
  const [filter, setFilter] = useState(0);
  const models = useQuery({
    queryKey: ["models", filter],
    queryFn: ({ signal }) => s.discoveredModels(filter || undefined, signal),
  });
  const refresh = useAdminMutation({
    mutationFn: s.refreshAll,
    invalidateKeys: DISCOVERY_INVALIDATE_KEYS,
  });
  const refreshOne = useAdminMutation({
    mutationFn: s.refreshChannel,
    invalidateKeys: DISCOVERY_INVALIDATE_KEYS,
    pendingIdOf: (channelId: number) => channelId,
  });
  const failedChannelIds = new Set(
    (refresh.data?.items ?? [])
      .filter((item) => item.error)
      .map((item) => item.channel_id),
  );
  const modelRows = models.data ?? [];
  const modelPagination = useClientPagination(modelRows, 15);
  const refreshBusy = refresh.isPending || refreshOne.isPending;
  return (
    <Panel
      actions={
        <>
          <select
            aria-label={t("ops.filterChannel")}
            value={filter}
            onChange={(e) => {
              refreshOne.reset();
              setFilter(Number(e.target.value));
            }}
          >
            <option value="0">{t("ops.allChannels")}</option>
            {channels.data?.map((c) => (
              <option value={c.id} key={c.id}>
                {c.name}
              </option>
            ))}
          </select>
          {filter > 0 && (
            <Button
              variant="secondary"
              icon={<RefreshCw size={16} />}
              disabled={refreshBusy}
              onClick={() => {
                refresh.reset();
                refreshOne.mutate(filter);
              }}
            >
              {refreshOne.isPending
                ? t("ops.refreshing")
                : t("ops.refreshChannel")}
            </Button>
          )}
          <Button
            icon={<RefreshCw size={16} />}
            disabled={refreshBusy}
            onClick={() => {
              refreshOne.reset();
              refresh.mutate();
            }}
          >
            {refresh.isPending ? t("ops.refreshing") : t("ops.refreshAll")}
          </Button>
        </>
      }
    >
      {refresh.data && (
        <div className="result-strip">
          <StatusBadge
            value={refresh.data.failure_count > 0 ? "failed" : "success"}
          />
          <span>
            {t("ops.refreshSummary", {
              success: refresh.data.success_count,
              failure: refresh.data.failure_count,
            })}
          </span>
        </div>
      )}
      {refreshOne.data && (
        <div className="result-strip">
          <StatusBadge value="success" />
          <span>
            {t("ops.refreshChannelResult", {
              id: refreshOne.data.channel_id,
              models: refreshOne.data.models.length,
              routes: refreshOne.data.created_routes,
            })}
          </span>
        </div>
      )}
      {refresh.data && failedChannelIds.size > 0 && (
        <div className="result-strip result-strip-error">
          <span>
            {t("ops.refreshFailures", {
              channels: Array.from(failedChannelIds)
                .map((id) => `#${id}`)
                .join(", "),
            })}
          </span>
        </div>
      )}
      {models.isPending ? (
        <Loading />
      ) : models.isError ? (
        <ErrorState error={models.error} />
      ) : (
        <>
          <DataTable
            headers={[
              t("common.model"),
              t("common.channel"),
              t("common.source"),
              t("common.available"),
              t("common.latency"),
              t("common.checked"),
            ]}
            empty={!modelRows.length}
          >
            {modelPagination.pageItems.map((m) => (
              <tr
                key={m.id}
                className={
                  failedChannelIds.has(m.channel_id) ? "row-failed" : undefined
                }
              >
                <td>
                  <strong>{m.model_name}</strong>
                </td>
                <td>#{m.channel_id}</td>
                <td>{m.source}</td>
                <td>
                  <StatusBadge value={m.available} />
                </td>
                <td>{t("common.ms", { n: m.latency_ms })}</td>
                <td>{formatDate(m.checked_at)}</td>
              </tr>
            ))}
          </DataTable>
          <PaginationBar
            page={modelPagination.page}
            totalPages={modelPagination.totalPages}
            total={modelPagination.total}
            pageSize={modelPagination.pageSize}
            rangeStart={modelPagination.rangeStart}
            rangeEnd={modelPagination.rangeEnd}
            hasPrev={modelPagination.hasPrev}
            hasNext={modelPagination.hasNext}
            onPageChange={modelPagination.setPage}
            onPageSizeChange={modelPagination.setPageSize}
          />
        </>
      )}
    </Panel>
  );
}

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

export function AuditPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const s = api(client!);
  const qc = useQueryClient();
  const [before, setBefore] = useState<number | undefined>();
  const [confirm, setConfirm] = useState(false);
  const q = useQuery({
    queryKey: ["audit", before],
    queryFn: ({ signal }) => s.auditEvents(before, signal),
  });
  const cleanup = useMutation({
    mutationFn: s.cleanupAudit,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["audit"] });
      setConfirm(false);
    },
  });
  const auditRows = q.data ?? [];
  const auditPagination = useClientPagination(auditRows, 15);
  return (
    <Panel
      actions={
        <Button
          variant="secondary"
          icon={<ShieldCheck size={16} />}
          onClick={() => setConfirm(true)}
        >
          {t("ops.applyRetention")}
        </Button>
      }
    >
      {q.isPending ? (
        <Loading />
      ) : q.isError ? (
        <ErrorState error={q.error} />
      ) : (
        <>
          <DataTable
            headers={[
              t("common.action"),
              t("common.actor"),
              t("common.resource"),
              t("common.outcome"),
              t("common.status"),
              t("common.category"),
              t("common.time"),
            ]}
            empty={!auditRows.length}
          >
            {auditPagination.pageItems.map((e) => (
              <tr key={e.id}>
                <td>
                  <strong>{e.action}</strong>
                  <small>#{e.id}</small>
                </td>
                <td>{e.actor_kind}</td>
                <td>
                  {e.resource_kind || "-"}
                  {e.resource_id ? ` #${e.resource_id}` : ""}
                </td>
                <td>
                  <StatusBadge value={e.outcome} />
                </td>
                <td>{e.status_code}</td>
                <td>{e.category || "-"}</td>
                <td>{formatDate(e.created_at)}</td>
              </tr>
            ))}
          </DataTable>
          <PaginationBar
            page={auditPagination.page}
            totalPages={auditPagination.totalPages}
            total={auditPagination.total}
            pageSize={auditPagination.pageSize}
            rangeStart={auditPagination.rangeStart}
            rangeEnd={auditPagination.rangeEnd}
            hasPrev={auditPagination.hasPrev}
            hasNext={auditPagination.hasNext}
            onPageChange={auditPagination.setPage}
            onPageSizeChange={auditPagination.setPageSize}
          />
          {q.data.length === 100 && (
            <Button
              variant="secondary"
              onClick={() => setBefore(q.data.at(-1)?.id)}
            >
              {t("ops.olderEvents")}
            </Button>
          )}
          {before && (
            <Button variant="quiet" onClick={() => setBefore(undefined)}>
              {t("ops.newestEvents")}
            </Button>
          )}
        </>
      )}
      {confirm && (
        <ConfirmDialog
          title={t("ops.applyRetentionTitle")}
          message={t("ops.applyRetentionMsg")}
          confirmLabel={t("ops.runCleanup")}
          pending={cleanup.isPending}
          error={cleanup.error}
          onClose={() => setConfirm(false)}
          onConfirm={() => cleanup.mutate()}
        />
      )}
    </Panel>
  );
}

export function BackupsPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const s = api(client!);
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["backups"],
    queryFn: ({ signal }) => s.backups(signal),
  });
  const create = useMutation({
    mutationFn: s.createBackup,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backups"] }),
  });
  const backupRows = q.data ?? [];
  const backupPagination = useClientPagination(backupRows, 12);
  return (
    <Panel
      title={t("ops.backups.title")}
      titleHelp={t("ops.backups.titleHelp")}
      actions={
        <Button
          icon={<DatabaseBackup size={16} />}
          disabled={create.isPending}
          onClick={() => create.mutate()}
        >
          {create.isPending ? t("ops.creatingBackup") : t("ops.createBackup")}
        </Button>
      }
    >
      {create.error && <ErrorState error={create.error} />}
      {create.data && (
        <div className="result-strip">
          <StatusBadge value={create.data.status} />
          <span>
            {t("ops.backupCreated", {
              name: create.data.name,
              size: formatBytes(create.data.size_bytes),
              time: formatDate(create.data.created_at),
            })}
          </span>
        </div>
      )}
      {q.isPending ? (
        <Loading />
      ) : q.isError ? (
        <ErrorState error={q.error} />
      ) : !q.data.length ? (
        <Empty>{t("ops.noBackups")}</Empty>
      ) : (
        <>
          <DataTable
            headers={[
              t("common.name"),
              t("common.status"),
              t("common.size"),
              t("common.duration"),
              t("common.checksum"),
              t("common.created"),
            ]}
          >
            {backupPagination.pageItems.map((b) => (
              <tr key={b.id}>
                <td>
                  <strong>{b.name}</strong>
                </td>
                <td>
                  <StatusBadge value={b.status} />
                </td>
                <td>{formatBytes(b.size_bytes)}</td>
                <td>{t("common.ms", { n: b.duration_ms })}</td>
                <td className="mono truncate">{b.checksum}</td>
                <td>{formatDate(b.created_at)}</td>
              </tr>
            ))}
          </DataTable>
          <PaginationBar
            page={backupPagination.page}
            totalPages={backupPagination.totalPages}
            total={backupPagination.total}
            pageSize={backupPagination.pageSize}
            rangeStart={backupPagination.rangeStart}
            rangeEnd={backupPagination.rangeEnd}
            hasPrev={backupPagination.hasPrev}
            hasNext={backupPagination.hasNext}
            onPageChange={backupPagination.setPage}
            onPageSizeChange={backupPagination.setPageSize}
          />
        </>
      )}
      <p className="muted" style={{ marginTop: 12, fontSize: 12 }}>
        {t("ops.restoreNote", {
          cmd: "meta-gateway restore --from <backup-name>",
        })}
      </p>
    </Panel>
  );
}

function numberOr(value: string, fallback: number) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

/** Panel-level anchor order for the runtime settings section nav. */
const RUNTIME_SECTION_ANCHORS = [
  ["relay", "ops.runtime.section.relay"],
  ["cooldown", "ops.runtime.section.cooldown"],
  ["health", "ops.runtime.section.health"],
  ["sticky", "ops.runtime.section.sticky"],
  ["checkin", "ops.runtime.section.checkin"],
  ["limits", "ops.runtime.section.limits"],
  ["audit", "ops.runtime.section.audit"],
  ["routing", "ops.runtime.section.routing"],
  ["stableFirst", "ops.runtime.section.stableFirst"],
  ["alerts", "ops.runtime.section.alerts"],
  ["server", "ops.runtime.section.server"],
] as const;

function SettingLabel({ label, hint }: { label: string; hint: string }) {
  return (
    <span className="setting-label">
      <span>{label}</span>
      <InfoTip label={hint} />
    </span>
  );
}

// Alert rules: metric/operator/threshold/window/sustained → webhook.
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

// Sensitive prompt guards: regex rules that mask, reject, or channel-exclude
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

// Factory reset: wipe all business data (channels, keys, routes, logs,
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

// Database maintenance: scheduled orphan GC + VACUUM (cron) and a manual
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

// Admin TOTP 2FA panel: setup (show secret + otpauth URI), enable with a
// code, and disable with a current code.
function TOTPPanel() {
  const { client } = useSession();
  const service = api(client!);
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [phase, setPhase] = useState<"idle" | "setup">("idle");
  const [setupData, setSetupData] = useState<{ secret: string; otpauth_uri: string } | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const status = useQuery({
    queryKey: ["totp-status"],
    queryFn: ({ signal }) => service.totpStatus(signal),
    refetchInterval: 30_000,
  });
  const enabled = status.data?.enabled ?? false;
  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ["totp-status"] });
    setPhase("idle");
    setSetupData(null);
    setCode("");
    setError("");
  };
  const run = async (fn: () => Promise<unknown>, onSuccess?: () => void) => {
    setBusy(true);
    setError("");
    try {
      await fn();
      onSuccess?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.failed"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Panel
      className="runtime-card runtime-tool-totp"
      id="runtime-totp"
    >
      <div className="panel-header">
        <strong>{t("ops.runtime.totpTitle")}</strong>
        {enabled ? (
          <StatusBadge value="success" />
        ) : (
          <span className="runtime-setting-value muted">
            {t("ops.runtime.totpDisabled")}
          </span>
        )}
      </div>
      {!enabled && phase === "idle" ? (
        <Button
          variant="secondary"
          disabled={busy}
          onClick={() =>
            void run(async () => {
              const res = await service.totpSetup();
              setSetupData(res);
              setPhase("setup");
            })
          }
        >
          {t("ops.runtime.totpSetup")}
        </Button>
      ) : null}
      {!enabled && phase === "setup" && setupData ? (
        <div className="totp-setup">
          <p className="muted" style={{ fontSize: 12 }}>
            {t("ops.runtime.totpSetupHint")}
          </p>
          <div className="totp-secret-row mono">
            <code>{setupData.secret}</code>
            <button
              type="button"
              className="redemption-copy"
              onClick={() => void navigator.clipboard.writeText(setupData.secret)}
            >
              {t("keys.redemptionCopy")}
            </button>
          </div>
          <a
            className="totp-uri-link"
            href={setupData.otpauth_uri}
            target="_blank"
            rel="noopener noreferrer"
          >
            {t("ops.runtime.totpOpenApp")}
          </a>
          <input
            type="text"
            inputMode="numeric"
            pattern="[0-9]{6}"
            maxLength={6}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            placeholder="123456"
            disabled={busy}
            style={{ width: 160 }}
          />
          <Button
            disabled={busy || code.length !== 6}
            onClick={() => void run(() => service.totpEnable(code), refresh)}
          >
            {t("ops.runtime.totpEnable")}
          </Button>
        </div>
      ) : null}
      {enabled ? (
        <div className="totp-setup">
          <p className="muted" style={{ fontSize: 12 }}>
            {t("ops.runtime.totpDisableHint")}
          </p>
          <input
            type="text"
            inputMode="numeric"
            pattern="[0-9]{6}"
            maxLength={6}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            placeholder="123456"
            disabled={busy}
            style={{ width: 160 }}
          />
          <Button
            variant="danger"
            disabled={busy || code.length !== 6}
            onClick={() => void run(() => service.totpDisable(code), refresh)}
          >
            {t("ops.runtime.totpDisable")}
          </Button>
        </div>
      ) : null}
      {error ? <div className="inline-error">{error}</div> : null}
    </Panel>
  );
}

/** Parse a daily "m h * * *" cron into wall-clock time; null for custom schedules. */
function parseDailyCron(cron: string): { hour: number; minute: number } | null {
  const parts = cron.trim().split(/\s+/);
  if (parts.length !== 5) return null;
  const [minuteRaw, hourRaw, dom, month, dow] = parts;
  if (dom !== "*" || month !== "*" || dow !== "*") return null;
  const minute = Number(minuteRaw);
  const hour = Number(hourRaw);
  if (!Number.isInteger(minute) || minute < 0 || minute > 59) return null;
  if (!Number.isInteger(hour) || hour < 0 || hour > 23) return null;
  return { hour, minute };
}

/**
 * Time picker for the daily check-in schedule. Stores back a standard
 * five-field cron ("m h * * *"); custom expressions keep the raw input.
 */
function CheckinTimePicker({
  value,
  disabled,
  onChange,
}: {
  value: string;
  disabled: boolean;
  onChange: (cron: string) => void;
}) {
  const { t } = useI18n();
  const time = parseDailyCron(value);
  if (!time) {
    // Custom expression (e.g. weekday-based): fall back to the raw input.
    return (
      <input
        disabled={disabled}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="0 8 * * *"
      />
    );
  }
  const hourOptions = Array.from({ length: 24 }, (_, h) => h);
  const minuteOptions = Array.from({ length: 12 }, (_, i) => i * 5);
  const pick = (hour: number, minute: number) =>
    onChange(`${minute} ${hour} * * *`);
  const pad = (n: number) => String(n).padStart(2, "0");
  return (
    <span className="checkin-time-picker">
      <select
        aria-label={t("ops.runtime.checkinHour")}
        disabled={disabled}
        value={time.hour}
        onChange={(e) => pick(Number(e.target.value), time.minute)}
      >
        {hourOptions.map((h) => (
          <option key={h} value={h}>
            {pad(h)}
          </option>
        ))}
      </select>
      <b>:</b>
      <select
        aria-label={t("ops.runtime.checkinMinute")}
        disabled={disabled}
        value={time.minute}
        onChange={(e) => pick(time.hour, Number(e.target.value))}
      >
        {minuteOptions.includes(time.minute) ? null : (
          <option value={time.minute}>{pad(time.minute)}</option>
        )}
        {minuteOptions.map((m) => (
          <option key={m} value={m}>
            {pad(m)}
          </option>
        ))}
      </select>
      <span className="checkin-time-hint">
        {t("ops.runtime.checkinDaily")} · {pad(time.hour)}:{pad(time.minute)}
      </span>
    </span>
  );
}

/** Admin-writable runtime parameters with hot reload. */
export function RuntimeSettingsPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const s = api(client!);
  const query = useQuery({
    queryKey: ["runtime-settings"],
    queryFn: ({ signal }) => s.runtimeSettings(signal),
  });
  const [draft, setDraft] = useState<RuntimeEditableSettings | null>(null);

  useEffect(() => {
    if (query.data?.editable) {
      setDraft({ ...query.data.editable });
    }
  }, [query.data]);

  const save = useAdminMutation({
    mutationFn: (body: RuntimeEditableSettings) =>
      s.updateRuntimeSettings(body),
    invalidateKeys: [["runtime-settings"]],
  });
  const reset = useAdminMutation({
    mutationFn: () => s.resetRuntimeSettings(),
    invalidateKeys: [["runtime-settings"]],
  });

  if (query.isPending || !draft) {
    return (
      <Panel>
        <Loading />
      </Panel>
    );
  }
  if (query.isError) {
    return (
      <Panel>
        <ErrorState error={query.error} />
      </Panel>
    );
  }
  const data = query.data!;
  const busy = save.isPending || reset.isPending;
  const patch = <K extends keyof RuntimeEditableSettings>(
    key: K,
    value: RuntimeEditableSettings[K],
  ) => {
    save.reset();
    reset.reset();
    setDraft((prev) => (prev ? { ...prev, [key]: value } : prev));
  };

  return (
    <div className="runtime-settings">
      <div className="runtime-settings-context">
        <div className="runtime-settings-context-copy">
          <strong>{t("ops.runtime.writableTitle")}</strong>
          <p>{t("ops.runtime.writableSummary")}</p>
        </div>
        <div className="runtime-settings-context-meta">
          <span className="runtime-source-pill">
            {t("ops.runtime.source")}: {t(
              data.source === "admin_override"
                ? "ops.runtime.sourceAdmin"
                : "ops.runtime.sourceEnvironment",
            )}
          </span>
          {data.updated_at ? (
            <span>
              {t("ops.runtime.updatedAt")}: {formatDate(data.updated_at)}
            </span>
          ) : null}
        </div>
      </div>

      {save.error || reset.error ? (
        <ErrorState error={save.error ?? reset.error} />
      ) : null}
      {save.isSuccess ? (
        <div className="result-strip">
          <StatusBadge value="success" />
          <span>{t("ops.runtime.saved")}</span>
        </div>
      ) : null}

      <nav className="runtime-section-nav" aria-label={t("ops.runtime.sectionNav")}>
        {RUNTIME_SECTION_ANCHORS.map(([key, i18nKey]) => (
          <button
            key={key}
            type="button"
            onClick={() =>
              document
                .getElementById(`runtime-${key}`)
                ?.scrollIntoView({ behavior: "smooth", block: "start" })
            }
          >
            {t(i18nKey)}
          </button>
        ))}
      </nav>
      <div className="runtime-settings-grid">
        <Panel className="runtime-card runtime-card-relay" id="runtime-relay">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.relay")}</strong>
          </div>
          <label
            className="check"
            style={{ marginBottom: 10 }}
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.cross_channel_failover_enabled}
              onChange={(e) =>
                patch("cross_channel_failover_enabled", e.target.checked)
              }
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.crossChannelFailover")}</span>
              <InfoTip label={t("ops.runtime.crossChannelFailoverHint")} />
            </span>
          </label>
          <label
            className="check"
            style={{ marginBottom: 10 }}
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.key_pool_rotation}
              onChange={(e) =>
                patch("key_pool_rotation", e.target.checked)
              }
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.keyPoolRotation")}</span>
              <InfoTip label={t("ops.runtime.keyPoolRotationHint")} />
            </span>
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.retryTimes")}
              hint={t("ops.runtime.retryTimesHint")}
            />
            <input
              type="number"
              min={0}
              max={100}
              disabled={busy || !draft.cross_channel_failover_enabled}
              value={draft.retry_times}
              onChange={(e) =>
                patch(
                  "retry_times",
                  numberOr(e.target.value, draft.retry_times),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.channelRetryTimes")}
              hint={t("ops.runtime.channelRetryTimesHint")}
            />
            <input
              type="number"
              min={0}
              max={5}
              disabled={busy}
              value={draft.channel_retry_times}
              onChange={(e) =>
                patch(
                  "channel_retry_times",
                  numberOr(e.target.value, draft.channel_retry_times),
                )
              }
            />
          </label>
        </Panel>

        <Panel className="runtime-card runtime-card-cooldown" id="runtime-cooldown">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.cooldown")}</strong>
          </div>
          <label
            className="check"
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.progressive_cooldown_enabled}
              onChange={(e) =>
                patch("progressive_cooldown_enabled", e.target.checked)
              }
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.progressiveCooldown")}</span>
              <InfoTip label={t("ops.runtime.progressiveCooldownHint")} />
            </span>
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.cooldown")}
              hint={t("ops.runtime.cooldownHint")}
            />
            <input
              type="number"
              min={0}
              max={86400}
              disabled={busy}
              value={draft.cooldown_seconds}
              onChange={(e) =>
                patch(
                  "cooldown_seconds",
                  numberOr(e.target.value, draft.cooldown_seconds),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.cooldownLevel2")}
              hint={t("ops.runtime.cooldownLevel2Hint")}
            />
            <input
              type="number"
              min={0}
              disabled={busy || !draft.progressive_cooldown_enabled}
              value={draft.cooldown_level2_seconds}
              onChange={(e) =>
                patch(
                  "cooldown_level2_seconds",
                  numberOr(e.target.value, draft.cooldown_level2_seconds),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.cooldownLevel3")}
              hint={t("ops.runtime.cooldownLevel3Hint")}
            />
            <input
              type="number"
              min={0}
              disabled={busy || !draft.progressive_cooldown_enabled}
              value={draft.cooldown_level3_seconds}
              onChange={(e) =>
                patch(
                  "cooldown_level3_seconds",
                  numberOr(e.target.value, draft.cooldown_level3_seconds),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.cooldownLevel4")}
              hint={t("ops.runtime.cooldownLevel4Hint")}
            />
            <input
              type="number"
              min={0}
              disabled={busy || !draft.progressive_cooldown_enabled}
              value={draft.cooldown_level4_seconds}
              onChange={(e) =>
                patch(
                  "cooldown_level4_seconds",
                  numberOr(e.target.value, draft.cooldown_level4_seconds),
                )
              }
            />
          </label>
          <p className="panel-muted" style={{ marginTop: 14, marginBottom: 6 }}>
            {t("ops.runtime.section.breaker")}
          </p>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.breakerCount")}
              hint={t("ops.runtime.breakerCountHint")}
            />
            <input
              type="number"
              min={draft.progressive_cooldown_enabled ? 5 : 0}
              disabled={busy}
              value={draft.breaker_fail_count}
              onChange={(e) =>
                patch(
                  "breaker_fail_count",
                  numberOr(e.target.value, draft.breaker_fail_count),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.modelBreaker")}
              hint={t("ops.runtime.modelBreakerHint")}
            />
            <input
              type="number"
              min={0}
              disabled={busy}
              value={draft.model_breaker_fail_count}
              onChange={(e) =>
                patch(
                  "model_breaker_fail_count",
                  numberOr(e.target.value, draft.model_breaker_fail_count),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.keyFailThreshold")}
              hint={t("ops.runtime.keyFailThresholdHint")}
            />
            <input
              type="number"
              min={0}
              disabled={busy}
              value={draft.key_fail_threshold}
              onChange={(e) =>
                patch(
                  "key_fail_threshold",
                  numberOr(e.target.value, draft.key_fail_threshold),
                )
              }
            />
          </label>
        </Panel>

        <Panel className="runtime-card runtime-card-health" id="runtime-health">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.health")}</strong>
          </div>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.autoDisable")}
              hint={t("ops.runtime.autoDisableHint")}
            />
            <input
              type="number"
              min={0}
              disabled={busy}
              value={draft.channel_auto_disable_threshold}
              onChange={(e) =>
                patch(
                  "channel_auto_disable_threshold",
                  numberOr(
                    e.target.value,
                    draft.channel_auto_disable_threshold,
                  ),
                )
              }
            />
          </label>
          <label
            className="check"
            style={{
              display: "flex",
              gap: 8,
              alignItems: "center",
              marginTop: 10,
            }}
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.recovery_probe_enabled}
              onChange={(e) =>
                patch("recovery_probe_enabled", e.target.checked)
              }
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.recoveryProbe")}</span>
              <InfoTip label={t("ops.runtime.recoveryProbeHint")} />
            </span>
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.recoveryInterval")}
              hint={t("ops.runtime.recoveryIntervalHint")}
            />
            <input
              type="number"
              min={10}
              disabled={busy || !draft.recovery_probe_enabled}
              value={draft.recovery_probe_interval_seconds}
              onChange={(e) =>
                patch(
                  "recovery_probe_interval_seconds",
                  numberOr(
                    e.target.value,
                    draft.recovery_probe_interval_seconds,
                  ),
                )
              }
            />
          </label>
          <p className="panel-muted" style={{ marginTop: 14, marginBottom: 6 }}>
            {t("ops.runtime.section.healthSweep")}
          </p>
          <label
            className="check"
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.health_sweep_enabled}
              onChange={(e) =>
                patch("health_sweep_enabled", e.target.checked)
              }
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.healthSweep")}</span>
              <InfoTip label={t("ops.runtime.healthSweepHint")} />
            </span>
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.healthSweepInterval")}
              hint={t("ops.runtime.healthSweepIntervalHint")}
            />
            <input
              type="number"
              min={10}
              max={86400}
              disabled={busy || !draft.health_sweep_enabled}
              value={draft.health_sweep_interval_seconds}
              onChange={(e) =>
                patch(
                  "health_sweep_interval_seconds",
                  numberOr(e.target.value, draft.health_sweep_interval_seconds),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.healthSweepJitter")}
              hint={t("ops.runtime.healthSweepJitterHint")}
            />
            <input
              type="number"
              min={0}
              max={3600}
              disabled={busy || !draft.health_sweep_enabled}
              value={draft.health_sweep_jitter_seconds}
              onChange={(e) =>
                patch(
                  "health_sweep_jitter_seconds",
                  numberOr(e.target.value, draft.health_sweep_jitter_seconds),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.healthSweepDegraded")}
              hint={t("ops.runtime.healthSweepDegradedHint")}
            />
            <input
              type="number"
              min={100}
              max={60000}
              disabled={busy || !draft.health_sweep_enabled}
              value={draft.health_sweep_degraded_ms}
              onChange={(e) =>
                patch(
                  "health_sweep_degraded_ms",
                  numberOr(e.target.value, draft.health_sweep_degraded_ms),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.healthSweepConcurrency")}
              hint={t("ops.runtime.healthSweepConcurrencyHint")}
            />
            <input
              type="number"
              min={1}
              max={64}
              disabled={busy || !draft.health_sweep_enabled}
              value={draft.health_sweep_concurrency}
              onChange={(e) =>
                patch(
                  "health_sweep_concurrency",
                  numberOr(e.target.value, draft.health_sweep_concurrency),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.healthSweepTimeout")}
              hint={t("ops.runtime.healthSweepTimeoutHint")}
            />
            <input
              type="number"
              min={1}
              max={120}
              disabled={busy || !draft.health_sweep_enabled}
              value={draft.health_sweep_timeout_seconds}
              onChange={(e) =>
                patch(
                  "health_sweep_timeout_seconds",
                  numberOr(e.target.value, draft.health_sweep_timeout_seconds),
                )
              }
            />
          </label>
        </Panel>

        <Panel className="runtime-card runtime-card-sticky" id="runtime-sticky">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.sticky")}</strong>
          </div>
          <label
            className="check"
            style={{ marginBottom: 10 }}
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.sticky_enabled}
              onChange={(e) => patch("sticky_enabled", e.target.checked)}
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.stickyEnabled")}</span>
              <InfoTip label={t("ops.runtime.stickyEnabledHint")} />
            </span>
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.stickyTTL")}
              hint={t("ops.runtime.stickyTTLHint")}
            />
            <input
              type="number"
              min={1}
              max={1440}
              disabled={busy || !draft.sticky_enabled}
              value={draft.sticky_ttl_minutes}
              onChange={(e) =>
                patch(
                  "sticky_ttl_minutes",
                  numberOr(e.target.value, draft.sticky_ttl_minutes),
                )
              }
            />
          </label>
        </Panel>

        <Panel className="runtime-card runtime-card-checkin" id="runtime-checkin">
          <div className="panel-header">
            <div>
              <strong>{t("ops.runtime.section.checkin")}</strong>
              <p className="panel-muted">{t("ops.runtime.checkinScope")}</p>
            </div>
            <Link className="button button-quiet" to="/checkins">
              {t("ops.runtime.openCheckin")}
            </Link>
          </div>
          <label
            className="check"
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.checkin_enabled}
              onChange={(e) => patch("checkin_enabled", e.target.checked)}
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.checkinEnabled")}</span>
              <InfoTip label={t("ops.runtime.checkinEnabledHint")} />
            </span>
          </label>
          <label className="field" style={{ marginTop: 10 }}>
            <SettingLabel
              label={t("ops.runtime.checkinCron")}
              hint={t("ops.runtime.checkinCronHint")}
            />
            <CheckinTimePicker
              value={draft.checkin_cron}
              disabled={busy}
              onChange={(cron) => patch("checkin_cron", cron)}
            />
          </label>
        </Panel>

        <Panel className="runtime-card runtime-card-limits" id="runtime-limits">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.limits")}</strong>
          </div>
          <div className="field">
            <SettingLabel
              label={t("ops.runtime.relayRate")}
              hint={t("ops.runtime.relayRateHint")}
            />
            <div className="runtime-inline-fields">
              <label className="runtime-inline-field">
                <SettingLabel
                  label={t("ops.runtime.ratePerMinute")}
                  hint={t("ops.runtime.relayRateHint")}
                />
                <input
                  type="number"
                  min={0}
                  disabled={busy}
                  value={draft.relay_rate_per_minute}
                  onChange={(e) =>
                    patch(
                      "relay_rate_per_minute",
                      numberOr(e.target.value, draft.relay_rate_per_minute),
                    )
                  }
                />
              </label>
              <label className="runtime-inline-field">
                <SettingLabel
                  label={t("ops.runtime.rateBurst")}
                  hint={t("ops.runtime.relayRateHint")}
                />
                <input
                  type="number"
                  min={0}
                  disabled={busy}
                  value={draft.relay_rate_burst}
                  onChange={(e) =>
                    patch(
                      "relay_rate_burst",
                      numberOr(e.target.value, draft.relay_rate_burst),
                    )
                  }
                />
              </label>
            </div>
          </div>
          <div className="field">
            <SettingLabel
              label={t("ops.runtime.adminRate")}
              hint={t("ops.runtime.adminRateHint")}
            />
            <div className="runtime-inline-fields">
              <label className="runtime-inline-field">
                <SettingLabel
                  label={t("ops.runtime.ratePerMinute")}
                  hint={t("ops.runtime.adminRateHint")}
                />
                <input
                  type="number"
                  min={0}
                  disabled={busy}
                  value={draft.admin_rate_per_minute}
                  onChange={(e) =>
                    patch(
                      "admin_rate_per_minute",
                      numberOr(e.target.value, draft.admin_rate_per_minute),
                    )
                  }
                />
              </label>
              <label className="runtime-inline-field">
                <SettingLabel
                  label={t("ops.runtime.rateBurst")}
                  hint={t("ops.runtime.adminRateHint")}
                />
                <input
                  type="number"
                  min={0}
                  disabled={busy}
                  value={draft.admin_rate_burst}
                  onChange={(e) =>
                    patch(
                      "admin_rate_burst",
                      numberOr(e.target.value, draft.admin_rate_burst),
                    )
                  }
                />
              </label>
            </div>
          </div>
        </Panel>

        <Panel className="runtime-card runtime-card-audit" id="runtime-audit">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.audit")}</strong>
          </div>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.auditDays")}
              hint={t("ops.runtime.auditDaysHint")}
            />
            <input
              type="number"
              min={0}
              disabled={busy}
              value={draft.audit_retention_days}
              onChange={(e) =>
                patch(
                  "audit_retention_days",
                  numberOr(e.target.value, draft.audit_retention_days),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.auditRows")}
              hint={t("ops.runtime.auditRowsHint")}
            />
            <input
              type="number"
              min={0}
              disabled={busy}
              value={draft.audit_retention_rows}
              onChange={(e) =>
                patch(
                  "audit_retention_rows",
                  numberOr(e.target.value, draft.audit_retention_rows),
                )
              }
            />
          </label>
        </Panel>
        <Panel className="runtime-card runtime-card-routing" id="runtime-routing">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.routing")}</strong>
          </div>
          <label
            className="check"
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.routing_latency_aware}
              onChange={(e) => patch("routing_latency_aware", e.target.checked)}
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.latencyAware")}</span>
              <InfoTip label={t("ops.runtime.latencyAwareHint")} />
            </span>
          </label>
          <label
            className="check"
            style={{
              display: "flex",
              gap: 8,
              alignItems: "center",
              marginTop: 10,
            }}
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.routing_error_aware}
              onChange={(e) => patch("routing_error_aware", e.target.checked)}
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.errorAware")}</span>
              <InfoTip label={t("ops.runtime.errorAwareHint")} />
            </span>
          </label>
          <label
            className="check"
            style={{
              display: "flex",
              gap: 8,
              alignItems: "center",
              marginTop: 10,
            }}
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.routing_concurrency_enabled}
              onChange={(e) =>
                patch("routing_concurrency_enabled", e.target.checked)
              }
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.concurrencyGuard")}</span>
              <InfoTip label={t("ops.runtime.concurrencyGuardHint")} />
            </span>
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.concurrencyLimit")}
              hint={t("ops.runtime.concurrencyLimitHint")}
            />
            <input
              type="number"
              min={1}
              disabled={busy}
              value={draft.routing_concurrency_limit}
              onChange={(e) =>
                patch(
                  "routing_concurrency_limit",
                  numberOr(e.target.value, draft.routing_concurrency_limit),
                )
              }
            />
          </label>
        </Panel>

        <Panel className="runtime-card runtime-card-stable-first" id="runtime-stable-first">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.stableFirst")}</strong>
          </div>
          <label
            className="check"
          >
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.stable_first_enabled}
              onChange={(e) => patch("stable_first_enabled", e.target.checked)}
            />
            <span className="setting-check-label">
              <span>{t("ops.runtime.stableFirst")}</span>
              <InfoTip label={t("ops.runtime.stableFirstHint")} />
            </span>
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.stableFirstDenominator")}
              hint={t("ops.runtime.stableFirstDenominatorHint")}
            />
            <input
              type="number"
              min={2}
              disabled={busy}
              value={draft.stable_first_denominator}
              onChange={(e) =>
                patch(
                  "stable_first_denominator",
                  numberOr(e.target.value, draft.stable_first_denominator),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.stableFirstPromote")}
              hint={t("ops.runtime.stableFirstPromoteHint")}
            />
            <input
              type="number"
              min={1}
              disabled={busy}
              value={draft.stable_first_promote_requests}
              onChange={(e) =>
                patch(
                  "stable_first_promote_requests",
                  numberOr(e.target.value, draft.stable_first_promote_requests),
                )
              }
            />
          </label>
        </Panel>

        <Panel className="runtime-card runtime-card-alerts" id="runtime-alerts">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.alerts")}</strong>
          </div>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.webhookURL")}
              hint={t("ops.runtime.webhookURLHint")}
            />
            <input
              type="url"
              placeholder="https://hooks.example.com/ops"
              disabled={busy}
              value={draft.webhook_url ?? ""}
              onChange={(e) => patch("webhook_url", e.target.value)}
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.proxyURL")}
              hint={t("ops.runtime.proxyURLHint")}
            />
            <input
              type="url"
              placeholder="http://127.0.0.1:7897"
              disabled={busy}
              value={draft.proxy_url ?? ""}
              onChange={(e) => patch("proxy_url", e.target.value)}
            />
          </label>
      <label className="field">
        <SettingLabel
          label={t("ops.runtime.discoveryCron")}
          hint={t("ops.runtime.discoveryCronHint")}
        />
        <input
          type="text"
          placeholder="0 3 * * *"
          disabled={busy}
          value={draft.discovery_cron ?? ""}
          onChange={(e) => patch("discovery_cron", e.target.value)}
        />
      </label>
      <label className="field">
        <SettingLabel
          label={t("ops.maintenance.cron")}
          hint={t("ops.maintenance.cronHint")}
        />
        <input
          type="text"
          placeholder="0 4 * * *"
          disabled={busy}
          value={draft.db_gc_cron ?? ""}
          onChange={(e) => patch("db_gc_cron", e.target.value)}
        />
      </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.webhookThrottle")}
              hint={t("ops.runtime.webhookThrottleHint")}
            />
            <input
              type="number"
              min={1}
              disabled={busy}
              value={draft.webhook_throttle_seconds}
              onChange={(e) =>
                patch(
                  "webhook_throttle_seconds",
                  numberOr(e.target.value, draft.webhook_throttle_seconds),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.alertConfigJson")}
              hint={t("ops.runtime.alertConfigJsonHint")}
            />
            <textarea
              rows={6}
              spellCheck={false}
              className="mono"
              disabled={busy}
              placeholder={
                '{"bark_url":"https://api.day.app/KEY","serverchan_key":"",' +
                '"telegram_bot_token":"","telegram_chat_id":"",' +
                '"smtp_host":"","smtp_port":587,"smtp_user":"","smtp_password":"",' +
                '"smtp_from":"","smtp_to":"","cooldown_seconds":300,' +
                '"daily_summary_enabled":true}'
              }
              value={draft.alert_config_json ?? ""}
              onChange={(e) => patch("alert_config_json", e.target.value)}
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.alertSweepInterval")}
              hint={t("ops.runtime.alertSweepIntervalHint")}
            />
            <input
              type="number"
              min={0}
              max={86400}
              disabled={busy}
              value={draft.alert_sweep_interval_seconds}
              onChange={(e) =>
                patch(
                  "alert_sweep_interval_seconds",
                  numberOr(e.target.value, draft.alert_sweep_interval_seconds),
                )
              }
            />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.alertDailyInterval")}
              hint={t("ops.runtime.alertDailyIntervalHint")}
            />
            <input
              type="number"
              min={0}
              max={86400}
              disabled={busy}
              value={draft.alert_daily_summary_interval_seconds}
              onChange={(e) =>
                patch(
                  "alert_daily_summary_interval_seconds",
                  numberOr(
                    e.target.value,
                    draft.alert_daily_summary_interval_seconds,
                  ),
                )
              }
            />
          </label>
        </Panel>

      <Panel className="runtime-card runtime-card-server" id="runtime-server">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.server")}</strong>
          </div>
          <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
            {t("ops.runtime.serverReadonly")}
          </p>
          <div className="runtime-setting-row">
            <span className="runtime-setting-label">
              {t("ops.runtime.httpAddr")}
            </span>
            <strong className="runtime-setting-value mono">
              {data.server_http_addr}
            </strong>
          </div>
          <div className="runtime-setting-row">
            <span className="runtime-setting-label">
              {t("ops.runtime.dataDir")}
            </span>
            <strong className="runtime-setting-value mono">
              {data.data_dir}
            </strong>
          </div>
          <div className="runtime-setting-row">
            <span className="runtime-setting-label">
              {t("ops.runtime.backupDir")}
            </span>
            <strong className="runtime-setting-value mono">
              {data.backup_dir}
            </strong>
          </div>
          <div className="runtime-setting-row">
            <span className="runtime-setting-label">
              {t("ops.runtime.pluginsDir")}
            </span>
            <strong className="runtime-setting-value mono">
              {data.plugins_dir}
            </strong>
          </div>
		  <div className="runtime-setting-row">
			<span className="runtime-setting-label">
			  {t("ops.runtime.metricsToken")}
			</span>
			<strong className="runtime-setting-value mono">
			  {data.metrics_token_masked
				? data.metrics_token_masked
				: t("ops.runtime.metricsTokenNone")}
			</strong>
		  </div>
      </Panel>
        </div>

      <div className="runtime-tools-grid">
        <TOTPPanel />
        <AlertRulesPanel />
        <ErrorRulesPanel />
        <PromptGuardPanel />
        <MaintenancePanel />
        <FactoryResetPanel />
      </div>

      <div className="runtime-settings-actions">
        <Button
          disabled={busy}
          onClick={() => {
            save.reset();
            save.mutate(draft);
          }}
        >
          {save.isPending ? t("common.working") : t("ops.runtime.save")}
        </Button>
        <Button
          variant="secondary"
          disabled={busy || !data.has_override}
          onClick={() => {
            reset.reset();
            reset.mutate();
          }}
        >
          {reset.isPending ? t("common.working") : t("ops.runtime.resetEnv")}
        </Button>
      </div>
    </div>
  );
}

// Error passthrough rules: status/keyword → passthrough / rewrite /
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
