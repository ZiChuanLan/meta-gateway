/**
 * WebDAV schedule presets (WebDAV-specific semantics). The AAH import and the
 * native backup upload each carry their own schedule; the literal cron "off"
 * disarms that direction's schedule while its toggle stays as chosen.
 */
import { SCHEDULE_PRESETS, type SchedulePresetId } from "./schedulePresets";

export type WebDAVSchedulePresetId = SchedulePresetId;

const DEFAULT_WEBDAV_CRON = "0 */6 * * *";

export function scheduleFromSettings(cron: string): {
	preset: WebDAVSchedulePresetId;
	cron: string;
} {
	const value = (cron || "").trim();
	if (!value || value === "off") {
		return {
			preset: "off",
			cron: value && value !== "off" ? value : DEFAULT_WEBDAV_CRON,
		};
	}
	const known = SCHEDULE_PRESETS.find(
		(item) => item.id !== "off" && item.id !== "custom" && item.cron === value,
	);
	return { preset: known ? known.id : "custom", cron: value };
}

export function settingsFromSchedule(input: {
	preset: WebDAVSchedulePresetId;
	cron: string;
}): { cron: string } {
	const cron = (input.cron || "").trim() || DEFAULT_WEBDAV_CRON;
	if (input.preset === "off") return { cron: "off" };
	if (input.preset === "custom") return { cron };
	const known = SCHEDULE_PRESETS.find((item) => item.id === input.preset);
	return { cron: known?.cron || cron };
}
