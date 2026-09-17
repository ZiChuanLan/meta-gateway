type Holder = { [key: string]: unknown };

/**
 * Providers wrap refusals differently; show whatever they actually said.
 *
 * Upstreams answer with `{"error":{"code":…,"message":…}}`, a bare `error`
 * string, or occasionally plain text. The console used to replace all of that
 * with a generic "upstream returned HTTP 429" line, which hid the one detail
 * that explains the failure — the code and message distinguish an exhausted
 * quota from an upstream rate limit. Prefer the message, fall back to the raw
 * body so nothing is swallowed.
 */
export function upstreamMessage(body: unknown): string {
	if (typeof body === "string") return body.trim().slice(0, 400);
	if (!body || typeof body !== "object") return "";
	const error = (body as Holder).error;
	if (typeof error === "string") return error;
	if (error && typeof error === "object") {
		const message = (error as Holder).message;
		if (typeof message === "string") return message.trim();
	}
	return JSON.stringify(body).slice(0, 400);
}
