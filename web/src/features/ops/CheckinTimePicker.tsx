import { useI18n } from "../../i18n"

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

export /**
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
