import { ApiError } from "./lib/apiError";
import { categorizeError, type ErrorClass } from "./errorCatalog";

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/** Class-name suffix shown next to the title, e.g. "Connection failed (network)". */
const CLASS_KEY: Record<ErrorClass, string> = {
  network: "err.network.title",
  auth: "err.auth.title",
  config: "err.config.title",
  upstream_shape: "err.upstreamShape.title",
  missing_key: "err.missingKey.title",
  missing_user_token: "err.missingUserToken.title",
  rate_limited: "err.rateLimited.title",
  upstream_reject: "err.upstreamReject.title",
  pinned_upstream: "err.pinnedUpstream.title",
  not_found: "err.notFound.title",
  server: "err.server.title",
  cancelled: "err.cancelled.title",
  empty_response: "err.emptyResponse.title",
  unknown: "err.unknown.title",
};

/**
 * Reason sentences for a pinned upstream. The backend answers with a code per
 * cause, so the console can name the fault instead of repeating one generic
 * "unavailable" for four different fixes.
 */
const PINNED_CAUSE: Record<string, string> = {
  pinned_upstream_not_member: "err.pinnedUpstream.notMember",
  pinned_upstream_member_disabled: "err.pinnedUpstream.memberDisabled",
  pinned_upstream_channel_disabled: "err.pinnedUpstream.channelDisabled",
  pinned_upstream_no_credential: "err.pinnedUpstream.noCredential",
  pinned_upstream_cooling_down: "err.pinnedUpstream.coolingDown",
  pinned_upstream_invalid_weight: "err.pinnedUpstream.invalidWeight",
  no_eligible_upstream: "err.pinnedUpstream.noEligible",
  preferred_channel_unavailable: "err.pinnedUpstream.generic",
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
    error instanceof ApiError ? error.message : typeof error === "string" ? error : "common.error";

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
  if (
    lower.includes("unlock password") ||
    lower.includes("backup password required") ||
    lower === "backup_unlock_required"
  ) {
    return {
      title: t("error.backup_unlock_required"),
      cause: t("error.backup_unlock_required"),
      fix: "",
      raw,
      class: "config",
    };
  }
  // The unlock password was supplied but did not open the envelope.
  if (lower === "decrypt_failed") {
    return {
      title: t("error.decryptFailed"),
      cause: t("error.decryptFailedCause"),
      fix: t("error.decryptFailedFix"),
      raw,
      class: "config",
    };
  }
  // WebDAV pull succeeded but the downloaded document is not an importable
  // Meta Gateway / AAH backup (wrong file in the cloud folder).
  if (lower.includes("not a supported import document")) {
    return {
      title: t("error.webdavInvalidBackup"),
      cause: t("error.webdavInvalidBackupCause"),
      fix: t("error.webdavInvalidBackupFix"),
      raw,
      class: "config",
    };
  }
  // The same failure through the file importer, which reports categories
  // rather than sentences.
  if (lower === "exchange_document_unsupported") {
    return {
      title: t("error.importFormatUnsupported"),
      cause: t("error.importFormatUnsupportedCause"),
      fix: t("error.importFormatUnsupportedFix"),
      raw,
      class: "config",
    };
  }
  // Recognized backup with nothing importable inside (no credential section,
  // or every row unusable).
  if (lower.includes("holds no importable credentials") || lower === "exchange_document_empty") {
    return {
      title: t("error.importEmpty"),
      cause: t("error.importEmptyCause"),
      fix: t("error.importEmptyFix"),
      raw,
      class: "config",
    };
  }
  if (lower === "exchange_document_invalid") {
    return {
      title: t("error.importInvalid"),
      cause: t("error.importInvalidCause"),
      fix: t("error.importInvalidFix"),
      raw,
      class: "config",
    };
  }
  if (lower === "exchange_document_conflict" || lower === "identity_conflict") {
    return {
      title: t("err.config.title"),
      cause: t("error.identity_conflict"),
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
  // A market install fails outside the gateway: the registry, the GitHub
  // release, or the artifact host. The generic classes sent the operator to a
  // channel's Base URL that is not involved at all, and "server" hid the one
  // fact that matters here — the gateway deliberately ignores the system proxy
  // when a plugin needs one to reach GitHub (2026-09-25). A code that carries an
  // HTTP status (_status_403) keeps the generic path so the status is not lost.
  if (!categorizeError(raw).status) {
    if (lower === "plugin_market_unavailable") {
      return {
        title: t("error.pluginMarketUnavailable"),
        cause: t("error.pluginMarketUnavailableCause"),
        fix: t("error.pluginMarketUnavailableFix"),
        raw,
        class: "network",
      };
    }
    if (
      lower.startsWith("plugin_release_") ||
      lower.startsWith("plugin_artifact_") ||
      lower.startsWith("plugin_download_")
    ) {
      return {
        title: t("error.pluginDownloadFailed"),
        cause: t("error.pluginDownloadFailedCause"),
        fix: t("error.pluginDownloadFailedFix"),
        raw,
        class: "network",
      };
    }
  }

  const classified = categorizeError(raw);
  const cls = classified.class;
  const title = t(CLASS_KEY[cls]);
  // A pinned upstream reports WHICH way it is unusable; the four answers send
  // the operator to four different places, so they get their own sentences
  // instead of one shared "unavailable".
  const pinnedCause = PINNED_CAUSE[raw.toLowerCase()];
  const cause = pinnedCause ? t(pinnedCause) : t(`err.${clsKey(cls)}.cause`);
  const fix = t(`err.${clsKey(cls)}.fix`);
  // When nothing matched, the raw backend phrase is usually the most
  // informative thing we have — surface it instead of a second generic line.
  const fallbackCause = cls === "unknown" && raw !== "common.error" ? raw : cause;
  return {
    title,
    cause: fallbackCause,
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
    case "upstream_shape":
      return "upstreamShape";
    case "missing_key":
      return "missingKey";
    case "missing_user_token":
      return "missingUserToken";
    case "rate_limited":
      return "rateLimited";
    case "upstream_reject":
      return "upstreamReject";
    case "pinned_upstream":
      return "pinnedUpstream";
    case "not_found":
      return "notFound";
    case "server":
      return "server";
    case "cancelled":
      return "cancelled";
    case "empty_response":
      return "emptyResponse";
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
  const statusSuffix = formatted.status ? t("err.classSuffix", { class: formatted.status }) : "";
  const parts = [`${formatted.title}${statusSuffix}`];
  if (
    formatted.cause &&
    formatted.cause.trim().toLocaleLowerCase() !== formatted.title.trim().toLocaleLowerCase()
  ) {
    parts.push(formatted.cause);
  }
  if (formatted.fix) parts.push(formatted.fix);
  return parts.join(" — ");
}
