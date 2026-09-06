import { useI18n } from "../../i18n";
import {
  SCHEDULE_PRESETS,
  presetOfCron,
  type SchedulePresetId,
} from "../../lib/schedulePresets";
import { CheckinTimePicker } from "./CheckinTimePicker";

/**
 * Preset-based cron picker for the runtime schedules (model sync, probing).
 * Same interaction as the check-in schedule: a preset dropdown plus a
 * wall-clock time picker for daily runs; "custom" keeps the raw cron editable
 * (non-daily expressions fall back to the raw input inside CheckinTimePicker).
 * An empty value reads as 关闭 — the scheduler treats "" as disabled — and
 * picking a preset materializes the concrete cron. The backend contract does
 * not change: a five-field cron string, empty to disable.
 */
export function CronSchedulePicker({
  value,
  disabled,
  onChange,
}: {
  value: string;
  disabled: boolean;
  onChange: (cron: string) => void;
}) {
  const { t } = useI18n();
  const preset = presetOfCron(value);
  const pickPreset = (id: SchedulePresetId) => {
    if (id === "off") {
      onChange("");
      return;
    }
    if (id === "custom") {
      // Keep the current cron so the raw input has something to edit.
      onChange(value.trim() || "0 3 * * *");
      return;
    }
    const known = SCHEDULE_PRESETS.find((item) => item.id === id);
    onChange(known?.cron ?? value);
  };
  return (
    <span className="cron-schedule-picker">
      <select
        aria-label={t("ops.schedule.presetLabel")}
        disabled={disabled}
        value={preset}
        onChange={(e) => pickPreset(e.target.value as SchedulePresetId)}
      >
        {SCHEDULE_PRESETS.map((item) => (
          <option key={item.id} value={item.id}>
            {item.id === "daily"
              ? t("ops.schedule.preset.dailyAt")
              : t(`ops.schedule.preset.${item.id}`)}
          </option>
        ))}
      </select>
      {preset === "off" ? (
        <span className="muted">{t("ops.schedule.offHint")}</span>
      ) : preset === "custom" || preset === "daily" ? (
        <CheckinTimePicker
          value={value}
          disabled={disabled}
          onChange={onChange}
        />
      ) : null}
    </span>
  );
}
