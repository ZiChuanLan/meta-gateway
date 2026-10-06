import { afterEach, expect, it, vi } from "vitest";
import { deferUntilNoModal } from "./deferUntilNoModal";
afterEach(() => {
  document.body.replaceChildren();
  vi.useRealTimers();
});
it("waits for the existing dialog to close and can be cancelled", () => {
  vi.useFakeTimers();
  const modal = document.createElement("div");
  modal.setAttribute("role", "dialog");
  document.body.append(modal);
  const start = vi.fn();
  const cancel = deferUntilNoModal(start);
  vi.advanceTimersByTime(1200);
  expect(start).not.toHaveBeenCalled();
  modal.remove();
  vi.advanceTimersByTime(600);
  expect(start).toHaveBeenCalledTimes(1);
  cancel();
  const next = vi.fn();
  const cancelNext = deferUntilNoModal(next);
  cancelNext();
  vi.advanceTimersByTime(600);
  expect(next).not.toHaveBeenCalled();
});
