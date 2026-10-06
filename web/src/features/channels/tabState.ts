import { readScopedTabState, writeScopedTabState } from "../../lib/tabState";

/**
 * The connections board's tab memory (search, filters, selected row), namespaced
 * so it cannot collide with the models board's. The implementation is shared; see
 * lib/tabState.ts for why this exists at all.
 */
const SCOPE = "channels";

export function readChannelTab<T>(key: string, fallback: T): T {
  return readScopedTabState(SCOPE, key, fallback);
}

export function writeChannelTab<T>(key: string, value: T): void {
  writeScopedTabState(SCOPE, key, value);
}
