/**
 * Tab-scoped persistence for the channels board.
 *
 * The sidebar navigates to a bare `/channels` (no query string), so URL-only
 * filters are lost on every page switch. These helpers keep the current tab's
 * search, filters and selected row across navigation, deliberately without the
 * URL: the URL stays the shareable truth (see the filter hook), the session
 * storage is only a memory of where the operator was.
 */
export function readChannelTab<T>(key: string, fallback: T): T {
  try {
    const raw = sessionStorage.getItem(`channels.${key}`);
    return raw != null ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

export function writeChannelTab<T>(key: string, value: T) {
  try {
    sessionStorage.setItem(`channels.${key}`, JSON.stringify(value));
  } catch {
    // Storage unavailable; state stays in memory for this render.
  }
}
