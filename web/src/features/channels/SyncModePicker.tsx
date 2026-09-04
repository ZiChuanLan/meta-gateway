import { HelpCircle } from "lucide-react";
import { useI18n } from "../../i18n";

/** Per-channel model sync mode. Mirrors domain.ModelSyncMode on the backend. */
export type ModelSyncMode = "auto" | "manual";

/**
 * Compact control for the per-channel model sync mode.
 *
 * One row: a segmented toggle, a help icon whose hover/focus popover carries
 * the per-mode trade-offs, and a muted adopted/total readout. The drawer
 * around the picker already explains the model list, so the control itself
 * stays quiet.
 */
export function SyncModePicker({
  value,
  onChange,
  disabled,
  modelCount,
  adoptedCount,
  defaultMode,
}: {
  value: ModelSyncMode;
  onChange: (next: ModelSyncMode) => void;
  disabled?: boolean;
  /** Size of the discovery snapshot (null = not fetched yet; hides counts). */
  modelCount?: number | null;
  /** Snapshot models already wired into routing (null = not fetched yet). */
  adoptedCount?: number | null;
  /** System default, noted under the toggle so inherit state stays visible. */
  defaultMode?: ModelSyncMode | null;
}) {
  const { t } = useI18n();
  const options: Array<{
    mode: ModelSyncMode;
    label: string;
    pro: string;
    con: string;
  }> = [
    {
      mode: "auto",
      label: t("channels.syncModeAuto"),
      pro: t("channels.syncModeAutoPro"),
      con: t("channels.syncModeAutoCon"),
    },
    {
      mode: "manual",
      label: t("channels.syncModeManual"),
      pro: t("channels.syncModeManualPro"),
      con: t("channels.syncModeManualCon"),
    },
  ];
  const showCounts =
    modelCount != null && modelCount > 0 && adoptedCount != null;
  return (
    <div className="sync-mode">
      <div className="sync-mode-row">
        <div
          className="sync-mode-toggle"
          role="radiogroup"
          aria-label={t("channels.syncMode")}
        >
          {options.map((option) => {
            const active = value === option.mode;
            return (
              <button
                key={option.mode}
                type="button"
                disabled={disabled}
                className={active ? "is-active" : ""}
                aria-pressed={active}
                onClick={() => onChange(option.mode)}
              >
                {option.label}
              </button>
            );
          })}
        </div>
        <span className="sync-mode-info" tabIndex={0}>
          <HelpCircle size={13} aria-hidden />
          <span className="sync-mode-pop" role="tooltip">
            <strong>{t("channels.syncModeGuide")}</strong>
            {options.map((option) => (
              <span key={option.mode} className="sync-mode-pop-row">
                <b>{option.label}</b>
                <em className="is-pro">{option.pro}</em>
                <em className="is-con">{option.con}</em>
              </span>
            ))}
            <span className="sync-mode-pop-row is-switch">
              {t("channels.syncModeGuideSwitch")}
            </span>
          </span>
        </span>
        {showCounts ? (
          <span className="sync-mode-counts">
            {t("channels.syncModeCounts", {
              total: modelCount,
              adopted: adoptedCount,
            })}
          </span>
        ) : null}
      </div>
      {defaultMode ? (
        <p className="sync-mode-default">
          {t("channels.syncModeInheritDefault", {
            mode:
              defaultMode === "auto"
                ? t("channels.syncModeAuto")
                : t("channels.syncModeManual"),
          })}
        </p>
      ) : null}
    </div>
  );
}
