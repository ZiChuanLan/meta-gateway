/**
 * WebDAV auto-sync schedule presets.
 * Thin alias over the shared schedule-preset module so Exchange keeps its
 * existing imports while check-in reuses the same preset machinery.
 */
export {
	SCHEDULE_PRESETS as WEBDAV_SCHEDULE_PRESETS,
	scheduleFromSettings,
	settingsFromSchedule,
} from "./schedulePresets";
export type {
	SchedulePreset as WebDAVSchedulePreset,
	SchedulePresetId as WebDAVSchedulePresetId,
} from "./schedulePresets";
