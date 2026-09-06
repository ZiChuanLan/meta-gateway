import { describe, expect, it } from "vitest";

import { presetOfCron } from "./schedulePresets";

describe("presetOfCron", () => {
  it("maps known preset crons back to their ids", () => {
    expect(presetOfCron("0 * * * *")).toBe("hourly");
    expect(presetOfCron("0 */6 * * *")).toBe("every6h");
    expect(presetOfCron("0 8 * * *")).toBe("daily");
  });

  it("treats anything else as custom", () => {
    expect(presetOfCron("0 3 * * *")).toBe("custom");
    expect(presetOfCron("30 2 * * 1")).toBe("custom");
  });

  it("reads an empty cron as off", () => {
    expect(presetOfCron("")).toBe("off");
    expect(presetOfCron("   ")).toBe("off");
  });
});
