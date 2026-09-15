/** Shared credential meta parsing (name / group / upstream_token_id / platform_user_id). */
export function parseCredentialMeta(metaJSON?: string): {
  name?: string;
  group?: string;
  upstream_token_id?: number;
  platform_user_id?: number;
  /** Raw parsed object, for callers that must round-trip unknown keys. */
  raw?: Record<string, unknown>;
} {
  if (!metaJSON?.trim()) return {};
  try {
    const parsed = JSON.parse(metaJSON) as Record<string, unknown>;
    const name = typeof parsed.name === "string" ? parsed.name : undefined;
    const group =
      typeof parsed.group === "string"
        ? parsed.group
        : typeof parsed.Group === "string"
          ? parsed.Group
          : undefined;
    const upstream =
      typeof parsed.upstream_token_id === "number"
        ? parsed.upstream_token_id
        : undefined;
    return {
      name,
      group,
      upstream_token_id: upstream,
      platform_user_id: positiveInt(parsed.platform_user_id),
      raw: parsed,
    };
  } catch {
    return {};
  }
}

/** positiveInt coerces a number or a numeric string ("1544") to a positive int. */
function positiveInt(value: unknown): number | undefined {
  const parsed =
    typeof value === "number"
      ? value
      : typeof value === "string" && value.trim() !== ""
        ? Number(value.trim())
        : Number.NaN;
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
}

/**
 * withCredentialMetaValue merges one key into an existing credential meta
 * document, preserving every other key verbatim.
 *
 * Store-owned keys (`checkin_path` / `checkin_method` / `headers` for external
 * check-in, `name` / `group` / `upstream_token_id` for API keys) must survive a
 * frontend round-trip, so the whole object is carried rather than rebuilt.
 * `undefined` removes the key; removing the last key yields `"{}"` (an empty
 * object) rather than `""`, because the admin API treats an empty meta_json as
 * "field not supplied" and would otherwise ignore the removal.
 *
 * An unparsable existing document is discarded in favour of the single key:
 * writing a user id must not be blocked by legacy garbage.
 */
export function withCredentialMetaValue(
  metaJSON: string | undefined,
  key: string,
  value: number | string | undefined,
): string {
  const existing = parseCredentialMeta(metaJSON).raw ?? {};
  const next: Record<string, unknown> = { ...existing };
  if (value === undefined) delete next[key];
  else next[key] = value;
  return JSON.stringify(next);
}
