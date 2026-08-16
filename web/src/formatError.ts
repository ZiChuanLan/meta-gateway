import { ApiError } from "./api/client";
import { categorizeError, type ErrorClass } from "./errorCatalog";

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/** Class-name suffix shown next to the title, e.g. "Connection failed (network)". */
const CLASS_KEY: Record<ErrorClass, string> = {
	network: "err.network.title",
	auth: "err.auth.title",
	config: "err.config.title",
	missing_key: "err.missingKey.title",
	missing_user_token: "err.missingUserToken.title",
	rate_limited: "err.rateLimited.title",
	upstream_reject: "err.upstreamReject.title",
	not_found: "err.notFound.title",
	server: "err.server.title",
	unknown: "err.unknown.title",
};

export interface FormattedError {
	/** Short, decisive heading, e.g. "Connection failed". */
	title: string;
	/** Root cause in one sentence. */
	cause: string;
	/** Concrete next step. */
	fix: string;
	/** Raw backend category / status for diagnostics. */
	raw: string;
	/** Stable class id. */
	class: ErrorClass;
	/** HTTP status when known. */
	status?: number;
}

/**
 * Maps API / mutation errors to a stable operator-facing error object.
 * Every backend category collapses into one of a small set of classes so the
 * UI never shows raw snake_case strings again.
 */
export function formatErrorObject(error: unknown, t: Translate): FormattedError {
	const raw =
		error instanceof ApiError
			? error.message
			: typeof error === "string"
				? error
				: "common.error";

	// Keep special-cased messages that carry their own guidance.
	if (raw === "api.unreachable" || raw === "Unable to reach Meta Gateway") {
		return {
			title: t("api.unreachable"),
			cause: t("api.unreachable"),
			fix: "",
			raw,
			class: "network",
		};
	}
	const lower = raw.toLowerCase();
	if (lower.includes("unlock password") || lower.includes("backup password required")) {
		return {
			title: t("error.backup_unlock_required"),
			cause: t("error.backup_unlock_required"),
			fix: "",
			raw,
			class: "config",
		};
	}
	// The upstream created the token but masked the returned secret (sk-xxxx****yyyy).
	// This is a distinct outcome from "no key available at all": the key exists
	// upstream, the gateway just cannot capture the plaintext.
	if (lower === "token_created_but_secret_masked") {
		return {
			title: t("err.tokenMasked.title"),
			cause: t("err.tokenMasked.cause"),
			fix: t("err.tokenMasked.fix"),
			raw,
			class: "missing_key",
		};
	}

	const classified = categorizeError(raw);
	const cls = classified.class;
	const title = t(CLASS_KEY[cls]);
	const cause = t(`err.${clsKey(cls)}.cause`);
	const fix = t(`err.${clsKey(cls)}.fix`);
	return {
		title,
		cause,
		fix,
		raw,
		class: cls,
		status: classified.status,
	};
}

function clsKey(cls: ErrorClass): string {
	switch (cls) {
		case "network":
			return "network";
		case "auth":
			return "auth";
		case "config":
			return "config";
		case "missing_key":
			return "missingKey";
		case "missing_user_token":
			return "missingUserToken";
		case "rate_limited":
			return "rateLimited";
		case "upstream_reject":
			return "upstreamReject";
		case "not_found":
			return "notFound";
		case "server":
			return "server";
		case "unknown":
			return "unknown";
	}
}

/**
 * Maps API / mutation errors to a stable operator-facing string.
 * Shared by ErrorState and bottom-right toasts.
 */
export function formatErrorMessage(error: unknown, t: Translate): string {
	const formatted = formatErrorObject(error, t);
	const statusSuffix = formatted.status
		? t("err.classSuffix", { class: formatted.status })
		: "";
	const parts = [`${formatted.title}${statusSuffix}`];
	if (
		formatted.cause &&
		formatted.cause.trim().toLocaleLowerCase() !==
			formatted.title.trim().toLocaleLowerCase()
	) {
		parts.push(formatted.cause);
	}
	if (formatted.fix) parts.push(formatted.fix);
	return parts.join(" — ");
}
