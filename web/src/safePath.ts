/**
 * Accept only same-app relative paths for post-create return navigation.
 * Rejects protocol-relative URLs, absolute schemes, and backslash tricks.
 */
export function isSafeInternalPath(
	value: string | null | undefined,
): value is string {
	if (!value) return false;
	if (!value.startsWith("/")) return false;
	if (value.startsWith("//")) return false;
	if (value.includes("\\")) return false;
	// Reject "http:...", "javascript:...", "data:...", etc. even after a leading slash mistake
	// like "/http://evil" is allowed as a path segment; schemes only apply without leading /
	// but " /\tjavascript:" style is already blocked by startswith /
	if (/^\/[a-zA-Z][a-zA-Z0-9+.-]*:/.test(value)) return false;
	return true;
}

/** Build /keys deep-link with optional auto-create and safe return path. */
export function buildKeysHref(options?: {
	create?: boolean;
	returnTo?: string | null;
}): string {
	const params = new URLSearchParams();
	if (options?.create) params.set("create", "1");
	const ret = options?.returnTo;
	if (ret && isSafeInternalPath(ret)) {
		params.set("return", ret);
	}
	const query = params.toString();
	return query ? `/keys?${query}` : "/keys";
}
