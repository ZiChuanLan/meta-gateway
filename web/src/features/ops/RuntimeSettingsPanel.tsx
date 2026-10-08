import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../api/client";
import { updateState } from "../../lib/updateState";
import type { RuntimeEditableSettings } from "../../api/types";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";
import { useUnsavedChanges } from "../../lib/unsavedChanges";
import {
  Button,
  ErrorState,
  Loading,
  Panel,
  InfoTip,
  StatusBadge,
  formatDate,
} from "../../components/ui";
import { AlertRulesPanel } from "./AlertRulesPanel";
import { ErrorRulesPanel } from "./ErrorRulesPanel";
import { PromptGuardPanel } from "./PromptGuardPanel";
import { MaintenancePanel } from "./MaintenancePanel";
import { FactoryResetPanel } from "./FactoryResetPanel";
import { TOTPPanel } from "./TOTPPanel";
import { CheckinTimePicker } from "./CheckinTimePicker";
import { CronSchedulePicker } from "./CronSchedulePicker";
import { UpdateDialog } from "../UpdateDialog";

function numberOr(value: string, fallback: number) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

// Update comparison is dotted-numeric only (see updatecheck.IsNewer); anything
// else (e.g. "dev") can never register as an update, so the panel must not
// claim "up to date" for it.
function isReleaseVersion(version: string) {
  return /^v?\d+(\.\d+)*(-[\w.]+)?$/.test(version.trim());
}

/** Panel-level anchor order for the runtime settings section nav. */
const RUNTIME_SECTION_GROUPS = [
  {
    key: "traffic",
    label: "ops.runtime.navGroup.routing",
    anchors: [
      ["relay", "ops.runtime.section.relay"],
      ["routing", "ops.runtime.section.routing"],
      ["stableFirst", "ops.runtime.section.stableFirst"],
      ["sticky", "ops.runtime.section.sticky"],
    ],
  },
  {
    key: "health",
    label: "ops.runtime.navGroup.health",
    anchors: [
      ["cooldown", "ops.runtime.section.cooldown"],
      ["health", "ops.runtime.section.healthSweep"],
      ["sync", "ops.runtime.section.sync"],
      ["probe", "ops.runtime.section.probe"],
      ["site-probe", "ops.runtime.section.siteProbe"],
    ],
  },
  {
    key: "governance",
    label: "ops.runtime.navGroup.governance",
    anchors: [
      ["limits", "ops.runtime.section.limits"],
      ["audit", "ops.runtime.section.audit"],
    ],
  },
  {
    key: "ops",
    label: "ops.runtime.navGroup.ops",
    anchors: [
      ["alerts", "ops.runtime.section.alerts"],
      ["maintenance", "ops.runtime.section.maintenance"],
      ["checkin", "ops.runtime.section.checkin"],
      ["server", "ops.runtime.section.server"],
      ["deployment", "ops.runtime.section.deployment"],
    ],
  },
  {
    key: "security",
    label: "ops.runtime.navGroup.security",
    anchors: [
      ["totp", "ops.runtime.section.totp"],
      ["db-maintenance", "ops.runtime.section.dbMaintenance"],
      ["factory-reset", "ops.runtime.section.factoryReset"],
    ],
  },
] as const;

/** Which collapsible group each section-nav anchor belongs to. */
const ANCHOR_GROUP: Record<string, string> = Object.fromEntries(
  RUNTIME_SECTION_GROUPS.flatMap((group) =>
    group.anchors.map(([anchor, _]) => [anchor, group.key]),
  ),
);

/** Card count of one group, looked up by key: the group list grows at the top. */
const groupCardCount = (key: string) =>
  RUNTIME_SECTION_GROUPS.find((group) => group.key === key)?.anchors.length ?? 0;

import {
  CollapsibleGroup,
  RuntimeSettingsColumns,
  SettingLabel,
  ValidatedNumberInput,
} from "./runtimeControls";

/** Admin-writable runtime parameters with hot reload. */
export function RuntimeSettingsPanel({
  onDirtyChange,
}: {
  /** Lets the surrounding settings page guard its own tab switches. */
  onDirtyChange?: (dirty: boolean) => void;
} = {}) {
  const { client, role } = useSession();
  const { t } = useI18n();
  const s = api(client!);
  const query = useQuery({
    queryKey: ["runtime-settings"],
    queryFn: ({ signal }) => s.runtimeSettings(signal),
  });
  const [draft, setDraft] = useState<RuntimeEditableSettings | null>(null);
  const [dirty, setDirty] = useState(false);
  // The server snapshot the draft was seeded from. A background refetch is
  // compared against it so "the server moved" can be reported instead of being
  // applied over what the operator is typing (the refetch used to replace the
  // draft unconditionally whenever the query data changed).
  const baseline = useRef("");
  const [remoteChanged, setRemoteChanged] = useState(false);
  const [updateOpen, setUpdateOpen] = useState(false);
  // The panel's own update entry opens a dialog instead of navigating, so the
  // in-app predicate is not needed here any more; the browser-level guard is
  // what still protects an unsubmitted draft, and the settings page guards its
  // tab switches.
  useUnsavedChanges(dirty);

  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);

  useEffect(() => {
    const editable = query.data?.editable;
    if (!editable) return;
    const incoming = JSON.stringify(editable);
    if (dirty) {
      if (incoming !== baseline.current) setRemoteChanged(true);
      return;
    }
    baseline.current = incoming;
    setDraft({ ...editable });
    setRemoteChanged(false);
  }, [query.data, dirty]);

  const save = useAdminMutation({
    mutationFn: (body: RuntimeEditableSettings) => s.updateRuntimeSettings(body),
    invalidateKeys: [["runtime-settings"]],
    onSuccess: () => {
      setDirty(false);
    },
  });
  const reset = useAdminMutation({
    mutationFn: () => s.resetRuntimeSettings(),
    invalidateKeys: [["runtime-settings"]],
    onSuccess: () => {
      setDirty(false);
    },
  });
  const updateCheckQuery = useQuery({
    queryKey: ["update-check"],
    queryFn: ({ signal }) => s.updateCheck(signal),
    staleTime: 10 * 60_000,
    refetchInterval: 30 * 60_000,
  });
  const refreshUpdate = useAdminMutation({
    mutationFn: () => s.refreshUpdateCheck(),
    invalidateKeys: [["update-check"]],
  });

  // Take the server's values and drop the local draft. Offered next to "keep
  // mine": the operator decides which side wins, instead of the page deciding
  // for them (silently, as it used to).
  const reloadFromServer = () => {
    const editable = query.data?.editable;
    if (!editable) return;
    baseline.current = JSON.stringify(editable);
    setDraft({ ...editable });
    setDirty(false);
    save.reset();
    reset.reset();
    setRemoteChanged(false);
  };
  const keepLocalDraft = () => {
    const editable = query.data?.editable;
    if (editable) baseline.current = JSON.stringify(editable);
    setRemoteChanged(false);
  };

  const updateInfo = updateCheckQuery.data;
  // Which groups are expanded. Everything starts collapsed: the page reads as
  // six one-line rows instead of ~20 open cards, and a section becomes part of
  // the page only when someone asks for it. The security group (self-saving
  // tools) and the danger zone fold the same way.
  const [openGroups, setOpenGroups] = useState<ReadonlySet<string>>(new Set());
  // The deployment parameters are read-only facts read off the query, so their
  // filter lives outside the draft: narrowing the list must not mark the page
  // dirty or block a tab switch.
  const [paramFilter, setParamFilter] = useState("");
  const toggleGroup = (key: string) => {
    setOpenGroups((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };
  // Section nav = open the anchor's group, then scroll. Opening first is what
  // makes the nav honest: scrolling to a hidden card would do nothing.
  const navToAnchor = (anchorKey: string) => {
    const groupKey = ANCHOR_GROUP[anchorKey];
    if (groupKey) {
      setOpenGroups((current) =>
        current.has(groupKey) ? current : new Set(current).add(groupKey),
      );
    }
    // The element exists after the group renders, so scroll on the next frame.
    requestAnimationFrame(() => {
      document
        .getElementById(`runtime-${anchorKey}`)
        ?.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  };
  if (query.isError) {
    return (
      <Panel>
        <ErrorState error={query.error} retry={() => query.refetch()} />
      </Panel>
    );
  }
  if (query.isPending || !draft) {
    return (
      <Panel>
        <Loading />
      </Panel>
    );
  }
  const data = query.data!;
  const deploymentParams = (data.deployment_parameters ?? []).filter((param) =>
    param.key.toLowerCase().includes(paramFilter.trim().toLowerCase()),
  );
  const busy = save.isPending || reset.isPending;
  const patch = <K extends keyof RuntimeEditableSettings>(
    key: K,
    value: RuntimeEditableSettings[K],
  ) => {
    setDirty(true);
    save.reset();
    reset.reset();
    setDraft((prev) => (prev ? { ...prev, [key]: value } : prev));
  };

  const updateResult = (() => {
    if (!draft.update_check_enabled)
      return <span className="muted">{t("ops.runtime.updateOff")}</span>;
    if (!updateInfo) return <span className="muted">{t("ops.runtime.updateNotYet")}</span>;
    if (!updateInfo.latest && updateInfo.error)
      return <span className="muted">{t("ops.runtime.updateFailed")}</span>;
    if (!isReleaseVersion(updateInfo.current))
      return (
        <span className="muted">
          {t("ops.runtime.updateDevBuild", {
            current: updateInfo.current,
            version: updateInfo.latest || "—",
          })}
        </span>
      );
    if (updateInfo.has_update)
      return (
        <a
          className="runtime-update-link"
          href={updateInfo.release_url || undefined}
          target="_blank"
          rel="noreferrer"
        >
          {t("ops.runtime.updateFound", { version: updateInfo.latest })}
        </a>
      );
    const state = updateState(updateInfo);
    if (state === "current") return <span>{t("ops.runtime.updateUpToDate")}</span>;
    if (state === "ahead")
      return (
        <span>
          {t("updates.channelIsNewer", {
            current: updateInfo.current,
            latest: updateInfo.latest,
            channel: t(updateInfo.channel === "beta" ? "updates.beta" : "updates.stable"),
          })}
        </span>
      );
    return <span>{t(`updates.state.${state}`)}</span>;
  })();

  return (
    <div className="runtime-settings">
      <div className="runtime-settings-context">
        <div className="runtime-settings-context-copy">
          <strong>{t("ops.runtime.writableTitle")}</strong>
          <p>{t("ops.runtime.writableSummary")}</p>
        </div>
        <div className="runtime-settings-context-meta">
          <span className="runtime-source-pill">
            {t("ops.runtime.source")}:{" "}
            {t(
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

      {save.error || reset.error ? <ErrorState error={save.error ?? reset.error} /> : null}
      {save.isSuccess ? (
        <div className="result-strip">
          <StatusBadge value="success" />
          <span>{t("ops.runtime.saved")}</span>
        </div>
      ) : null}

      <nav className="runtime-section-nav" aria-label={t("ops.runtime.sectionNav")}>
        {RUNTIME_SECTION_GROUPS.map((group) => (
          <div key={group.key} className="runtime-nav-group">
            <span className="runtime-nav-group-label">{t(group.label)}</span>
            {group.anchors.map(([key, i18nKey]) => (
              <button key={key} type="button" onClick={() => navToAnchor(key)}>
                {t(i18nKey)}
              </button>
            ))}
          </div>
        ))}
      </nav>
      {/* The multi-user area owns its own switch now (Users → Overview), because
          it turns a product capability on — not a tuning knob. What stays here
          is the way in: a personal gateway hides the navigation entry, so
          without this link the module would be unreachable from Settings. */}
      <Panel className="runtime-card" id="runtime-multiuser">
        <div className="panel-header">
          <strong>{t("ops.tab.mode")}</strong>
        </div>
        <p className="muted panel-lede">{t("ops.runtime.multiUserHint")}</p>
        <Link className="button" to="/users">
          {t("ops.runtime.openUsers")}
        </Link>
      </Panel>
      <CollapsibleGroup
        id="runtime-group-traffic"
        title={t("ops.runtime.navGroup.routing")}
        description={t("ops.runtime.group.routingDesc")}
        cardCount={groupCardCount("traffic")}
        open={openGroups.has("traffic")}
        onToggle={() => toggleGroup("traffic")}
      >
        <RuntimeSettingsColumns>
          <Panel className="runtime-card runtime-card-relay" id="runtime-relay">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.relay")}</strong>
            </div>
            <label className="check is-stacked">
              <input
                type="checkbox"
                disabled={busy}
                checked={draft.cross_channel_failover_enabled}
                onChange={(e) => patch("cross_channel_failover_enabled", e.target.checked)}
              />
              <span className="setting-check-label">
                <span>{t("ops.runtime.crossChannelFailover")}</span>
                <InfoTip label={t("ops.runtime.crossChannelFailoverHint")} />
              </span>
            </label>

            <label className="field">
              <SettingLabel
                label={t("ops.runtime.retryTimes")}
                hint={t("ops.runtime.retryTimesHint")}
              />
              <ValidatedNumberInput
                min={0}
                max={100}
                disabled={busy || !draft.cross_channel_failover_enabled}
                value={draft.retry_times}
                onChange={(e) => patch("retry_times", numberOr(e.target.value, draft.retry_times))}
              />
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.channelRetryTimes")}
                hint={t("ops.runtime.channelRetryTimesHint")}
              />
              <ValidatedNumberInput
                min={0}
                max={5}
                disabled={busy}
                value={draft.channel_retry_times}
                onChange={(e) =>
                  patch("channel_retry_times", numberOr(e.target.value, draft.channel_retry_times))
                }
              />
            </label>
          </Panel>

          <Panel className="runtime-card runtime-card-routing" id="runtime-routing">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.routing")}</strong>
            </div>
            <label className="check">
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
            <label className="check is-spaced">
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
            <label className="check is-spaced">
              <input
                type="checkbox"
                disabled={busy}
                checked={draft.routing_concurrency_enabled}
                onChange={(e) => patch("routing_concurrency_enabled", e.target.checked)}
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
              <ValidatedNumberInput
                min={1}
                max={100000}
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
            <label className="check">
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
              <ValidatedNumberInput
                min={2}
                max={1000}
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
              <ValidatedNumberInput
                min={1}
                max={100000}
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

          <Panel className="runtime-card runtime-card-sticky" id="runtime-sticky">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.sticky")}</strong>
            </div>
            <label className="check is-stacked">
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
              <ValidatedNumberInput
                min={1}
                max={1440}
                disabled={busy || !draft.sticky_enabled}
                value={draft.sticky_ttl_minutes}
                onChange={(e) =>
                  patch("sticky_ttl_minutes", numberOr(e.target.value, draft.sticky_ttl_minutes))
                }
              />
            </label>
          </Panel>
        </RuntimeSettingsColumns>
      </CollapsibleGroup>
      <CollapsibleGroup
        id="runtime-group-health"
        title={t("ops.runtime.navGroup.health")}
        description={t("ops.runtime.group.healthDesc")}
        cardCount={RUNTIME_SECTION_GROUPS[1].anchors.length}
        open={openGroups.has("health")}
        onToggle={() => toggleGroup("health")}
      >
        <RuntimeSettingsColumns>
          <Panel className="runtime-card runtime-card-cooldown" id="runtime-cooldown">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.cooldown")}</strong>
            </div>
            <label className="check">
              <input
                type="checkbox"
                disabled={busy}
                checked={draft.fault_protection_enabled}
                onChange={(e) => patch("fault_protection_enabled", e.target.checked)}
              />
              <span className="setting-check-label">
                <span>{t("ops.runtime.faultProtection")}</span>
                <InfoTip label={t("ops.runtime.faultProtectionHint")} />
              </span>
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.cooldown")}
                hint={t("ops.runtime.cooldownHint")}
              />
              <ValidatedNumberInput
                min={0}
                max={86400}
                disabled={busy || !draft.fault_protection_enabled}
                value={draft.cooldown_seconds}
                onChange={(e) =>
                  patch("cooldown_seconds", numberOr(e.target.value, draft.cooldown_seconds))
                }
              />
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.autoDisable")}
                hint={t("ops.runtime.autoDisableHint")}
              />
              <ValidatedNumberInput
                min={0}
                max={1000}
                disabled={busy || !draft.fault_protection_enabled}
                value={draft.channel_auto_disable_threshold}
                onChange={(e) =>
                  patch(
                    "channel_auto_disable_threshold",
                    numberOr(e.target.value, draft.channel_auto_disable_threshold),
                  )
                }
              />
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.recoveryInterval")}
                hint={t("ops.runtime.recoveryIntervalHint")}
              />
              <ValidatedNumberInput
                min={10}
                max={86400}
                disabled={busy || !draft.fault_protection_enabled || !draft.recovery_probe_enabled}
                value={draft.recovery_probe_interval_seconds}
                onChange={(e) =>
                  patch(
                    "recovery_probe_interval_seconds",
                    numberOr(e.target.value, draft.recovery_probe_interval_seconds),
                  )
                }
              />
            </label>
            <label className="check">
              <input
                type="checkbox"
                disabled={busy || !draft.fault_protection_enabled}
                checked={draft.recovery_probe_enabled}
                onChange={(e) => patch("recovery_probe_enabled", e.target.checked)}
              />
              <span className="setting-check-label">
                <span>{t("ops.runtime.recoveryProbe")}</span>
                <InfoTip label={t("ops.runtime.recoveryProbeHint")} />
              </span>
            </label>
          </Panel>

          <Panel className="runtime-card runtime-card-health" id="runtime-health">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.healthSweep")}</strong>
            </div>
            <label className="check">
              <input
                type="checkbox"
                disabled={busy}
                checked={draft.health_sweep_enabled}
                onChange={(e) => patch("health_sweep_enabled", e.target.checked)}
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
              <ValidatedNumberInput
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
              <ValidatedNumberInput
                min={0}
                max={3600}
                disabled={busy || !draft.health_sweep_enabled}
                customError={
                  draft.health_sweep_interval_seconds >= 10 &&
                  draft.health_sweep_interval_seconds <= 86400 &&
                  draft.health_sweep_jitter_seconds > draft.health_sweep_interval_seconds
                    ? t("ops.runtime.validation.jitterExceedsInterval", {
                        interval: draft.health_sweep_interval_seconds,
                      })
                    : undefined
                }
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
              <ValidatedNumberInput
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
              <ValidatedNumberInput
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
              <ValidatedNumberInput
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

          <Panel className="runtime-card runtime-card-sync" id="runtime-sync">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.sync")}</strong>
            </div>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.discoveryCron")}
                hint={t("ops.runtime.discoveryCronHint")}
              />
              <CronSchedulePicker
                disabled={busy}
                value={draft.discovery_cron ?? ""}
                onChange={(cron) => patch("discovery_cron", cron)}
              />
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.defaultModelSyncMode")}
                hint={t("ops.runtime.defaultModelSyncModeHint")}
              />
              <select
                disabled={busy}
                value={draft.default_model_sync_mode ?? "manual"}
                onChange={(e) =>
                  patch("default_model_sync_mode", e.target.value === "auto" ? "auto" : "manual")
                }
              >
                <option value="manual">{t("channels.syncModeManual")}</option>
                <option value="auto">{t("channels.syncModeAuto")}</option>
              </select>
            </label>
          </Panel>

          <Panel className="runtime-card runtime-card-probe" id="runtime-probe">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.probe")}</strong>
            </div>
            <p className="muted panel-lede">{t("ops.runtime.probeIntro")}</p>
            {/* The scope is chosen where the pick lists are (模型 → 模型工具 → 模型探测),
                so this card states it instead of offering a second editor for it. */}
            <p className="muted" role="status">
              {t("ops.runtime.probeScope", {
                channels:
                  (draft.probe_channels ?? []).length === 0
                    ? t("ops.runtime.probeScopeAllChannels")
                    : t("ops.runtime.probeScopeChannels", {
                        count: (draft.probe_channels ?? []).length,
                      }),
                models:
                  (draft.probe_models ?? []).length === 0
                    ? t("ops.runtime.probeScopeAllModels")
                    : t("ops.runtime.probeScopeModels", {
                        count: (draft.probe_models ?? []).length,
                      }),
              })}
              <span className="field-hint"> {t("ops.runtime.probeScopeHint")}</span>
            </p>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.probeCron")}
                hint={t("ops.runtime.probeCronHint")}
              />
              <CronSchedulePicker
                disabled={busy}
                value={draft.probe_cron ?? ""}
                onChange={(cron) => patch("probe_cron", cron)}
              />
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.probePrompt")}
                hint={t("ops.runtime.probePromptHint")}
              />
              <input
                type="text"
                placeholder="hi"
                disabled={busy}
                value={draft.probe_prompt ?? ""}
                onChange={(e) => patch("probe_prompt", e.target.value)}
              />
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.probeMaxTokens")}
                hint={t("ops.runtime.probeMaxTokensHint")}
              />
              <input
                type="number"
                min={1}
                max={256}
                disabled={busy}
                value={draft.probe_max_tokens ?? 1}
                onChange={(e) => patch("probe_max_tokens", Number(e.target.value))}
              />
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.probeConcurrency")}
                hint={t("ops.runtime.probeConcurrencyHint")}
              />
              <input
                type="number"
                min={1}
                max={16}
                disabled={busy}
                value={draft.probe_concurrency ?? 4}
                onChange={(e) => patch("probe_concurrency", Number(e.target.value))}
              />
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.probeAutoDisable")}
                hint={t("ops.runtime.probeAutoDisableHint")}
              />
              <input
                type="number"
                min={0}
                max={10}
                disabled={busy}
                value={draft.probe_auto_disable ?? 0}
                onChange={(e) => patch("probe_auto_disable", Number(e.target.value))}
              />
            </label>
          </Panel>

          <Panel className="runtime-card runtime-card-site-probe" id="runtime-site-probe">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.siteProbe")}</strong>
            </div>
            <p className="muted panel-lede">{t("ops.runtime.siteProbeIntro")}</p>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.siteProbeInterval")}
                hint={t("ops.runtime.siteProbeIntervalHint")}
              />
              <ValidatedNumberInput
                min={60}
                max={86400}
                disabled={busy}
                value={draft.site_probe_interval_seconds}
                onChange={(e) =>
                  patch(
                    "site_probe_interval_seconds",
                    numberOr(e.target.value, draft.site_probe_interval_seconds),
                  )
                }
              />
            </label>
            <label className="field">
              <SettingLabel
                label={t("ops.runtime.siteProbeJitter")}
                hint={t("ops.runtime.siteProbeJitterHint")}
              />
              <ValidatedNumberInput
                min={0}
                max={3600}
                disabled={busy}
                customError={
                  draft.site_probe_interval_seconds >= 60 &&
                  draft.site_probe_jitter_seconds > draft.site_probe_interval_seconds
                    ? t("ops.runtime.validation.jitterExceedsInterval", {
                        interval: draft.site_probe_interval_seconds,
                      })
                    : undefined
                }
                value={draft.site_probe_jitter_seconds}
                onChange={(e) =>
                  patch(
                    "site_probe_jitter_seconds",
                    numberOr(e.target.value, draft.site_probe_jitter_seconds),
                  )
                }
              />
            </label>
          </Panel>
        </RuntimeSettingsColumns>
      </CollapsibleGroup>
      <CollapsibleGroup
        id="runtime-group-governance"
        title={t("ops.runtime.navGroup.governance")}
        description={t("ops.runtime.group.governanceDesc")}
        cardCount={RUNTIME_SECTION_GROUPS[2].anchors.length}
        open={openGroups.has("governance")}
        onToggle={() => toggleGroup("governance")}
      >
        <RuntimeSettingsColumns>
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
                  <ValidatedNumberInput
                    min={0}
                    max={1000000}
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
                  <ValidatedNumberInput
                    min={0}
                    max={1000000}
                    disabled={busy}
                    value={draft.relay_rate_burst}
                    onChange={(e) =>
                      patch("relay_rate_burst", numberOr(e.target.value, draft.relay_rate_burst))
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
                  <ValidatedNumberInput
                    min={0}
                    max={1000000}
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
                  <ValidatedNumberInput
                    min={0}
                    max={1000000}
                    disabled={busy}
                    value={draft.admin_rate_burst}
                    onChange={(e) =>
                      patch("admin_rate_burst", numberOr(e.target.value, draft.admin_rate_burst))
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
              <ValidatedNumberInput
                min={0}
                max={36500}
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
              <ValidatedNumberInput
                min={0}
                max={10000000}
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

          {/* Alert, error and prompt rules ARE governance: they decide what the
            gateway refuses, rewrites or escalates. They previously sat in a
            "tools" grid next to two-factor setup and database wipe, which made
            three policy editors read as miscellaneous utilities. */}
          <AlertRulesPanel />
          <ErrorRulesPanel />
          <PromptGuardPanel />
        </RuntimeSettingsColumns>
      </CollapsibleGroup>
      <CollapsibleGroup
        id="runtime-group-ops"
        title={t("ops.runtime.navGroup.ops")}
        description={t("ops.runtime.group.opsDesc")}
        cardCount={groupCardCount("ops")}
        open={openGroups.has("ops")}
        onToggle={() => toggleGroup("ops")}
      >
        <RuntimeSettingsColumns>
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
                label={t("ops.runtime.webhookThrottle")}
                hint={t("ops.runtime.webhookThrottleHint")}
              />
              <ValidatedNumberInput
                min={1}
                max={86400}
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
              <ValidatedNumberInput
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
              <ValidatedNumberInput
                min={0}
                max={86400}
                disabled={busy}
                value={draft.alert_daily_summary_interval_seconds}
                onChange={(e) =>
                  patch(
                    "alert_daily_summary_interval_seconds",
                    numberOr(e.target.value, draft.alert_daily_summary_interval_seconds),
                  )
                }
              />
            </label>
          </Panel>

          <Panel className="runtime-card runtime-card-maintenance" id="runtime-maintenance">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.maintenance")}</strong>
            </div>
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
            <label className="check">
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
            <label className="field is-spaced">
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

          <Panel className="runtime-card runtime-card-server" id="runtime-server">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.server")}</strong>
            </div>
            <label className="field is-stacked">
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
            <div className="runtime-setting-sub">
              <span className="runtime-setting-subtitle">{t("ops.runtime.outboundLimits")}</span>
              <span className="field-hint">{t("ops.runtime.outboundLimitsHint")}</span>
            </div>
            <div className="field-row">
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.outboundConnectTimeout")}
                  hint={t("ops.runtime.outboundConnectTimeoutHint")}
                />
                <input
                  type="number"
                  min={0}
                  max={600}
                  disabled={busy}
                  value={draft.outbound_connect_timeout_seconds ?? 0}
                  onChange={(e) =>
                    patch("outbound_connect_timeout_seconds", Number(e.target.value))
                  }
                />
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.outboundHeaderTimeout")}
                  hint={t("ops.runtime.outboundHeaderTimeoutHint")}
                />
                <input
                  type="number"
                  min={0}
                  max={3600}
                  disabled={busy}
                  value={draft.outbound_header_timeout_seconds ?? 0}
                  onChange={(e) => patch("outbound_header_timeout_seconds", Number(e.target.value))}
                />
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.outboundImageHeaderTimeout")}
                  hint={t("ops.runtime.outboundImageHeaderTimeoutHint")}
                />
                <input
                  type="number"
                  min={0}
                  max={3600}
                  disabled={busy}
                  value={draft.outbound_image_header_timeout_seconds ?? 0}
                  onChange={(e) =>
                    patch("outbound_image_header_timeout_seconds", Number(e.target.value))
                  }
                />
              </label>
            </div>
            <div className="field-row">
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.outboundTLSTimeout")}
                  hint={t("ops.runtime.outboundTLSTimeoutHint")}
                />
                <input
                  type="number"
                  min={0}
                  max={600}
                  disabled={busy}
                  value={draft.outbound_tls_timeout_seconds ?? 0}
                  onChange={(e) => patch("outbound_tls_timeout_seconds", Number(e.target.value))}
                />
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.outboundMaxIdleConns")}
                  hint={t("ops.runtime.outboundMaxIdleConnsHint")}
                />
                <input
                  type="number"
                  min={0}
                  max={100000}
                  disabled={busy}
                  value={draft.outbound_max_idle_conns ?? 0}
                  onChange={(e) => patch("outbound_max_idle_conns", Number(e.target.value))}
                />
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.outboundMaxIdleConnsPerHost")}
                  hint={t("ops.runtime.outboundMaxIdleConnsPerHostHint")}
                />
                <input
                  type="number"
                  min={0}
                  max={100000}
                  disabled={busy}
                  value={draft.outbound_max_idle_conns_per_host ?? 0}
                  onChange={(e) =>
                    patch("outbound_max_idle_conns_per_host", Number(e.target.value))
                  }
                />
              </label>
            </div>
            <p className="muted panel-lede">{t("ops.runtime.serverReadonly")}</p>
            <div className="runtime-setting-row">
              <span className="runtime-setting-label">{t("ops.runtime.buildVersion")}</span>
              <strong className="runtime-setting-value mono">
                {updateCheckQuery.data?.current ?? "…"}
              </strong>
            </div>
            <div className="runtime-setting-row">
              <span className="runtime-setting-label">{t("ops.runtime.httpAddr")}</span>
              <strong className="runtime-setting-value mono">{data.server_http_addr}</strong>
            </div>
            <div className="runtime-setting-row">
              <span className="runtime-setting-label">{t("ops.runtime.dataDir")}</span>
              <strong className="runtime-setting-value mono">{data.data_dir}</strong>
            </div>
            <div className="runtime-setting-row">
              <span className="runtime-setting-label">{t("ops.runtime.backupDir")}</span>
              <strong className="runtime-setting-value mono">{data.backup_dir}</strong>
            </div>
            <div className="runtime-setting-row">
              <span className="runtime-setting-label">{t("ops.runtime.pluginsDir")}</span>
              <strong className="runtime-setting-value mono">{data.plugins_dir}</strong>
            </div>
            <div className="runtime-setting-row">
              <span className="runtime-setting-label">{t("ops.runtime.metricsToken")}</span>
              <strong className="runtime-setting-value mono">
                {data.metrics_token_masked
                  ? data.metrics_token_masked
                  : t("ops.runtime.metricsTokenNone")}
              </strong>
            </div>
            <label className="check is-section">
              <input
                type="checkbox"
                disabled={busy}
                checked={draft.update_check_enabled}
                onChange={(e) => patch("update_check_enabled", e.target.checked)}
              />
              <span className="setting-check-label">
                <span>{t("ops.runtime.updateCheck")}</span>
                <InfoTip label={t("ops.runtime.updateCheckHint")} />
              </span>
            </label>
            <div className="runtime-update-row">
              <Button
                variant="secondary"
                disabled={refreshUpdate.isPending || !draft.update_check_enabled}
                onClick={() => refreshUpdate.mutate()}
              >
                {refreshUpdate.isPending
                  ? t("ops.runtime.updateChecking")
                  : t("ops.runtime.updateNow")}
              </Button>
              <span className="runtime-update-result">{updateResult}</span>
            </div>
            {role === null || role === "owner" ? (
              <Button variant="secondary" onClick={() => setUpdateOpen(true)}>
                {t("updates.dialogTitle")}
              </Button>
            ) : null}
          </Panel>

          {/* The environment layer: what the process actually read at startup.
              Without it, "the gateway timed out at 60s" is unanswerable from the
              console — the ceiling is in a deployment file, and a variable
              compose never passed looks exactly like a deliberate default. */}
          <Panel className="runtime-card runtime-card-deployment" id="runtime-deployment">
            <div className="panel-header">
              <strong>{t("ops.runtime.section.deployment")}</strong>
            </div>
            <p className="muted panel-lede">{t("ops.runtime.deploymentLede")}</p>
            <input
              className="runtime-param-filter"
              type="search"
              value={paramFilter}
              placeholder={t("ops.runtime.paramFilter")}
              aria-label={t("ops.runtime.paramFilter")}
              onChange={(event) => setParamFilter(event.target.value)}
            />
            <div className="runtime-param-list">
              {deploymentParams.map((param) => (
                <div className="runtime-param-row" key={param.key}>
                  <code className="runtime-param-key">{param.key}</code>
                  <span className="runtime-param-value mono">{param.value || "—"}</span>
                  <span className={`runtime-param-source${param.from_env ? " is-env" : ""}`}>
                    {param.from_env ? t("ops.runtime.paramFromEnv") : t("ops.runtime.paramDefault")}
                  </span>
                </div>
              ))}
              {deploymentParams.length === 0 ? (
                <p className="muted runtime-param-empty">{t("ops.runtime.paramEmpty")}</p>
              ) : null}
            </div>
          </Panel>
        </RuntimeSettingsColumns>
      </CollapsibleGroup>

      {/* Account security and database upkeep save themselves — neither is part
          of the runtime draft this page's Save button commits. Keeping them in
          the same grid as traffic policy blurred that boundary. */}
      <CollapsibleGroup
        id="runtime-group-security"
        title={t("ops.runtime.group.security")}
        description={t("ops.runtime.group.securityDesc")}
        cardCount={2}
        open={openGroups.has("security")}
        onToggle={() => toggleGroup("security")}
      >
        <div className="runtime-tools-grid">
          <TOTPPanel />
          <MaintenancePanel />
        </div>
      </CollapsibleGroup>

      {/* An irreversible wipe must not be a peer card in a settings grid. It
          gets its own terminal region, last on the page, reached by deliberate
          scroll rather than brushed past en route to something else. */}
      <CollapsibleGroup
        id="runtime-group-danger"
        title={t("ops.runtime.group.danger")}
        description={t("ops.runtime.group.dangerDesc")}
        open={openGroups.has("danger")}
        onToggle={() => toggleGroup("danger")}
        danger
      >
        <FactoryResetPanel />
      </CollapsibleGroup>

      {remoteChanged ? (
        <div className="runtime-settings-notice" role="status">
          <div className="runtime-settings-notice-copy">
            <strong>{t("ops.runtime.remoteChanged")}</strong>
            <p className="field-hint">{t("ops.runtime.remoteChangedHint")}</p>
          </div>
          <span className="flex-spacer" />
          <Button variant="secondary" onClick={keepLocalDraft}>
            {t("ops.runtime.keepMine")}
          </Button>
          <Button variant="secondary" onClick={reloadFromServer}>
            {t("ops.runtime.reloadServer")}
          </Button>
        </div>
      ) : null}

      <div className="runtime-settings-actions">
        {dirty && !remoteChanged ? (
          <span className="runtime-settings-unsaved" role="status">
            {t("ops.runtime.unsaved")}
          </span>
        ) : null}
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
      {/* The update entry used to deep-link into Settings → update channel. The
          channel is not a setting (it is the deployment's image tag), so the
          entry now opens the same dialog the top-bar pill does. */}
      {updateOpen && updateCheckQuery.data ? (
        <UpdateDialog update={updateCheckQuery.data} onClose={() => setUpdateOpen(false)} />
      ) : null}
    </div>
  );
}
