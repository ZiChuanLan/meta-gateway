import { useEffect, useRef, type RefObject } from "react";
import { modalFocusRoots, registerOverlay } from "./overlayStack";

export function focusableElements(root: ParentNode): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(
    'button:not(:disabled), a[href], input:not(:disabled):not([type="hidden"]), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
  )).filter((element) => {
    if (element.matches(":disabled") || element.tabIndex < 0) return false;
    if (element.closest('[hidden], [inert]')) return false;
    if (typeof element.checkVisibility === "function" && !element.checkVisibility()) return false;
    const style = getComputedStyle(element);
    return style.display !== "none" && style.visibility !== "hidden";
  });
}

export function focusAfterPopover(anchor: HTMLElement | null, panel: HTMLElement | null, backward: boolean) {
  const roots = modalFocusRoots();
  const candidates = (roots.length ? roots.flatMap(focusableElements) : focusableElements(document))
    .filter((element) => !panel?.contains(element));
  const index = anchor ? candidates.indexOf(anchor) : -1;
  const next = index < 0 ? (backward ? candidates.length - 1 : 0)
    : (index + (backward ? -1 : 1) + candidates.length) % candidates.length;
  candidates[next]?.focus({ preventScroll: true });
}

/** One focus/scroll contract for dialogs and drawers, including side panels. */
export function useModalFocus(
  ref: RefObject<HTMLElement | null>,
  onClose: () => void,
  modal = true,
) {
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    const node = ref.current;
    if (!node) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const unregister = registerOverlay(() => closeRef.current(), { modal, element: node });
    if (!node.contains(document.activeElement)) {
      (focusableElements(node)[0] ?? node).focus({ preventScroll: true });
    }
    const onKeydown = (event: KeyboardEvent) => {
      if (event.key !== "Tab" || !unregister.ownsFocus()) return;
      const roots = modalFocusRoots();
      const items = roots.flatMap(focusableElements);
      const active = document.activeElement;
      const inside = roots.some((root) => root.contains(active));
      if (!items.length) {
        event.preventDefault();
        node.focus({ preventScroll: true });
      } else if (event.shiftKey && (!inside || active === items[0] || active === node)) {
        event.preventDefault();
        items[items.length - 1]?.focus();
      } else if (!event.shiftKey && (!inside || active === items[items.length - 1])) {
        event.preventDefault();
        items[0]?.focus();
      }
    };
    window.addEventListener("keydown", onKeydown);
    return () => {
      unregister();
      window.removeEventListener("keydown", onKeydown);
      if (previous?.isConnected && (node.contains(document.activeElement) || document.activeElement === document.body)) {
        previous.focus({ preventScroll: true });
      }
    };
  }, [modal, ref]);
}
