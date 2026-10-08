import { useQuery } from "@tanstack/react-query";
import {
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type FocusEvent,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import { ChevronRight, LoaderCircle } from "lucide-react";
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
import {
  CollapsibleGroup,
  SettingLabel,
  SettingState,
  ValidatedNumberInput,
} from "./runtimeControls";
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

type EditableKey = keyof RuntimeEditableSettings;

/**
 * Settings where 0 does not mean "zero" but "no override, inherit the
 * deployment default" (see runtimeconfig.withOutboundDefaults / unsetIfZero).
 * The backend already resolves them before answering, so a stored 0 comes back
 * as the deployment value — comparing the raw draft against env_bootstrap would
 * otherwise call a field the operator just cleared "changed".
 */
const ZERO_MEANS_DEFAULT: ReadonlySet<EditableKey> = new Set<EditableKey>([
  "outbound_connect_timeout_seconds",
  "outbound_header_timeout_seconds",
  "outbound_image_header_timeout_seconds",
  "outbound_tls_timeout_seconds",
  "outbound_max_idle_conns",
  "outbound_max_idle_conns_per_host",
  "site_probe_interval_seconds",
]);

interface RuntimeSection {
  key: string;
  /** i18n key for the section name; shared by the sidebar and the card title. */
  label: string;
  /**
   * Draft fields the section owns. Empty means the section saves itself (its
   * own API calls) or is a link: no "changed" badge and no section reset.
   */
  fields: readonly EditableKey[];
}

interface RuntimeGroup {
  key: string;
  label: string;
  sections: readonly RuntimeSection[];
}

/** The landing section, and the fallback when a stored key no longer exists. */
const RELAY_SECTION: RuntimeSection = {
  key: "relay",
  label: "ops.runtime.section.relay",
  fields: ["cross_channel_failover_enabled", "retry_times", "channel_retry_times"],
};

/**
 * Three semantic groups instead of five navigational ones, and a sidebar
 * instead of a chip grid: nineteen peers in one scroll answered "what exists",
 * never "where does what I need live". The split is by what a setting decides —
 * where a request goes, what watches the upstreams, what is kept and operated
 * afterwards.
 */
const RUNTIME_GROUPS: readonly RuntimeGroup[] = [
  {
    key: "forwarding",
    label: "ops.runtime.navGroup.forwarding",
    sections: [
      RELAY_SECTION,
      {
        key: "routing",
        label: "ops.runtime.section.routing",
        fields: [
          "routing_latency_aware",
          "routing_error_aware",
          "routing_concurrency_enabled",
          "routing_concurrency_limit",
        ],
      },
      {
        key: "stableFirst",
        label: "ops.runtime.section.stableFirst",
        fields: [
          "stable_first_enabled",
          "stable_first_denominator",
          "stable_first_promote_requests",
        ],
      },
      {
        key: "sticky",
        label: "ops.runtime.section.sticky",
        fields: ["sticky_enabled", "sticky_ttl_minutes"],
      },
      {
        key: "limits",
        label: "ops.runtime.section.limits",
        fields: [
          "relay_rate_per_minute",
          "relay_rate_burst",
          "admin_rate_per_minute",
          "admin_rate_burst",
        ],
      },
      // Error passthrough and prompt guards are request-path policy: they decide
      // what the gateway rewrites or refuses on the way through.
      { key: "errorRules", label: "ops.errorRules.title", fields: [] },
      { key: "promptGuard", label: "ops.guard.title", fields: [] },
    ],
  },
  {
    key: "automation",
    label: "ops.runtime.navGroup.automation",
    sections: [
      {
        key: "cooldown",
        label: "ops.runtime.section.cooldown",
        fields: [
          "fault_protection_enabled",
          "cooldown_seconds",
          "channel_auto_disable_threshold",
          "recovery_probe_interval_seconds",
          "recovery_probe_enabled",
        ],
      },
      {
        key: "health",
        label: "ops.runtime.section.healthSweep",
        fields: [
          "health_sweep_enabled",
          "health_sweep_interval_seconds",
          "health_sweep_jitter_seconds",
          "health_sweep_degraded_ms",
          "health_sweep_concurrency",
          "health_sweep_timeout_seconds",
        ],
      },
      {
        key: "sync",
        label: "ops.runtime.section.sync",
        fields: ["discovery_cron", "default_model_sync_mode"],
      },
      {
        key: "probe",
        label: "ops.runtime.section.probe",
        fields: [
          "probe_cron",
          "probe_prompt",
          "probe_max_tokens",
          "probe_concurrency",
          "probe_auto_disable",
          "probe_channels",
          "probe_models",
        ],
      },
      {
        key: "siteProbe",
        label: "ops.runtime.section.siteProbe",
        fields: ["site_probe_interval_seconds", "site_probe_jitter_seconds"],
      },
      {
        key: "checkin",
        label: "ops.runtime.section.checkin",
        fields: ["checkin_enabled", "checkin_cron"],
      },
    ],
  },
  {
    key: "data",
    label: "ops.runtime.navGroup.data",
    sections: [
      {
        key: "audit",
        label: "ops.runtime.section.audit",
        fields: ["audit_retention_days", "audit_retention_rows"],
      },
      {
        key: "alerts",
        label: "ops.runtime.section.alerts",
        fields: [
          "webhook_url",
          "webhook_throttle_seconds",
          "alert_config_json",
          "alert_sweep_interval_seconds",
          "alert_daily_summary_interval_seconds",
        ],
      },
      { key: "alertRules", label: "ops.alertRules.title", fields: [] },
      { key: "maintenance", label: "ops.runtime.section.maintenance", fields: ["db_gc_cron"] },
      {
        key: "server",
        label: "ops.runtime.section.server",
        fields: [
          "proxy_url",
          "outbound_connect_timeout_seconds",
          "outbound_header_timeout_seconds",
          "outbound_image_header_timeout_seconds",
          "outbound_tls_timeout_seconds",
          "outbound_max_idle_conns",
          "outbound_max_idle_conns_per_host",
          "update_check_enabled",
        ],
      },
      { key: "totp", label: "ops.runtime.section.totp", fields: [] },
      { key: "dbMaintenance", label: "ops.runtime.section.dbMaintenance", fields: [] },
      { key: "users", label: "ops.runtime.section.users", fields: [] },
      { key: "danger", label: "ops.runtime.section.factoryReset", fields: [] },
    ],
  },
];

const ALL_SECTIONS: readonly RuntimeSection[] = RUNTIME_GROUPS.flatMap((group) => group.sections);
const SECTION_BY_KEY = new Map(ALL_SECTIONS.map((section) => [section.key, section]));
const DEFAULT_SECTION_KEY = RELAY_SECTION.key;

/** undefined and "" are the same absent value across the API boundary. */
function scalarOrEmpty(value: unknown) {
  return value === undefined || value === null ? "" : value;
}

function sameSetting(a: unknown, b: unknown) {
  if (Array.isArray(a) || Array.isArray(b)) {
    return JSON.stringify(a ?? []) === JSON.stringify(b ?? []);
  }
  return scalarOrEmpty(a) === scalarOrEmpty(b);
}

/**
 * One section: its card, the badge that says whether it overrides the
 * deployment default, and the way back to that default. With instant save there
 * is no page-level Save to undo a section with, so the undo lives on the
 * section itself.
 */
function SectionCard({
  section,
  changed,
  busy,
  onRestore,
  actions,
  children,
}: {
  section: RuntimeSection;
  changed: boolean;
  busy: boolean;
  onRestore: () => void;
  actions?: ReactNode;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <Panel className={`runtime-card runtime-card-${section.key}`} id={`runtime-${section.key}`}>
      <div className="panel-header">
        <strong>{t(section.label)}</strong>
        <SettingState state={changed ? "changed" : "default"} />
        <span className="flex-spacer" />
        {actions}
        <Button variant="quiet" disabled={busy || !changed} onClick={onRestore}>
          {t("ops.runtime.resetSection")}
        </Button>
      </div>
      {children}
    </Panel>
  );
}

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
  // Every edit goes through the ref before it goes through state: the
  // container-level blur/change handlers run in the same tick as the edit and
  // would otherwise read the previous render's draft.
  const draftRef = useRef<RuntimeEditableSettings | null>(null);
  // The last server-confirmed snapshot. "Dirty" means the draft drifted from
  // it, which is what the unload guard and the status line report.
  const baselineRef = useRef("");
  const [baseline, setBaseline] = useState("");
  const [remoteChanged, setRemoteChanged] = useState(false);
  const [saveState, setSaveState] = useState<"idle" | "saving" | "saved" | "error">("idle");
  // One write at a time: a commit made while the previous one is in flight is
  // coalesced into a single follow-up PUT of the latest draft, so two rapid
  // toggles cannot land out of order.
  const savingRef = useRef(false);
  const queuedRef = useRef(false);
  const [activeSection, setActiveSection] = useState<string>(DEFAULT_SECTION_KEY);
  const [updateOpen, setUpdateOpen] = useState(false);
  // The deployment parameters are read-only facts read off the query, so their
  // filter lives outside the draft: narrowing the list must not mark the page
  // dirty or block a tab switch.
  const [paramFilter, setParamFilter] = useState("");
  const [paramShowAll, setParamShowAll] = useState(false);
  const [deploymentOpen, setDeploymentOpen] = useState(false);
  // The danger zone starts folded even when its section is selected.
  const [dangerOpen, setDangerOpen] = useState(false);

  const draftJson = draft ? JSON.stringify(draft) : "";
  const dirty = draftJson !== "" && draftJson !== baseline;
  // The panel's own update entry opens a dialog instead of navigating, so the
  // in-app predicate is not needed here any more; the browser-level guard is
  // what still protects an uncommitted draft, and the settings page guards its
  // tab switches.
  useUnsavedChanges(dirty);

  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);

  const save = useAdminMutation({
    mutationFn: (body: RuntimeEditableSettings) => s.updateRuntimeSettings(body),
    invalidateKeys: [["runtime-settings"]],
    // The status line beside the source pill is where a failed instant save is
    // reported; a toast on top of it would be a second voice saying the same
    // thing, and there is no dialog to dismiss this time.
    toastOnError: false,
    onSuccess: (snapshot, variables) => {
      const incoming = JSON.stringify(snapshot.editable);
      baselineRef.current = incoming;
      setBaseline(incoming);
      // Adopt the server's normalised values only when nothing was typed while
      // the request was in flight. Otherwise a resolved default (an outbound
      // limit sent as 0 comes back as the deployment value) would leave the
      // field looking permanently unsaved.
      if (JSON.stringify(variables) === JSON.stringify(draftRef.current)) {
        draftRef.current = snapshot.editable;
        setDraft(snapshot.editable);
      }
      setSaveState("saved");
    },
    onError: () => setSaveState("error"),
    onSettled: () => {
      savingRef.current = false;
      if (queuedRef.current) {
        queuedRef.current = false;
        flush();
      }
    },
  });

  const reset = useAdminMutation({
    mutationFn: () => s.resetRuntimeSettings(),
    invalidateKeys: [["runtime-settings"]],
    onSuccess: (snapshot) => {
      const incoming = JSON.stringify(snapshot.editable);
      baselineRef.current = incoming;
      setBaseline(incoming);
      draftRef.current = snapshot.editable;
      setDraft(snapshot.editable);
      setRemoteChanged(false);
      setSaveState("idle");
    },
  });

  /**
   * Write the current draft. Called on every blur and on every checkbox/select
   * change — never from a keystroke, so a half-typed number ("300" on its way
   * to "3") is never what lands upstream.
   */
  function flush() {
    const body = draftRef.current;
    if (!body) return;
    // Blur fires whenever focus moves between two fields, including ones the
    // operator never touched: only a real difference is worth a PUT.
    if (JSON.stringify(body) === baselineRef.current) return;
    if (savingRef.current) {
      queuedRef.current = true;
      return;
    }
    savingRef.current = true;
    setSaveState("saving");
    save.mutate(body);
  }

  useEffect(() => {
    const editable = query.data?.editable;
    if (!editable) return;
    const incoming = JSON.stringify(editable);
    const local = draftRef.current ? JSON.stringify(draftRef.current) : null;
    // A local draft that differs from what the server last confirmed means the
    // operator is mid-edit: report that the server moved instead of overwriting
    // what they typed.
    if (local !== null && local !== baselineRef.current) {
      if (incoming !== baselineRef.current) setRemoteChanged(true);
      return;
    }
    baselineRef.current = incoming;
    setBaseline(incoming);
    draftRef.current = { ...editable };
    setDraft({ ...editable });
    setRemoteChanged(false);
  }, [query.data]);

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
    const incoming = JSON.stringify(editable);
    baselineRef.current = incoming;
    setBaseline(incoming);
    draftRef.current = { ...editable };
    setDraft({ ...editable });
    setRemoteChanged(false);
    setSaveState("idle");
    save.reset();
    reset.reset();
  };
  const keepLocalDraft = () => {
    const editable = query.data?.editable;
    if (editable) {
      const incoming = JSON.stringify(editable);
      baselineRef.current = incoming;
      setBaseline(incoming);
    }
    setRemoteChanged(false);
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
  // Tolerated as absent: the panel must still render (and simply call
  // everything "default") if the bootstrap layer is missing from the payload.
  const bootstrap = data.env_bootstrap;
  // Only the global reset blocks input: an instant save must not freeze the
  // form the operator is still working in.
  const busy = reset.isPending;
  const active: RuntimeSection = SECTION_BY_KEY.get(activeSection) ?? RELAY_SECTION;

  const isChanged = (key: EditableKey) => {
    const value = draft[key];
    if (ZERO_MEANS_DEFAULT.has(key) && !value) return false;
    return !sameSetting(value, bootstrap?.[key]);
  };
  const sectionChanged = (section: RuntimeSection) => section.fields.some(isChanged);
  const activeChanged = sectionChanged(active);

  const patch = <K extends EditableKey>(key: K, value: RuntimeEditableSettings[K]) => {
    const current = draftRef.current;
    if (!current) return;
    const next = { ...current, [key]: value };
    draftRef.current = next;
    setDraft(next);
    // Keep a failure visible until the next successful write retires it.
    setSaveState((prev) => (prev === "error" ? prev : "idle"));
  };

  const restoreSection = (section: RuntimeSection) => {
    const current = draftRef.current;
    if (!current || !bootstrap) return;
    const next = { ...current } as unknown as Record<string, unknown>;
    const defaults = bootstrap as unknown as Record<string, unknown>;
    for (const key of section.fields) next[key] = defaults[key];
    const typed = next as unknown as RuntimeEditableSettings;
    draftRef.current = typed;
    setDraft(typed);
    flush();
  };

  const handleSectionBlur = (event: FocusEvent<HTMLDivElement>) => {
    const target = event.target;
    // Checkboxes and selects commit on change — they have no reliable blur
    // moment — so the change handler below owns them.
    if (
      target instanceof HTMLInputElement &&
      (target.type === "checkbox" || target.type === "radio")
    )
      return;
    if (target instanceof HTMLSelectElement) return;
    // A field the validator has already flagged must not be written: with
    // instant save there is no later chance to fix it before the PUT.
    if (target instanceof HTMLElement && target.getAttribute("aria-invalid") === "true") return;
    flush();
  };

  const handleSectionChange = (event: ChangeEvent<HTMLDivElement>) => {
    const target = event.target;
    const isToggle = target instanceof HTMLInputElement && target.type === "checkbox";
    if (isToggle || target instanceof HTMLSelectElement) flush();
  };

  const updateInfo = updateCheckQuery.data;
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

  // The deployment layer: what the process actually read at startup. Without
  // it, "the gateway timed out at 60s" is unanswerable from the console — the
  // ceiling is in a deployment file, and a variable compose never passed looks
  // exactly like a deliberate default.
  const allParams = data.deployment_parameters ?? [];
  const needle = paramFilter.trim().toLowerCase();
  const matchedParams = needle
    ? allParams.filter((param) => param.key.toLowerCase().includes(needle))
    : allParams;
  const envParamCount = allParams.filter((param) => param.from_env).length;
  // Only the variables the container really set are shown until asked
  // otherwise: a value that fell back to a code default is the same on every
  // deployment, so it is the env-provided rows that answer "why is this 60s".
  const showEveryParam = paramShowAll || needle !== "";
  const visibleParams = showEveryParam
    ? matchedParams
    : matchedParams.filter((param) => param.from_env);
  const hiddenParamCount = matchedParams.length - visibleParams.length;

  const status =
    saveState === "error" ? (
      <>
        <StatusBadge value="error" />
        <span>{t("ops.runtime.saveFailed")}</span>
      </>
    ) : save.isPending ? (
      <>
        <LoaderCircle size={13} className="spin" aria-hidden="true" />
        <span>{t("ops.runtime.saving")}</span>
      </>
    ) : dirty ? (
      <span className="runtime-settings-unsaved">{t("ops.runtime.unsaved")}</span>
    ) : saveState === "saved" ? (
      <>
        <StatusBadge value="success" />
        <span>{t("ops.runtime.saved")}</span>
      </>
    ) : null;

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
          <span className="runtime-save-status" role="status">
            {status}
          </span>
          {saveState === "error" ? (
            <Button variant="quiet" onClick={flush}>
              {t("common.retry")}
            </Button>
          ) : null}
          <Button
            variant="quiet"
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

      <div className="runtime-settings-layout">
        <nav className="runtime-section-nav" aria-label={t("ops.runtime.sectionNav")}>
          {RUNTIME_GROUPS.map((group) => (
            <div key={group.key} className="runtime-nav-group">
              <span className="runtime-nav-group-label">{t(group.label)}</span>
              {group.sections.map((section) => (
                <button
                  key={section.key}
                  type="button"
                  className={`runtime-nav-item${section.key === active.key ? " is-active" : ""}`}
                  aria-current={section.key === active.key ? "true" : undefined}
                  onClick={() => setActiveSection(section.key)}
                >
                  <span>{t(section.label)}</span>
                  {sectionChanged(section) ? (
                    <span className="runtime-nav-dot" aria-hidden="true" />
                  ) : null}
                </button>
              ))}
            </div>
          ))}
        </nav>

        {/* One blur/change listener for the whole section body: React bubbles
            both through the container, so no field needs its own save call. */}
        <div
          className="runtime-section-body"
          onBlur={handleSectionBlur}
          onChange={handleSectionChange}
        >
          {active.key === "relay" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
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
                  <SettingState
                    state={isChanged("cross_channel_failover_enabled") ? "changed" : "default"}
                  />
                </span>
              </label>

              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.retryTimes")}
                  hint={t("ops.runtime.retryTimesHint")}
                  changed={isChanged("retry_times")}
                />
                <ValidatedNumberInput
                  min={0}
                  max={100}
                  disabled={busy || !draft.cross_channel_failover_enabled}
                  value={draft.retry_times}
                  onChange={(e) =>
                    patch("retry_times", numberOr(e.target.value, draft.retry_times))
                  }
                />
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.channelRetryTimes")}
                  hint={t("ops.runtime.channelRetryTimesHint")}
                  changed={isChanged("channel_retry_times")}
                />
                <ValidatedNumberInput
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
            </SectionCard>
          ) : null}

          {active.key === "routing" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
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
                  <SettingState
                    state={isChanged("routing_latency_aware") ? "changed" : "default"}
                  />
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
                  <SettingState state={isChanged("routing_error_aware") ? "changed" : "default"} />
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
                  <SettingState
                    state={isChanged("routing_concurrency_enabled") ? "changed" : "default"}
                  />
                </span>
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.concurrencyLimit")}
                  hint={t("ops.runtime.concurrencyLimitHint")}
                  changed={isChanged("routing_concurrency_limit")}
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
            </SectionCard>
          ) : null}

          {active.key === "stableFirst" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
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
                  <SettingState state={isChanged("stable_first_enabled") ? "changed" : "default"} />
                </span>
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.stableFirstDenominator")}
                  hint={t("ops.runtime.stableFirstDenominatorHint")}
                  changed={isChanged("stable_first_denominator")}
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
                  changed={isChanged("stable_first_promote_requests")}
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
            </SectionCard>
          ) : null}

          {active.key === "sticky" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
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
                  <SettingState state={isChanged("sticky_enabled") ? "changed" : "default"} />
                </span>
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.stickyTTL")}
                  hint={t("ops.runtime.stickyTTLHint")}
                  changed={isChanged("sticky_ttl_minutes")}
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
            </SectionCard>
          ) : null}

          {active.key === "limits" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
              <div className="field">
                <SettingLabel
                  label={t("ops.runtime.relayRate")}
                  hint={t("ops.runtime.relayRateHint")}
                  changed={isChanged("relay_rate_per_minute") || isChanged("relay_rate_burst")}
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
                  changed={isChanged("admin_rate_per_minute") || isChanged("admin_rate_burst")}
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
            </SectionCard>
          ) : null}

          {active.key === "errorRules" ? <ErrorRulesPanel /> : null}
          {active.key === "promptGuard" ? <PromptGuardPanel /> : null}

          {active.key === "cooldown" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
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
                  <SettingState
                    state={isChanged("fault_protection_enabled") ? "changed" : "default"}
                  />
                </span>
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.cooldown")}
                  hint={t("ops.runtime.cooldownHint")}
                  changed={isChanged("cooldown_seconds")}
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
                  changed={isChanged("channel_auto_disable_threshold")}
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
                  changed={isChanged("recovery_probe_interval_seconds")}
                />
                <ValidatedNumberInput
                  min={10}
                  max={86400}
                  disabled={
                    busy || !draft.fault_protection_enabled || !draft.recovery_probe_enabled
                  }
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
                  <SettingState
                    state={isChanged("recovery_probe_enabled") ? "changed" : "default"}
                  />
                </span>
              </label>
            </SectionCard>
          ) : null}

          {active.key === "health" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
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
                  <SettingState state={isChanged("health_sweep_enabled") ? "changed" : "default"} />
                </span>
              </label>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.healthSweepInterval")}
                  hint={t("ops.runtime.healthSweepIntervalHint")}
                  changed={isChanged("health_sweep_interval_seconds")}
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
                  changed={isChanged("health_sweep_jitter_seconds")}
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
                  changed={isChanged("health_sweep_degraded_ms")}
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
                  changed={isChanged("health_sweep_concurrency")}
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
                  changed={isChanged("health_sweep_timeout_seconds")}
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
            </SectionCard>
          ) : null}

          {active.key === "sync" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.discoveryCron")}
                  hint={t("ops.runtime.discoveryCronHint")}
                  changed={isChanged("discovery_cron")}
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
                  changed={isChanged("default_model_sync_mode")}
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
            </SectionCard>
          ) : null}

          {active.key === "probe" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
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
                  changed={isChanged("probe_cron")}
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
                  changed={isChanged("probe_prompt")}
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
                  changed={isChanged("probe_max_tokens")}
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
                  changed={isChanged("probe_concurrency")}
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
                  changed={isChanged("probe_auto_disable")}
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
            </SectionCard>
          ) : null}

          {active.key === "siteProbe" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
              <p className="muted panel-lede">{t("ops.runtime.siteProbeIntro")}</p>
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.siteProbeInterval")}
                  hint={t("ops.runtime.siteProbeIntervalHint")}
                  changed={isChanged("site_probe_interval_seconds")}
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
                  changed={isChanged("site_probe_jitter_seconds")}
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
            </SectionCard>
          ) : null}

          {active.key === "checkin" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
              actions={
                <Link className="button button-quiet" to="/checkins">
                  {t("ops.runtime.openCheckin")}
                </Link>
              }
            >
              <p className="muted panel-lede">{t("ops.runtime.checkinScope")}</p>
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
                  <SettingState state={isChanged("checkin_enabled") ? "changed" : "default"} />
                </span>
              </label>
              <label className="field is-spaced">
                <SettingLabel
                  label={t("ops.runtime.checkinCron")}
                  hint={t("ops.runtime.checkinCronHint")}
                  changed={isChanged("checkin_cron")}
                />
                <CheckinTimePicker
                  value={draft.checkin_cron}
                  disabled={busy}
                  onChange={(cron) => patch("checkin_cron", cron)}
                />
              </label>
            </SectionCard>
          ) : null}

          {active.key === "audit" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.auditDays")}
                  hint={t("ops.runtime.auditDaysHint")}
                  changed={isChanged("audit_retention_days")}
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
                  changed={isChanged("audit_retention_rows")}
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
            </SectionCard>
          ) : null}

          {active.key === "alerts" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
              <label className="field">
                <SettingLabel
                  label={t("ops.runtime.webhookURL")}
                  hint={t("ops.runtime.webhookURLHint")}
                  changed={isChanged("webhook_url")}
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
                  changed={isChanged("webhook_throttle_seconds")}
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
                  changed={isChanged("alert_config_json")}
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
                  changed={isChanged("alert_sweep_interval_seconds")}
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
                  changed={isChanged("alert_daily_summary_interval_seconds")}
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
            </SectionCard>
          ) : null}

          {active.key === "alertRules" ? <AlertRulesPanel /> : null}

          {active.key === "maintenance" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
              <label className="field">
                <SettingLabel
                  label={t("ops.maintenance.cron")}
                  hint={t("ops.maintenance.cronHint")}
                  changed={isChanged("db_gc_cron")}
                />
                <input
                  type="text"
                  placeholder="0 4 * * *"
                  disabled={busy}
                  value={draft.db_gc_cron ?? ""}
                  onChange={(e) => patch("db_gc_cron", e.target.value)}
                />
              </label>
            </SectionCard>
          ) : null}

          {active.key === "server" ? (
            <SectionCard
              section={active}
              changed={activeChanged}
              busy={busy}
              onRestore={() => restoreSection(active)}
            >
              <label className="field is-stacked">
                <SettingLabel
                  label={t("ops.runtime.proxyURL")}
                  hint={t("ops.runtime.proxyURLHint")}
                  changed={isChanged("proxy_url")}
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
                    changed={isChanged("outbound_connect_timeout_seconds")}
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
                    changed={isChanged("outbound_header_timeout_seconds")}
                  />
                  <input
                    type="number"
                    min={0}
                    max={3600}
                    disabled={busy}
                    value={draft.outbound_header_timeout_seconds ?? 0}
                    onChange={(e) =>
                      patch("outbound_header_timeout_seconds", Number(e.target.value))
                    }
                  />
                </label>
                <label className="field">
                  <SettingLabel
                    label={t("ops.runtime.outboundImageHeaderTimeout")}
                    hint={t("ops.runtime.outboundImageHeaderTimeoutHint")}
                    changed={isChanged("outbound_image_header_timeout_seconds")}
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
                    changed={isChanged("outbound_tls_timeout_seconds")}
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
                    changed={isChanged("outbound_max_idle_conns")}
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
                    changed={isChanged("outbound_max_idle_conns_per_host")}
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
                  <SettingState state={isChanged("update_check_enabled") ? "changed" : "default"} />
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

              {/* The environment layer is a fact about the deployment, not a
                  runtime parameter, so it lives inside the section that owns
                  the process's own settings and starts folded. */}
              <div className={`runtime-subblock${deploymentOpen ? " is-open" : ""}`}>
                <button
                  type="button"
                  className="runtime-subblock-toggle"
                  aria-expanded={deploymentOpen}
                  onClick={() => setDeploymentOpen((open) => !open)}
                >
                  <ChevronRight size={14} className="runtime-subblock-chevron" aria-hidden="true" />
                  <span className="runtime-subblock-title">
                    {t("ops.runtime.section.deployment")}
                  </span>
                  <span className="runtime-subblock-count">
                    {t("ops.runtime.deploymentEnvSummary", { count: envParamCount })}
                  </span>
                </button>
                {deploymentOpen ? (
                  <div className="runtime-subblock-body">
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
                      {visibleParams.map((param) => (
                        <div className="runtime-param-row" key={param.key}>
                          <code className="runtime-param-key">{param.key}</code>
                          <span className="runtime-param-value mono">{param.value || "—"}</span>
                          <span
                            className={`runtime-param-source${param.from_env ? " is-env" : ""}`}
                          >
                            {param.from_env
                              ? t("ops.runtime.paramFromEnv")
                              : t("ops.runtime.paramDefault")}
                          </span>
                        </div>
                      ))}
                      {visibleParams.length === 0 ? (
                        <p className="muted runtime-param-empty">{t("ops.runtime.paramEmpty")}</p>
                      ) : null}
                    </div>
                    {hiddenParamCount > 0 ? (
                      <Button variant="quiet" onClick={() => setParamShowAll((show) => !show)}>
                        {showEveryParam
                          ? t("ops.runtime.deploymentEnvOnly")
                          : t("ops.runtime.deploymentShowAll", { count: hiddenParamCount })}
                      </Button>
                    ) : null}
                  </div>
                ) : null}
              </div>
            </SectionCard>
          ) : null}

          {active.key === "totp" ? <TOTPPanel /> : null}
          {active.key === "dbMaintenance" ? <MaintenancePanel /> : null}

          {/* The multi-user area owns its own switch now (Users → Overview),
              because it turns a product capability on — not a tuning knob. What
              stays here is the way in: a personal gateway hides the navigation
              entry, so without this the module would be unreachable from
              Settings. */}
          {active.key === "users" ? (
            <Panel className="runtime-card runtime-card-users" id="runtime-users">
              <div className="panel-header">
                <strong>{t("ops.runtime.section.users")}</strong>
              </div>
              <p className="muted panel-lede">{t("ops.runtime.multiUserHint")}</p>
              <Link className="button" to="/users">
                {t("ops.runtime.openUsers")}
              </Link>
            </Panel>
          ) : null}

          {/* An irreversible wipe must not be a peer card in a settings grid:
              the section opens on a folded danger block, so revealing the
              action is a second, deliberate click. */}
          {active.key === "danger" ? (
            <CollapsibleGroup
              id="runtime-group-danger"
              title={t("ops.runtime.group.danger")}
              description={t("ops.runtime.group.dangerDesc")}
              open={dangerOpen}
              onToggle={() => setDangerOpen((open) => !open)}
              danger
            >
              <FactoryResetPanel />
            </CollapsibleGroup>
          ) : null}
        </div>
      </div>

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

      {/* The update entry used to deep-link into Settings → update channel. The
          channel is not a setting (it is the deployment's image tag), so the
          entry now opens the same dialog the top-bar pill does. */}
      {updateOpen && updateCheckQuery.data ? (
        <UpdateDialog update={updateCheckQuery.data} onClose={() => setUpdateOpen(false)} />
      ) : null}
    </div>
  );
}
