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
});
