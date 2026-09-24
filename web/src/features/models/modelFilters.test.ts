import { describe, expect, it } from "vitest";
import { countActiveModelFilters } from "./modelFilters";

const idle = { group: "", channel: 0, status: "enabled" };

describe("countActiveModelFilters", () => {
  it("counts the list as unfiltered when every control is at its baseline", () => {
    expect(countActiveModelFilters(idle)).toBe(0);
  });

  it("counts each narrowing select once", () => {
    expect(countActiveModelFilters({ ...idle, group: "openai" })).toBe(1);
    expect(countActiveModelFilters({ ...idle, channel: 7 })).toBe(1);
    expect(countActiveModelFilters({ ...idle, status: "all" })).toBe(1);
    expect(countActiveModelFilters({ ...idle, group: "openai", channel: 7 })).toBe(2);
    expect(
      countActiveModelFilters({ group: "openai", channel: 7, status: "disabled" }),
    ).toBe(3);
  });

  it("treats a departure from the default status as a filter, not just group and channel", () => {
    // The list starts on `enabled`, so "all"/"disabled" hide rows the operator
    // would otherwise see — the exact case that used to go unreported.
    expect(countActiveModelFilters({ ...idle, status: "all" })).toBe(1);
    expect(countActiveModelFilters({ ...idle, status: "disabled" })).toBe(1);
  });
});
