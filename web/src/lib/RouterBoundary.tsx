import { useContext, useEffect, useRef, type ReactNode } from "react";
import {
  UNSAFE_DataRouterStateContext,
  useBlocker,
  type BlockerFunction,
  type Location,
} from "react-router-dom";
import { useI18n } from "../i18n";
import { leavesDirtyZone } from "./unsavedChanges";

/** A move is held back only when it would leave the page the operator is on. */
const shouldBlockLeave: BlockerFunction = ({ currentLocation, nextLocation }) =>
  leavesDirtyZone(currentLocation.pathname, nextLocation.pathname);

/**
 * The router-level half of the unsaved-changes contract; `unsavedChanges.ts`
 * owns the registry and the dirty hook that feed it.
 *
 * Install it once, wrapping the element of a data router's single root route:
 *
 *   const router = createBrowserRouter(
 *     [{ path: "*", element: <UnsavedChangesBoundary><App /></UnsavedChangesBoundary> }],
 *     { basename: "/console" },
 *   );
 *   <RouterProvider router={router} />
 *
 * The pages themselves did not change: `App` keeps its own `<Routes>`, and the
 * root route's `*` hands it the whole pathname.
 *
 * `useBlocker` is why this has to be a data router. It is the only API that
 * also holds back a navigation the app did not start — the browser's
 * Back/Forward buttons. A declarative `BrowserRouter` cannot: a POP goes
 * straight from `popstate` to the router's single history listener, never
 * through the navigator an app can wrap.
 */
export function UnsavedChangesBoundary({ children }: { children: ReactNode }) {
  // `useBlocker` throws outside a data router. main.tsx installs this under
  // `RouterProvider`, so this check is only a safety net for a declarative one
  // (a component test, a stray render): there the boundary degrades to no
  // router guard, while the hook's own beforeunload/predicate still hold.
  const dataRouter = useContext(UNSAFE_DataRouterStateContext);
  return dataRouter ? <GuardedNavigation>{children}</GuardedNavigation> : <>{children}</>;
}

function GuardedNavigation({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  const blocker = useBlocker(shouldBlockLeave);
  // One prompt per held-back move. The effect also runs for unrelated
  // re-renders and, under StrictMode, twice for one mount; asking again would
  // put a second dialog in front of the same navigation.
  const asked = useRef<Location | null>(null);

  useEffect(() => {
    if (blocker.state !== "blocked") {
      asked.current = null;
      return;
    }
    if (asked.current === blocker.location) return;
    asked.current = blocker.location;
    if (window.confirm(t("common.unsavedConfirm"))) blocker.proceed();
    else blocker.reset();
  }, [blocker, t]);

  return <>{children}</>;
}
