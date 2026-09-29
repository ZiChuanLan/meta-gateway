import { beforeEach, describe, expect, it, vi } from "vitest";
import { readAdvancedOpen, writeAdvancedOpen } from "./advancedPrefs";

describe("advanced-section preference", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("has no opinion before the operator makes one", () => {
    expect(readAdvancedOpen()).toBeNull();
  });

  it("round-trips an explicit choice", () => {
    writeAdvancedOpen(true);
    expect(readAdvancedOpen()).toBe(true);
    writeAdvancedOpen(false);
    expect(readAdvancedOpen()).toBe(false);
  });

  it("ignores anything else in the slot", () => {
    window.localStorage.setItem("meta-gateway.channel-advanced-open", "yes");
    expect(readAdvancedOpen()).toBeNull();
  });

  it("degrades to no preference when storage is unavailable", () => {
    const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(readAdvancedOpen()).toBeNull();
    getItem.mockRestore();
    const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => writeAdvancedOpen(true)).not.toThrow();
    setItem.mockRestore();
  });
});
