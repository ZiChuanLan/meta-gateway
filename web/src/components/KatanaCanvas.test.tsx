import { cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { KatanaCanvas } from "./KatanaCanvas";

let hidden = false;
let reduced = false;
beforeEach(() => {
  hidden = false; reduced = false;
  vi.stubGlobal("CanvasRenderingContext2D", class {});
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.stubGlobal("requestAnimationFrame", vi.fn(() => 17));
  vi.stubGlobal("cancelAnimationFrame", vi.fn());
  vi.stubGlobal("matchMedia", vi.fn(() => ({ get matches() { return reduced; }, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
  vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden);
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ setTransform: vi.fn(), clearRect: vi.fn() } as unknown as CanvasRenderingContext2D);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("pauses when the page is hidden, resumes on return, and cancels on unmount", () => {
  const { unmount } = render(<KatanaCanvas charging chargeProgress={1} />);
  expect(requestAnimationFrame).toHaveBeenCalledOnce();
  hidden = true; fireEvent(document, new Event("visibilitychange"));
  expect(cancelAnimationFrame).toHaveBeenCalledOnce();
  expect(requestAnimationFrame).toHaveBeenCalledOnce();
  hidden = false; fireEvent(document, new Event("visibilitychange"));
  expect(requestAnimationFrame).toHaveBeenCalledTimes(2);
  unmount();
  expect(cancelAnimationFrame).toHaveBeenCalledTimes(2);
});

it("does not start a frame loop when reduced motion is enabled", () => {
  reduced = true;
  render(<KatanaCanvas charging chargeProgress={1} />);
  expect(requestAnimationFrame).not.toHaveBeenCalled();
});
