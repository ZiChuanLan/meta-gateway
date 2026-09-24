import { useEffect, useRef } from "react";

/**
 * Drives an overflowing rail with the mouse wheel and reports whether the
 * gesture was consumed.
 *
 * Exported separately from the hook so the decision can be tested as a plain
 * function: jsdom has no layout engine, so scrollWidth / clientWidth /
 * scrollLeft are all fake there and an event-driven test would end up asserting
 * on its own mocks rather than on the behaviour.
 *
 * Returns true when the rail actually moved, meaning the caller should
 * preventDefault. False hands the gesture back to the page — no overflow, a
 * horizontal gesture the trackpad already handles on its own, or an end of the
 * rail where the page must keep scrolling instead of dead-ending.
 */
export function scrollRailByWheel(node: HTMLElement, deltaX: number, deltaY: number): boolean {
  if (node.scrollWidth <= node.clientWidth) return false;
  if (Math.abs(deltaY) <= Math.abs(deltaX)) return false;
  const before = node.scrollLeft;
  node.scrollLeft = before + deltaY;
  return node.scrollLeft !== before;
}

/**
 * Returns a ref for a horizontally scrollable rail that a plain mouse wheel can
 * reach.
 *
 * `overflow-x: auto` alone only answers a trackpad's two-finger swipe (or
 * shift+wheel), and this rail hides its scrollbar for looks — so with a mouse
 * every item past the edge is unreachable and there is no handle to drag
 * either. This is the standard tab-strip behaviour instead: while the rail can
 * still move, the wheel drives it.
 *
 * The listener is registered by hand because React's `onWheel` is passive and a
 * passive listener cannot preventDefault — without that the page would scroll
 * behind the rail at the same time as the rail scrolls sideways.
 */
export function useHorizontalWheel<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  useEffect(() => {
    const node = ref.current;
    if (!node) return;
    const onWheel = (event: WheelEvent) => {
      if (scrollRailByWheel(node, event.deltaX, event.deltaY)) {
        event.preventDefault();
      }
    };
    node.addEventListener("wheel", onWheel, { passive: false });
    return () => node.removeEventListener("wheel", onWheel);
  }, []);
  return ref;
}
