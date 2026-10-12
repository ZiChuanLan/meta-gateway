import { createElement, useCallback, useContext, useEffect, useMemo, type ReactNode } from "react";
import type React from "react";
import { UNSAFE_NavigationContext, useLocation } from "react-router-dom";
import { useI18n } from "../i18n";

/**
 * Keeps unsaved work from disappearing without a word.
 *
 * The shared dialogs protect `busy` (a write in flight) but have no notion of
 * `dirty`, so a settings page owns that contract itself. Two paths can carry
 * local edits away:
 *
 *   - closing or reloading the tab: covered by `beforeunload` (the browser,
 *     not the app, decides what to show);
 *   - an in-app navigation: covered by the shared boundary below, which
 *     `useUnsavedChanges` registers a dirty surface with.
 *
 * The boundary re-publishes `UNSAFE_NavigationContext` with a wrapped
 * `navigator`. That is the one seam a declarative `BrowserRouter` exposes for
 * in-app navigation: `<Link>`, `<NavLink>`, `<Navigate>` and `useNavigate` all
 * resolve their target and then call `navigator.push`/`replace`/`go`, so
 * wrapping those three covers every link, every command-palette jump and every
 * programmatic `navigate()` without patching global `history` or sprinkling
 * confirms through the tree. The API is prefixed `UNSAFE_` because it is not
 * covered by react-router's semver promise — the shape read here
 * (`basename` + `navigator`) is asserted by `unsavedChanges.test.tsx`, so a
 * breaking upgrade fails loudly instead of silently dropping the guard.
 *
 * ⚠️ A POP that the operator starts in the browser itself (the Back/Forward
 * buttons) does *not* go through `navigator`: the browser fires `popstate`,
 * the router's single history listener updates the location, and the app never
 * sees a navigator call to intercept. Blocking that needs a data router
 * (`useBlocker` throws outside one). This boundary therefore covers in-app
 * navigation only; the tab's Back button is left to the browser's own
 * `beforeunload` prompt, which does not fire for an in-page POP.
 */

/** A mounted editing surface that currently holds unsaved edits. */
interface DirtyZone {
  /**
   * The pathname the edits belong to — the scope of the guard. Navigating
   * within the same pathname (a tab switched through `?tab=` or a `#hash`) is
   * not a leave: those switches are the page's own to guard, so the boundary
   * stays out of the way and cannot double-prompt.
   */
  pathname: string;
}

/**
 * Every dirty surface mounted right now. A module-level registry rather than a
 * context keeps the boundary free of a second provider and lets the hook work
 * unchanged in trees that render without the boundary (tests, member pages).
 */
const dirtyZones = new Set<DirtyZone>();

/** True when proceeding would leave at least one dirty surface behind. */
function leavesDirtyZone(target: string | null): boolean {
  for (const zone of dirtyZones) {
    // An unknown target (a `go(delta)` the router cannot resolve) is assumed
    // to leave; the same-pathname check needs the resolved pathname.
    if (target === null || zone.pathname !== target) return true;
  }
  return false;
}

/** Mirrors react-router's own basename stripping — keep the two in step. */
function trimBasename(pathname: string, basename: string): string {
  const base = basename.replace(/\/+$/, "") || "/";
  if (base === "/") return pathname;
  if (!pathname.toLowerCase().startsWith(base.toLowerCase())) return pathname;
  const rest = pathname.slice(base.length);
  return rest === "" || rest.startsWith("/") ? rest || "/" : pathname;
}

/**
 * The pathname a navigation target heads for, relative to the router's
 * basename. `navigator.push`/`replace` are handed a target that already
 * carries the basename, while `useLocation().pathname` does not, so the two
 * sides only line up after this. A target that cannot be read resolves to
 * `null`, which `leavesDirtyZone` treats as a leave.
 */
function targetPathname(to: unknown, basename: string): string | null {
  const pathname =
    typeof to === "string"
      ? (to.split(/[?#]/, 1)[0] ?? "")
      : typeof to === "object" &&
          to !== null &&
          typeof (to as { pathname?: unknown }).pathname === "string"
        ? (to as { pathname: string }).pathname
        : null;
  if (pathname === null) return null;
  return trimBasename(pathname, basename);
}

type NavigationValue = React.ContextType<typeof UNSAFE_NavigationContext>;
type Navigator = NavigationValue["navigator"];

/**
 * Registers the calling surface as dirty while it has unsaved edits, so the
 * shared boundary below guards any in-app navigation away from it. Must run
 * inside a router (`useLocation`); mount `UnsavedChangesBoundary` once, above
 * the app, to arm the guard.
 *
 * The returned predicate stays for callers that own a same-pathname move the
 * boundary deliberately ignores — the settings page's `?tab=` switches. Call
 * it before navigating, and the boundary will not ask a second time because
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

  useEffect(() => {
    if (!dirty) return;
    const handler = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      // Browsers show their own wording; a non-empty returnValue is what arms
      // the prompt, and an empty string is the documented no-op form.
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [dirty]);

  /** True when the caller may proceed; false when the operator chose to stay. */
  return useCallback(() => !dirty || window.confirm(t("common.unsavedConfirm")), [dirty, t]);
}

/**
 * Wraps the router's navigator once so every in-app navigation asks before it
 * discards a registered draft. Render it inside the router and above the app:
 *
 *   <BrowserRouter basename="/console">
 *     <UnsavedChangesBoundary>
 *       <App />
 *     </UnsavedChangesBoundary>
 *   </BrowserRouter>
 *
 * A declined navigation is simply not forwarded: the URL keeps its entry, the
 * page stays mounted and the draft is untouched. An accepted one is forwarded
 * exactly once, so the one `confirm` per move cannot cascade.
 */
export function UnsavedChangesBoundary({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  const navigation = useContext(UNSAFE_NavigationContext);
  const { basename, navigator: base } = navigation;

  const navigator = useMemo<Navigator>(() => {
    const confirmLeave = (target: string | null) =>
      !leavesDirtyZone(target) || window.confirm(t("common.unsavedConfirm"));
    return {
      ...base,
      push(to, state, opts) {
        if (confirmLeave(targetPathname(to, basename))) base.push(to, state, opts);
      },
      replace(to, state, opts) {
        if (confirmLeave(targetPathname(to, basename))) base.replace(to, state, opts);
      },
      go(delta) {
        // The router hands `go` a delta, not a destination, so the target is
        // unknown here and any dirty surface is treated as being left.
        if (confirmLeave(null)) base.go(delta);
      },
    };
  }, [base, basename, t]);

  const value = useMemo(() => ({ ...navigation, navigator }), [navigation, navigator]);
  return createElement(UNSAFE_NavigationContext.Provider, { value }, children);
}
