/**
 * Per-member alias redirect.
 *
 * A route_members row can carry {"real": "<upstream name>"}, which lets one
 * alias route reach a differently named model on each channel. An empty
 * mapping_json means the member serves the route's own name unchanged.
 */

export interface MemberMapping {
  real?: string;
}

export function parseMemberMapping(json?: string): MemberMapping {
  if (!json?.trim()) return {};
  try {
    const parsed: unknown = JSON.parse(json);
    if (parsed && typeof parsed === "object" && "real" in parsed) {
      const real = (parsed as { real?: unknown }).real;
      if (typeof real === "string") return { real: real.trim() || undefined };
    }
  } catch {
    // A malformed mapping is treated as "no redirect" rather than fatal.
  }
  return {};
}

/** Serialises a redirect, or "" when the member should serve the route name. */
export function serializeMemberMapping(real: string): string {
  const trimmed = real.trim();
  return trimmed ? JSON.stringify({ real: trimmed }) : "";
}

/** The upstream model a member actually reaches; "" for a plain member. */
export function memberRealName(member: { mapping_json?: string }): string {
  return parseMemberMapping(member.mapping_json).real ?? "";
}
