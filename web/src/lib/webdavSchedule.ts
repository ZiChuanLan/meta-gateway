/**
 * Human-friendly WebDAV auto-sync schedule presets.
 * Backend still stores five-field cron; operators never need to type it.
 */

export type WebDAVSchedulePresetId =
	| "off"
	| "hourly"
	| "every3h"
	| "every6h"
	| "every12h"
	| "daily"
	| "custom";

export type WebDAVSchedulePreset = {
	id: WebDAVSchedulePresetId;
	cron: string;
};

export const WEBDAV_SCHEDULE_PRESETS: WebDAVSchedulePreset[] = [
	{ id: "off", cron: "" },
	{ id: "hourly", cron: "0 * * * *" },
	{ id: "every3h", cron: "0 */3 * * *" },
	{ id: "every6h", cron: "0 */6 * * *" },
	{ id: "every12h", cron: "0 */12 * * *" },
	{ id: "daily", cron: "0 8 * * *" },
	{ id: "custom", cron: "" },
];

export function scheduleFromSettings(input: {
	enabled: boolean;
	cron: string;
}): { preset: WebDAVSchedulePresetId; cron: string } {
	const cron = (input.cron || "").trim() || "0 */6 * * *";
	if (!input.enabled) {
		return { preset: "off", cron };
	}
	const known = WEBDAV_SCHEDULE_PRESETS.find(
		(item) => item.id !== "off" && item.id !== "custom" && item.cron === cron,
	);
	if (known) {
		return { preset: known.id, cron };
	}
	return { preset: "custom", cron };
}

export function settingsFromSchedule(input: {
	preset: WebDAVSchedulePresetId;
	cron: string;
}): { enabled: boolean; cron: string } {
	if (input.preset === "off") {
		return {
			enabled: false,
			cron: (input.cron || "").trim() || "0 */6 * * *",
		};
	}
	if (input.preset === "custom") {
		return {
			enabled: true,
			cron: (input.cron || "").trim() || "0 */6 * * *",
		};
	}
	const known = WEBDAV_SCHEDULE_PRESETS.find((item) => item.id === input.preset);
	return {
		enabled: true,
		cron: known?.cron || "0 */6 * * *",
	};
}
