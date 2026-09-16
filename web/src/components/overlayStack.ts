type OverlayEntry = {
  close: () => void;
  modal: boolean;
  transient: boolean;
  element?: HTMLElement | null;
};

const stack: OverlayEntry[] = [];
let previousOverflow: string | undefined;

function lastModalIndex() {
  for (let index = stack.length - 1; index >= 0; index--) {
    if (stack[index]?.modal) return index;
  }
  return -1;
}

export type OverlayRegistration = (() => void) & {
  isTop: () => boolean;
  ownsFocus: () => boolean;
};

export function modalFocusRoots(): HTMLElement[] {
  const modal = lastModalIndex();
  if (modal < 0) return [];
  return stack.slice(modal).flatMap((entry) =>
    entry.element && !entry.transient ? [entry.element] : [],
  );
}

function closeTopOverlay(event: KeyboardEvent) {
  if (event.key !== "Escape" || event.defaultPrevented || event.isComposing) return;
  const top = stack[stack.length - 1];
  if (!top) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  top.close();
}

/**
 * Registers a mounted modal/drawer in z-order so Escape closes only the
 * visible top-most surface. Dialogs/drawers share a CSS layer, so their
 * portal DOM order determines which is on top. React child effects can
 * register before a parent effect; effect order alone is not sufficient.
 */
export function registerOverlay(
  close: () => void,
  options: { modal?: boolean; transient?: boolean; element?: HTMLElement | null } = {},
): OverlayRegistration {
  // Opening another surface dismisses old menus/popovers before they can
  // obscure a new dialog or leave multiple unrelated menus visible.
  for (const entry of [...stack]) if (entry.transient) entry.close();
  const entry: OverlayEntry = {
    close, modal: options.modal ?? true,
    transient: options.transient ?? false, element: options.element,
  };
  if (entry.modal && !stack.some((item) => item.modal)) {
    previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
  }
  stack.push(entry);
  stack.sort((left, right) => {
    if (left.transient !== right.transient) return Number(left.transient) - Number(right.transient);
    if (!left.element?.isConnected || !right.element?.isConnected) return 0;
    const position = left.element.compareDocumentPosition(right.element);
    if (position & Node.DOCUMENT_POSITION_FOLLOWING) return -1;
    if (position & Node.DOCUMENT_POSITION_PRECEDING) return 1;
    return 0;
  });
  if (stack.length === 1) {
    window.addEventListener("keydown", closeTopOverlay);
  }

  const unregister = () => {
    const index = stack.indexOf(entry);
    if (index >= 0) stack.splice(index, 1);
    if (stack.length === 0) {
      window.removeEventListener("keydown", closeTopOverlay);
    }
    if (!stack.some((item) => item.modal) && previousOverflow !== undefined) {
      document.body.style.overflow = previousOverflow;
      previousOverflow = undefined;
    }
  };
  unregister.isTop = () => stack[stack.length - 1] === entry;
  unregister.ownsFocus = () =>
    !stack[stack.length - 1]?.transient &&
    stack[lastModalIndex()] === entry;
  return unregister;
}
