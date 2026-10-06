/**
 * Key-paste helpers for the credential surfaces.
 *
 * Operators paste keys from a panel that is often one-per-line, sometimes
 * comma-separated, and occasionally with a trailing note ("sk-… # backup").
 * Splitting and de-duplicating here means the form can add every key in one
 * go instead of silently keeping only the first line.
 */

import { KEY_HINTS, KEY_PREFIXES } from "../connectionTypes";

export type KeyPaste = {
  /** Unique keys in paste order. */
  keys: string[];
  /** Entries dropped because the same value appeared earlier. */
  duplicates: number;
};

/**
 * splitApiKeys reads one-or-many keys out of a pasted blob.
 *
 * Splits on newlines, commas, semicolons and runs of spaces; strips an inline
 * "#…" note and surrounding quotes; drops empties. Values are never rewritten
 * beyond that trimming — a key that legitimately contains a space cannot be
 * recovered from such a paste anyway, and guessing would corrupt it.
 */
export function splitApiKeys(text: string): KeyPaste {
  const seen = new Set<string>();
  const keys: string[] = [];
  let duplicates = 0;
  // Strip an inline note per line first, then split the line: a comment marker
  // only ever ends the value it trails, not the rest of the paste.
  const candidates = text
    .split(/\r?\n/)
    .map((line) => line.replace(/#.*$/, ""))
    .flatMap((line) => line.split(/[\s,;]+/))
    .map((part) => part.trim().replace(/^["']|["']$/g, ""))
    .filter(Boolean);
  for (const candidate of candidates) {
    if (seen.has(candidate)) {
      duplicates += 1;
      continue;
    }
    seen.add(candidate);
    keys.push(candidate);
  }
  return { keys, duplicates };
}

/** keyHintFor returns the documented key shape for a connection type. */
export function keyHintFor(type: string): string {
  return KEY_HINTS[type] ?? "";
}

/**
 * apiKeyLooksWrong reports an obvious prefix mismatch for the vendor types
 * whose keys carry a fixed prefix. It is advisory only: relay sites hand out
 * arbitrary tokens, and an OpenAI-compatible endpoint is a wire format rather
 * than a vendor, so neither ever earns a complaint.
 */
export function apiKeyLooksWrong(type: string, key: string): boolean {
  const prefix = KEY_PREFIXES[type];
  const value = key.trim();
  if (!prefix || value === "" || value.includes(" ")) return false;
  return !value.startsWith(prefix);
}
