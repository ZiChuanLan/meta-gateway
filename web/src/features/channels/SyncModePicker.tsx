import { ChevronDown } from "lucide-react";
import { useState } from "react";
import { useI18n } from "../../i18n";

/** Per-channel model sync mode. Mirrors domain.ModelSyncMode on the backend. */
export type ModelSyncMode = "auto" | "manual";

/**
 * Guided picker for the per-channel model sync mode.
 *
 * This is the single most consequential channel setting and the least
 * self-explanatory one, so the control carries its own onboarding: what each
 * mode does, what it costs, what switching will do to models already wired
 * into routing, and the live model/adopted counters that make "manual mode
 * shows 0 adopted" readable instead of alarming.
 */
export function SyncModePicker({
  value,
  onChange,
  disabled,
  modelCount,
  adoptedCount,
  loading,
  defaultMode,
}: {
  value: ModelSyncMode;
  onChange: (next: ModelSyncMode) => void;
  disabled?: boolean;
  /** Size of the discovery snapshot (null = not fetched yet). */
  modelCount?: number | null;
  /** Snapshot models already wired into routing (null = not fetched yet). */
  adoptedCount?: number | null;
  loading?: boolean;
  /** System default, shown as "inherits …" context on the matching option. */
  defaultMode?: ModelSyncMode | null;
}) {
  const { t } = useI18n();
  const [guideOpen, setGuideOpen] = useState(false);
  const total = modelCount ?? 0;
  const adopted = adoptedCount ?? 0;
  const pending = Math.max(0, total - adopted);

  const options: Array<{
    mode: ModelSyncMode;
    label: string;
    hint: string;
    pro: string;
    con: string;
    badge?: string;
  }> = [
    {
      mode: "auto",
      label: t("channels.syncModeAuto"),
      hint: t("channels.syncModeAutoHint"),
      pro: t("channels.syncModeAutoPro"),
      con: t("channels.syncModeAutoCon"),
      badge: t("channels.syncModeAutoBadge"),
    },
    {
      mode: "manual",
      label: t("channels.syncModeManual"),
      hint: t("channels.syncModeManualHint"),
      pro: t("channels.syncModeManualPro"),
      con: t("channels.syncModeManualCon"),
      badge: t("channels.syncModeManualBadge"),
    },
  ];

  const counts =
    loading || modelCount == null
      ? t("channels.syncModeCountsLoading")
      : total === 0
        ? t("channels.syncModeNoModels")
        : t("channels.syncModeCounts", { total, adopted });

  return (
    <div className="sync-mode-field">
      <p className="sync-mode-intro">{t("channels.syncModeIntro")}</p>

      <div
        className="sync-mode-picker"
        role="radiogroup"
        aria-label={t("channels.syncMode")}
      >
        {options.map((option) => {
          const active = value === option.mode;
          return (
            <label
              key={option.mode}
              className={`sync-mode-option${active ? " is-active" : ""}`}
            >
              <input
                type="radio"
                name="sync-mode"
                checked={active}
                disabled={disabled}
                onChange={() => onChange(option.mode)}
              />
              <span>
                <span className="sync-mode-option-head">
                  <strong>{option.label}</strong>
                  {option.badge ? (
                    <em className="sync-mode-badge">{option.badge}</em>
                  ) : null}
                </span>
                <small>{option.hint}</small>
                <span className="sync-mode-pros">
                  <span className="sync-mode-pro">+ {option.pro}</span>
                  <span className="sync-mode-con">− {option.con}</span>
                </span>
                {defaultMode === option.mode ? (
                  <span className="sync-mode-inherit">
                    {t("channels.syncModeInheritDefault", {
                      mode: option.label,
                    })}
                  </span>
                ) : null}
              </span>
            </label>
          );
        })}
      </div>

      {/* Immediate feedback for the choice: what happens on the next sync. */}
      <p
        className={`sync-mode-effect is-${value}`}
        aria-live="polite"
      >
        {value === "auto"
          ? pending > 0
            ? t("channels.syncModeEffectAutoPending", { n: pending })
            : t("channels.syncModeEffectAuto")
          : t("channels.syncModeEffectManual")}
      </p>

      <div className="sync-mode-stats">
        <span className="sync-mode-counts">{counts}</span>
        {!loading && total > 0 && pending > 0 ? (
          <span className="sync-mode-pending">
            {t("channels.syncModeNotAdopted", { n: pending })}
          </span>
        ) : null}
      </div>

      <button
        type="button"
        className={`sync-mode-guide-toggle${guideOpen ? " is-open" : ""}`}
        onClick={() => setGuideOpen((open) => !open)}
        aria-expanded={guideOpen}
      >
        <ChevronDown size={13} />
        {t("channels.syncModeGuide")}
      </button>
      {guideOpen ? (
        <div className="sync-mode-guide">
          <p>
            <strong>{t("channels.syncModeAuto")}</strong>
            {t("channels.syncModeGuideAuto")}
          </p>
          <p>
            <strong>{t("channels.syncModeManual")}</strong>
            {t("channels.syncModeGuideManual")}
          </p>
          <p className="sync-mode-guide-switch">
            {t("channels.syncModeGuideSwitch")}
          </p>
          <p className="sync-mode-guide-recommend">
            {t("channels.syncModeGuideRecommend")}
          </p>
        </div>
      ) : null}
    </div>
  );
}
