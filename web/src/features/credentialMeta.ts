/** Shared credential meta parsing (name / group / upstream_token_id). */
export function parseCredentialMeta(metaJSON?: string): {
  name?: string;
  group?: string;
  upstream_token_id?: number;
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
    return { name, group, upstream_token_id: upstream };
  } catch {
    return {};
  }
}
