/**
 * Unified error taxonomy.
 *
 * The backend surfaces dozens of raw category strings (from six service layers)
 * that historically rendered as inconsistent one-liners. This catalog maps
 * every known category to one of a small set of operator-facing error classes,
 * each with a stable title, a root-cause explanation, and a suggested fix.
 */

export type ErrorClass =
	| "network"
	| "auth"
	| "config"
	| "missing_key"
	| "missing_user_token"
	| "rate_limited"
	| "upstream_reject"
	| "not_found"
	| "server"
	| "unknown";

export interface CategorizedError {
	/** Stable, translatable class used to pick title / hint templates. */
	class: ErrorClass;
	/** Raw backend category (or HTTP status) that produced this class. */
	raw: string;
	/** HTTP status when known (0 otherwise). */
	status?: number;
}

/** Map every known backend category string to its error class. */
const CATEGORY_TO_CLASS: Record<string, ErrorClass> = {
	// — Reachability / transport —
	transport: "network",
	upstream_failure: "network",
	connection_failed: "network",
	unreachable: "network",
	timeout: "network",
	dns_failure: "network",
	tls_failure: "network",
	outbound_blocked: "network",

	// — Authentication / authorization —
	unauthorized: "auth",
	upstream_unauthorized: "auth",
	invalid_token: "auth",
	auth_failed: "auth",
	upstream_status_401: "auth",
	upstream_status_403: "auth",
	user_token_not_for_models: "auth",
	session_expired: "auth",

	// — Configuration / invalid input —
	invalid_base_url: "config",
	invalid_url: "config",
	site_unavailable: "config",
	channel_disabled: "config",
	credential_unavailable: "config",
	credential_disabled: "config",
	unsupported_adapter: "config",
	invalid_metadata: "config",
	invalid_payload: "config",
	validation_error: "config",
	invalid_channel_id: "config",
	invalid_id: "config",
	identity_conflict: "config",
	unsupported_format: "config",
	config_incomplete: "config",

	// — Missing / masked API key —
	no_credential: "missing_key",
	missing_api_key: "missing_key",
	keys_masked: "missing_key",
	key_masked: "missing_key",
	skipped_masked: "missing_key",
	empty_token_list: "missing_key",
	token_created_but_secret_masked: "missing_key",
	already_has_api_key: "missing_key",

	// — Missing user (access) token —
	user_credential_unavailable: "missing_user_token",
	no_user_token: "missing_user_token",

	// — Rate limiting —
	rate_limited: "rate_limited",
	upstream_status_408: "rate_limited",
	upstream_status_429: "rate_limited",

	// — Upstream rejected the request (other status codes) —
	upstream_status: "upstream_reject",
	upstream_status_400: "upstream_reject",
	upstream_status_404: "upstream_reject",
	upstream_status_500: "upstream_reject",
	upstream_status_502: "upstream_reject",
	upstream_status_503: "upstream_reject",
	upstream_status_504: "upstream_reject",

	// — Not found —
	channel_not_found: "not_found",
	route_not_found: "not_found",
	credential_not_found: "not_found",
	plugin_not_found: "not_found",
	not_found: "not_found",

	// — Server / internal —
	internal_error: "server",
	server_error: "server",
	persistence_failure: "server",
	encryption_failed: "server",
	decrypt_failed: "server",
	response_too_large: "server",

	// — Cancelled / stream interruptions (contextual, treat as network) —
	cancelled: "network",
	stream_interrupted: "network",
};

function classForRaw(raw: string): ErrorClass {
	return CATEGORY_TO_CLASS[raw.toLowerCase()] ?? "unknown";
}

/** Keyword → class for free-form backend messages (sentences, not categories). */
const MESSAGE_KEYWORDS: Array<[RegExp, ErrorClass]> = [
	[/no api token/i, "missing_key"],
	[/no usable.*key/i, "missing_key"],
	[/masked/i, "missing_key"],
	[/cannot.*reveal/i, "missing_key"],
	[/could not be revealed/i, "missing_key"],
	[/secret.*masked/i, "missing_key"],
	[/key list/i, "missing_key"],
	[/hidden/i, "missing_key"],
	[/unauthorized/i, "auth"],
	[/invalid access token/i, "auth"],
	[/invalid token/i, "auth"],
	[/not provided/i, "auth"],
	[/rate limit/i, "rate_limited"],
	[/too many requests/i, "rate_limited"],
	[/throttl/i, "rate_limited"],
	[/connection.*(refused|reset|closed)/i, "network"],
	[/tls/i, "network"],
	[/handshake/i, "network"],
	[/timeout/i, "network"],
	[/cannot reach/i, "network"],
	[/unreachable/i, "network"],
	[/invalid base url/i, "config"],
	[/not found/i, "not_found"],
];

/**
 * Classify an error message (backend category or HTTP status text) into the
 * unified taxonomy. Also understands "status <code>" and "HTTP <code>" shapes.
 */
export function categorizeError(raw: string): CategorizedError {
	const trimmed = (raw ?? "").trim();
	const direct = classForRaw(trimmed);
	if (direct !== "unknown") {
		return { class: direct, raw: trimmed };
	}

	// "upstream_status_429" → rate_limited; "status 503" / "HTTP 503" → class.
	const statusMatch = trimmed.match(/(?:^|_)status[ _]*(\d{3})$/i) ?? trimmed.match(/HTTP[ _]*(\d{3})/i);
	if (statusMatch) {
		const status = Number(statusMatch[1]);
		const cls = classForRaw(`upstream_status_${status}`);
		if (cls !== "unknown") {
			return { class: cls, raw: trimmed, status };
		}
	}
	const bareCode = trimmed.match(/^\d{3}$/);
	if (bareCode) {
		const status = Number(bareCode[0]);
		const cls = classForRaw(`upstream_status_${status}`);
		if (cls !== "unknown") {
			return { class: cls, raw: trimmed, status };
		}
	}

	// Free-form message keyword matching.
	for (const [pattern, cls] of MESSAGE_KEYWORDS) {
		if (pattern.test(trimmed)) {
			return { class: cls, raw: trimmed };
		}
	}

	return { class: "unknown", raw: trimmed };
}
