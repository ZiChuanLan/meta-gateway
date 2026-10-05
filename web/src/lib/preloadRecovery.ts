/**
 * Recover from a stale bundle.
 *
 * The console and the user app ship as content-addressed chunks: `index.html`
 * is served `no-cache` and every asset is `immutable`. That is correct for
 * caching, but it means a tab that was opened before a deploy still runs the
 * OLD build while the server only has the NEW files — so the next lazily
 * imported page (Exchange, Check-ins, …) requests a hash that no longer
 * exists and the import rejects with
 * "Failed to fetch dynamically imported module".
 *
 * Nothing is broken on either side: the tab is simply out of date. Vite
 * announces exactly this case with a `vite:preloadError` event, so the fix is
 * to reload once — the fresh shell then points at chunks that exist.
 *
 * The reload is guarded by a session timestamp: if it fails twice in a row
 * (a genuinely missing file, a broken deploy, an offline user), reloading
 * again would only make the tab flicker forever. On that second failure we
 * stop and tell the app, which surfaces a readable message instead.
 */

/** Session key holding the time of the last automatic reload. */
const RELOAD_STAMP = "meta-gateway.preload-reload";

/**
 * How long after an automatic reload another one is refused. Long enough that
 * a real failure cannot loop, short enough that a user who keeps the tab open
 * across two deploys still recovers on the next stale import.
 */
const RELOAD_COOLDOWN_MS = 30_000;

/** Fired when recovery is refused because it already ran recently. */
export const PRELOAD_EXHAUSTED_EVENT = "meta-gateway:preload-exhausted";

function readStamp(): number {
  try {
    const raw = sessionStorage.getItem(RELOAD_STAMP);
    const value = Number(raw ?? 0);
    return Number.isFinite(value) ? value : 0;
  } catch {
    // When storage is unavailable, the handler refuses an automatic reload:
    // an in-memory cooldown would be lost on reload and could loop forever.
    return 0;
  }
}

function writeStamp(value: number): boolean {
  try {
    sessionStorage.setItem(RELOAD_STAMP, String(value));
    return sessionStorage.getItem(RELOAD_STAMP) === String(value);
  } catch {
    return false;
  }
}

/**
 * Whether a stale-chunk failure was seen very recently.
 *
 * This is the signal the error boundary uses to tell "this tab outlived a
 * deploy" from "this build is broken": the browser words the chunk failure
 * differently depending on engine and version (`Failed to fetch dynamically
 * imported module`, `Importing a module script failed`, and React's own
 * `Cannot read properties of undefined` when the lazy payload never arrived),
 * so matching on the message alone is fragile. The stamp is written by the
 * handler below and survives the reload it triggers.
 */
export function recentPreloadFailure(): boolean {
  const stamp = readStamp();
  return stamp > 0 && Date.now() - stamp < RELOAD_COOLDOWN_MS;
}

/**
 * Installs the handler. Called from both entry modules before React mounts, so
 * a failure during the very first lazily imported chunk is covered too.
 */
export function installPreloadRecovery(target: Window = window): void {
  target.addEventListener("vite:preloadError", (event) => {
    // Keep the browser's console quiet: this is a recovered condition, not an
    // unhandled error the user should have to read.
    event.preventDefault?.();
    const now = Date.now();
    const last = readStamp();
    if (last > 0 && now - last < RELOAD_COOLDOWN_MS) {
      target.dispatchEvent(new CustomEvent(PRELOAD_EXHAUSTED_EVENT));
      return;
    }
    if (!writeStamp(now)) {
      target.dispatchEvent(new CustomEvent(PRELOAD_EXHAUSTED_EVENT));
      return;
    }
    target.location.reload();
  });
}
