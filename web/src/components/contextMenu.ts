import type { KeyboardEvent, MouseEvent } from "react";

// Keep browser copy/link/input menus available. Shift + right click is an
// explicit escape hatch to the browser's own menu.
export function rowContextPoint(event: MouseEvent<HTMLElement>) {
  const target = event.target instanceof Element ? event.target : null;
  if (event.shiftKey || target?.closest('a[href], input, textarea, select, [contenteditable="true"]') || window.getSelection()?.toString()) return null;
  event.preventDefault();
  event.stopPropagation();
  event.currentTarget.focus({ preventScroll: true });
  const rect = event.currentTarget.getBoundingClientRect();
  return {
    left: event.clientX || rect.left + 16,
    top: event.clientY || rect.top + Math.min(rect.height, 28),
  };
}

export function rowKeyboardContextPoint(event: KeyboardEvent<HTMLElement>) {
  if (event.target !== event.currentTarget) return null;
  if (event.key !== "ContextMenu" && !(event.shiftKey && event.key === "F10")) {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      event.currentTarget.click();
    }
    return null;
  }
  event.preventDefault();
  event.stopPropagation();
  const rect = event.currentTarget.getBoundingClientRect();
  return { left: rect.left + 16, top: rect.top + Math.min(rect.height, 28) };
}
