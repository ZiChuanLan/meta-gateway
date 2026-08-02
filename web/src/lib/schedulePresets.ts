/**
 * Human-friendly cron schedule presets.
 * Backend still stores five-field cron; operators never need to type it.
 * Shared by WebDAV auto-sync and scheduled check-in.
 */

export type SchedulePresetId =
	| "off"
	| "hourly"
	| "every3h"
	| "every6h"
	| "every12h"
	| "daily"
	| "custom";

export type SchedulePreset = {
	id: SchedulePresetId;
	cron: string;
};

export const SCHEDULE_PRESETS: SchedulePreset[] = [
	{ id: "off", cron: "" },
	{ id: "hourly", cron: "0 * * * *" },
	{ id: "every3h", cron: "0 */3 * * *" },
	{ id: "every6h", cron: "0 */6 * * *" },
	{ id: "every12h", cron: "0 */12 * * *" },
	{ id: "daily", cron: "0 8 * * *" },
	{ id: "custom", cron: "" },
];

/** Default cron used when none configured and scheduling is on. */
export const DEFAULT_SCHEDULE_CRON = "0 8 * * *";

export function scheduleFromSettings(input: {
	enabled: boolean;
	cron: string;
}): { preset: SchedulePresetId; cron: string } {
	const cron = (input.cron || "").trim() || DEFAULT_SCHEDULE_CRON;
	if (!input.enabled) {
		return { preset: "off", cron };
	}
	const known = SCHEDULE_PRESETS.find(
		(item) => item.id !== "off" && item.id !== "custom" && item.cron === cron,
	);
	if (known) {
		return { preset: known.id, cron };
	}
	return { preset: "custom", cron };
}

export function settingsFromSchedule(input: {
	preset: SchedulePresetId;
	cron: string;
}): { enabled: boolean; cron: string } {
	if (input.preset === "off") {
		return {
			enabled: false,
			cron: (input.cron || "").trim() || DEFAULT_SCHEDULE_CRON,
		};
	}
	if (input.preset === "custom") {
		return {
			enabled: true,
			cron: (input.cron || "").trim() || DEFAULT_SCHEDULE_CRON,
		};
	}
	const known = SCHEDULE_PRESETS.find((item) => item.id === input.preset);
	return {
		enabled: true,
		cron: known?.cron || DEFAULT_SCHEDULE_CRON,
	};
}
