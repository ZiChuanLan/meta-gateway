import { describe, expect, it } from "vitest";
import { scrollRailByWheel } from "./useHorizontalWheel";

// jsdom has no layout engine, so the geometry this rule reads is faked by hand.
// That is exactly why the decision is a pure function: the assertions below are
// about the rule, not about the mocks an event-driven test would have needed.
function rail({ width = 600, visible = 300, scrollLeft = 0 } = {}) {
  const node = document.createElement("div");
  let left = scrollLeft;
  Object.defineProperty(node, "scrollWidth", { value: width, configurable: true });
  Object.defineProperty(node, "clientWidth", { value: visible, configurable: true });
  Object.defineProperty(node, "scrollLeft", {
    get: () => left,
    set: (value: number) => {
      // Browsers clamp to the scrollable range; so does this fake.
      left = Math.max(0, Math.min(value, width - visible));
    },
    configurable: true,
  });
  return node;
}

describe("scrollRailByWheel", () => {
  it("moves the rail on a vertical wheel gesture", () => {
    const node = rail();
    expect(scrollRailByWheel(node, 0, 120)).toBe(true);
    expect(node.scrollLeft).toBe(120);
  });

  it("hands the gesture back when the rail already fits", () => {
    const node = rail({ width: 300, visible: 300 });
    expect(scrollRailByWheel(node, 0, 120)).toBe(false);
    expect(node.scrollLeft).toBe(0);
  });

  it("leaves a horizontal gesture to the trackpad", () => {
    const node = rail();
    expect(scrollRailByWheel(node, 120, 0)).toBe(false);
    expect(node.scrollLeft).toBe(0);
  });

  it("releases the gesture at the end of the rail", () => {
    const node = rail({ scrollLeft: 300 });
    expect(scrollRailByWheel(node, 0, 120)).toBe(false);
    expect(node.scrollLeft).toBe(300);
  });

  it("still scrolls back from the end", () => {
    const node = rail({ scrollLeft: 300 });
    expect(scrollRailByWheel(node, 0, -120)).toBe(true);
    expect(node.scrollLeft).toBe(180);
  });

  it("takes a mostly-vertical diagonal gesture", () => {
    const node = rail();
    expect(scrollRailByWheel(node, 20, 100)).toBe(true);
  });
});
