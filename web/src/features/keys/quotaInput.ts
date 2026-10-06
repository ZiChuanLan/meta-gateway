/** Blank explicitly means unlimited; malformed input must never become zero. */
export function parseQuotaInput(raw: string, wholeTokens = false): number | null {
  if (!raw.trim()) return 0;
  const value = Number(raw);
  if (!Number.isFinite(value) || value < 0) return null;
  if (wholeTokens && !Number.isSafeInteger(value)) return null;
  return value;
}
