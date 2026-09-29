/**
 * Remembers whether the channel editor's advanced section was left open.
 *
 * The section holds the settings that actually change how requests are
 * forwarded (payload rules, header overrides, retry, endpoint mapping), so
 * "collapsed" must never mean "invisible": the dialog shows a configured-count
 * badge either way, and this preference only carries the operator's explicit
 * choice between openings. Storage failures (private mode, quota) degrade to
 * "no preference stored" rather than breaking the dialog.
 */

const STORAGE_KEY = "meta-gateway.channel-advanced-open";

export function readAdvancedOpen(): boolean | null {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (raw === "1") return true;
    if (raw === "0") return false;
    return null;
  } catch {
    return null;
  }
}

export function writeAdvancedOpen(open: boolean): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, open ? "1" : "0");
  } catch {
    // A dialog that cannot persist a preference still works.
  }
}
