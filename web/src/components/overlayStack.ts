type OverlayEntry = {
  close: () => void;
};

const stack: OverlayEntry[] = [];

function closeTopOverlay(event: KeyboardEvent) {
  if (event.key !== "Escape") return;
  const top = stack[stack.length - 1];
  if (!top) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  top.close();
}

/**
 * Registers a mounted modal/drawer in z-order so Escape closes only the
 * visible top-most surface. The registration order follows portal mount
 * order, which also matches the visual stacking order.
 */
export function registerOverlay(close: () => void): () => void {
  const entry: OverlayEntry = { close };
  stack.push(entry);
  if (stack.length === 1) {
    window.addEventListener("keydown", closeTopOverlay);
  }

  return () => {
    const index = stack.indexOf(entry);
    if (index >= 0) stack.splice(index, 1);
    if (stack.length === 0) {
      window.removeEventListener("keydown", closeTopOverlay);
    }
  };
}
