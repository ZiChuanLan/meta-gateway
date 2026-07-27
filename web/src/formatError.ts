import { ApiError } from "./api/client";

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/**
 * Maps API / mutation errors to a stable operator-facing string.
 * Shared by ErrorState and bottom-right toasts.
 */
export function formatErrorMessage(error: unknown, t: Translate): string {
	const raw = error instanceof ApiError ? error.message : typeof error === "string" ? error : "common.error";
	const mappedKey = `error.${raw}`;
	const mapped = t(mappedKey);
	if (raw === "api.unreachable" || raw === "Unable to reach Meta Gateway") {
		return t("api.unreachable");
	}
	if (raw === "common.error") {
		return t("common.error");
	}
	const lower = raw.toLowerCase();
	if (
		lower.includes("unlock password") ||
		lower.includes("backup password required")
	) {
		return t("error.backup_unlock_required");
	}
	if (mapped !== mappedKey) {
		return mapped;
	}
	return raw;
}
