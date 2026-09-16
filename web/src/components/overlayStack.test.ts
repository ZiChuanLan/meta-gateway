import { afterEach, describe, expect, it, vi } from "vitest";
import { registerOverlay } from "./overlayStack";

describe("overlay stack", () => {
  afterEach(() => {
    // Each test unregisters its entries; this also documents that the stack
    // owns only mounted overlays and does not retain callbacks between tests.
    vi.restoreAllMocks();
  });

  it("closes only the top-most overlay on Escape", () => {
    const bottom = vi.fn();
    const top = vi.fn();
    const unregisterBottom = registerOverlay(bottom);
    const unregisterTop = registerOverlay(top);

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    expect(top).toHaveBeenCalledTimes(1);
    expect(bottom).not.toHaveBeenCalled();

    unregisterTop();
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    expect(bottom).toHaveBeenCalledTimes(1);

    unregisterBottom();
  });

  it("follows portal DOM order when effects register in a different order", () => {
    const lower = document.createElement("section");
    const upper = document.createElement("section");
    document.body.append(lower, upper);
    const closeLower = vi.fn();
    const closeUpper = vi.fn();
    const unregisterUpper = registerOverlay(closeUpper, { element: upper });
    const unregisterLower = registerOverlay(closeLower, { element: lower });
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    expect(closeUpper).toHaveBeenCalledOnce();
    expect(closeLower).not.toHaveBeenCalled();
    expect(unregisterUpper.ownsFocus()).toBe(true);
    unregisterUpper();
    unregisterLower();
    lower.remove();
    upper.remove();
  });
});
