import { useCallback, useEffect } from "react";
import { useBeforeUnload, useLocation } from "react-router-dom";
import { useI18n } from "../i18n";

/**
 * Keeps unsaved work from disappearing without a word.
 *
 * The shared dialogs protect `busy` (a write in flight) but have no notion of
 * `dirty`, so a settings page owns that contract itself. Two paths can carry
 * local edits away:
 *
 *   - closing or reloading the tab: covered by `beforeunload` here, and by the
 *     browser, which decides what to show;
 *   - any in-app navigation — a link, a command-palette jump, a programmatic
 *     `navigate()`, or the browser's Back/Forward buttons: covered by the
 *     router-level guard in `RouterBoundary.tsx`.
 *
 * This module owns the registry that connects the two. A page calls
 * `useUnsavedChanges(dirty)` while it holds edits; the hook records the
 * pathname those edits belong to, and the router guard refuses to start a
 * navigation that would leave one behind until the operator says so.
 *
 * The pathname is the guard's scope: a move that only changes the query or the
 * hash stays on the page, and pages that own such a move (the settings page's
 * `?tab=` switches) guard it themselves with the predicate this hook returns.
 */

/** A mounted editing surface that currently holds unsaved edits. */
interface DirtyZone {
  /**
   * The pathname the edits belong to. Navigating within the same pathname (a
   * tab switched through `?tab=` or a `#hash`) is not a leave: those switches
   * are the page's own to guard, so the router guard stays out of the way and
   * cannot double-prompt.
   */
  pathname: string;
}

/**
 * Every dirty surface mounted right now. A module-level registry rather than a
 * context keeps the guard free of a provider: the hook registers wherever it
 * runs, and the guard reads at navigation time. A tree with no guard (a
 * component test, a member page) simply leaves the entries unread.
 */
const dirtyZones = new Set<DirtyZone>();

/**
 * True when leaving `currentPathname` for `nextPathname` would abandon edits.
 *
 * Both sides matter. The registry can hold zones for surfaces that are mounted
 * but not on screen (a settings tab that keeps its panels alive), so a move has
 * to be judged against where the operator actually is: blocking on "the target
 * differs from some dirty zone" made an off-screen draft freeze navigation from
 * every other page — "I changed nothing, why is it asking?".
 *
 * `nextPathname` arrives stripped of the router's basename — the same form
 * `useLocation().pathname` uses — because `useBlocker` strips it before calling
 * the guard.
 */
export function leavesDirtyZone(currentPathname: string, nextPathname: string): boolean {
  if (currentPathname === nextPathname) return false;
  for (const zone of dirtyZones) {
    if (zone.pathname === currentPathname) return true;
  }
  return false;
}

/**
 * Registers the calling surface as dirty while it has unsaved edits, so the
 * router guard refuses to navigate away from it. Must run inside a router
 * (`useLocation`).
 *
 * The returned predicate stays for callers that own a same-pathname move the
 * router guard deliberately ignores — the settings page's `?tab=` switches.
 * Call it before navigating, and the guard will not ask a second time because
 * the pathname did not change.
 */
export function useUnsavedChanges(dirty: boolean) {
  const { t } = useI18n();
  const { pathname } = useLocation();

  useEffect(() => {
    if (!dirty) return;
    const zone: DirtyZone = { pathname };
    dirtyZones.add(zone);
    return () => {
      dirtyZones.delete(zone);
    };
  }, [dirty, pathname]);

  // The listener stays mounted and no-ops while clean, so arming the prompt
  // never races the effect that would have attached it.
  const beforeUnload = useCallback(
    (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault();
      // Browsers show their own wording; a non-empty returnValue is what arms
      // the prompt, and an empty string is the documented no-op form.
      event.returnValue = "";
    },
    [dirty],
  );
  useBeforeUnload(beforeUnload);

  /** True when the caller may proceed; false when the operator chose to stay. */
  return useCallback(() => !dirty || window.confirm(t("common.unsavedConfirm")), [dirty, t]);
}
