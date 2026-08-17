/**
 * WebDAV auto-sync schedule presets.
 * Thin alias over the shared schedule-preset module so Exchange keeps its
 * existing imports while check-in reuses the same preset machinery.
 */
export {
	scheduleFromSettings,
	settingsFromSchedule,
} from "./schedulePresets";
export type {
	SchedulePresetId as WebDAVSchedulePresetId,
} from "./schedulePresets";
