import { useQuery } from "@tanstack/react-query";
import {
  Children,
  useEffect,
  useId,
  useState,
  type InputHTMLAttributes,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import { api } from "../../api/client";
import type { RuntimeEditableSettings } from "../../api/types";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";
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

function numberOr(value: string, fallback: number) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

/** Panel-level anchor order for the runtime settings section nav. */
const RUNTIME_SECTION_ANCHORS = [
  ["relay", "ops.runtime.section.relay"],
  ["cooldown", "ops.runtime.section.cooldown"],
  ["health", "ops.runtime.section.healthSweep"],
  ["sticky", "ops.runtime.section.sticky"],
  ["checkin", "ops.runtime.section.checkin"],
  ["limits", "ops.runtime.section.limits"],
  ["audit", "ops.runtime.section.audit"],
  ["routing", "ops.runtime.section.routing"],
  ["stableFirst", "ops.runtime.section.stableFirst"],
  ["maintenance", "ops.runtime.section.maintenance"],
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

function numberValidationError(
  value: number,
  min: number | undefined,
  max: number | undefined,
  customError: string | undefined,
  t: (key: string, vars?: Record<string, string | number>) => string,
) {
  if (!Number.isFinite(value)) return t("ops.runtime.validation.number");
  if (!Number.isInteger(value)) return t("ops.runtime.validation.integer");
  if (min !== undefined && value < min) {
    return max !== undefined
      ? t("ops.runtime.validation.between", { min, max })
      : t("ops.runtime.validation.min", { min });
  }
  if (max !== undefined && value > max) {
    return min !== undefined
      ? t("ops.runtime.validation.between", { min, max })
      : t("ops.runtime.validation.max", { max });
  }
  return customError;
}

type ValidatedNumberInputProps = Omit<
  InputHTMLAttributes<HTMLInputElement>,
  "max" | "min" | "value"
> & {
  min?: number;
  max?: number;
  value: number;
  customError?: string;
};

function ValidatedNumberInput({
  min,
  max,
  customError,
  disabled,
  value,
  ...props
}: ValidatedNumberInputProps) {
  const { t } = useI18n();
  const errorId = useId();
  const numericValue = Number(value);
  const error =
    disabled || !Number.isFinite(numericValue)
      ? undefined
      : numberValidationError(numericValue, min, max, customError, t);

  return (
    <span className={`setting-input-wrap${error ? " is-invalid" : ""}`}>
      <input
        {...props}
        type="number"
        min={min}
        max={max}
        step={1}
        disabled={disabled}
        value={value}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? errorId : undefined}
      />
      {error ? (
        <span id={errorId} className="setting-validation" role="alert">
          {error}
        </span>
      ) : null}
    </span>
  );
}

/** Render the row-major cards as two independent vertical columns. */
function RuntimeSettingsColumns({ children }: { children: ReactNode }) {
  const cards = Children.toArray(children);
  return (
    <div className="runtime-settings-grid">
      <div className="runtime-settings-column">
        {cards.filter((_, index) => index % 2 === 0)}
      </div>
      <div className="runtime-settings-column">
        {cards.filter((_, index) => index % 2 === 1)}
      </div>
    </div>
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

      {save.error || reset.error ? (
        <ErrorState error={save.error ?? reset.error} />
      ) : null}
      {save.isSuccess ? (
        <div className="result-strip">
          <StatusBadge value="success" />
          <span>{t("ops.runtime.saved")}</span>
        </div>
      ) : null}

      <nav
        className="runtime-section-nav"
        aria-label={t("ops.runtime.sectionNav")}
      >
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
      <RuntimeSettingsColumns>
        <Panel className="runtime-card runtime-card-relay" id="runtime-relay">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.relay")}</strong>
          </div>
          <label className="check" style={{ marginBottom: 10 }}>
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
          <label className="check" style={{ marginBottom: 10 }}>
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.key_pool_rotation}
              onChange={(e) => patch("key_pool_rotation", e.target.checked)}
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
        </Panel>

        <Panel
          className="runtime-card runtime-card-cooldown"
          id="runtime-cooldown"
        >
          <div className="panel-header">
            <strong>{t("ops.runtime.section.cooldown")}</strong>
          </div>
          <label className="check">
            <input
              type="checkbox"
              disabled={busy}
              checked={draft.fault_protection_enabled}
              onChange={(e) =>
                patch("fault_protection_enabled", e.target.checked)
              }
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
                patch(
                  "cooldown_seconds",
                  numberOr(e.target.value, draft.cooldown_seconds),
                )
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
                numberOr(
                  e.target.value,
                  draft.channel_auto_disable_threshold,
                ),
              )
            } />
          </label>
          <label className="field">
            <SettingLabel
              label={t("ops.runtime.recoveryInterval")}
              hint={t("ops.runtime.recoveryIntervalHint")}
            />
            <ValidatedNumberInput
              min={10}
              max={86400}
              disabled={
                busy ||
                !draft.fault_protection_enabled ||
                !draft.recovery_probe_enabled
              }
              value={draft.recovery_probe_interval_seconds}
            onChange={(e) =>
              patch(
                "recovery_probe_interval_seconds",
                numberOr(
                  e.target.value,
                  draft.recovery_probe_interval_seconds,
                ),
              )
            } />
          </label>
          <label className="check">
            <input
              type="checkbox"
              disabled={busy || !draft.fault_protection_enabled}
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
                draft.health_sweep_jitter_seconds >
                  draft.health_sweep_interval_seconds
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

        <Panel className="runtime-card runtime-card-sticky" id="runtime-sticky">
          <div className="panel-header">
            <strong>{t("ops.runtime.section.sticky")}</strong>
          </div>
          <label className="check" style={{ marginBottom: 10 }}>
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
                patch(
                  "sticky_ttl_minutes",
                  numberOr(e.target.value, draft.sticky_ttl_minutes),
                )
              }
            />
          </label>
        </Panel>

        <Panel
          className="runtime-card runtime-card-checkin"
          id="runtime-checkin"
        >
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
        <Panel
          className="runtime-card runtime-card-routing"
          id="runtime-routing"
        >
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

        <Panel
          className="runtime-card runtime-card-stable-first"
          id="runtime-stable-first"
        >
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
                  numberOr(
                    e.target.value,
                    draft.alert_daily_summary_interval_seconds,
                  ),
                )
              }
            />
          </label>
        </Panel>

        <Panel
          className="runtime-card runtime-card-maintenance"
          id="runtime-maintenance"
        >
          <div className="panel-header">
            <strong>{t("ops.runtime.section.maintenance")}</strong>
          </div>
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
      </RuntimeSettingsColumns>

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
