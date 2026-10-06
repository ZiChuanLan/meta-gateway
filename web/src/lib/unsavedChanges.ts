import { useCallback, useEffect } from "react";
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
 *   - an in-app navigation the caller can wrap with the returned predicate.
 *
 * A route-level blocker is deliberately absent: this app uses the declarative
 * `BrowserRouter`, whose `useBlocker` needs a data router. Callers wrap the
 * links they own (the settings tabs, the panel's own links) and the sidebar is
 * left to the browser prompt.
 */
export function useUnsavedChanges(dirty: boolean) {
  const { t } = useI18n();
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
