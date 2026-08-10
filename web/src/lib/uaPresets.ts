/**
 * User-Agent spoof presets for UA-gated upstreams.
 *
 * Values come from cc-switch's userAgentPresets (PR #3671 curl-tested against
 * Kimi Coding Plan's UA allowlist): `claude-cli/*`, `claude-code/*`,
 * `Kilo-Code/*` pass; `codex-cli`, `kimi-cli` get 403. The allowlist only
 * checks the UA name prefix, not the version, so static values stay valid.
 * The first entry is the exact format the official Claude Code CLI sends
 * (`claude-cli/2.1.161 (external, cli)`), which passes strictest checks.
 */

export const UA_PRESETS: readonly string[] = [
  "claude-cli/2.1.161 (external, cli)",
  "claude-cli/2.1.161",
  "claude-code/1.0.0",
  "claude-code/0.1.0",
  "Kilo-Code/1.0",
];

/** Extract the current User-Agent value from a header_override JSON string. */
export function uaFromHeaderOverride(raw: string): string {
  try {
    const obj = JSON.parse(raw) as Record<string, unknown>;
    const ua = obj["User-Agent"];
    return typeof ua === "string" ? ua : "";
  } catch {
    return "";
  }
}

/**
 * Set (or remove when blank) the User-Agent key inside a header_override JSON
 * string. Preserves every other header. Invalid JSON is treated as empty and
 * replaced, mirroring how the backend merges header overrides.
 */
export function setUAInHeaderOverride(raw: string, ua: string): string {
  let obj: Record<string, unknown>;
  try {
    obj = JSON.parse(raw) as Record<string, unknown>;
  } catch {
    obj = {};
  }
  const trimmed = ua.trim();
  if (trimmed) obj["User-Agent"] = trimmed;
  else delete obj["User-Agent"];
  return JSON.stringify(obj);
}

/** Header sanity check: User-Agent must not contain control characters. */
export function isValidUserAgent(ua: string): boolean {
  return !/[\u0000-\u001f\u007f]/.test(ua);
}
