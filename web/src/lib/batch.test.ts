import { expect, it } from "vitest";
import { runBatch } from "./batch";
it("bounds requests and retains failed identities without repeating successes", async () => {
  let active = 0, peak = 0;
  const calls: number[] = [];
  const result = await runBatch([1, 2, 3, 4, 5], async (id) => {
    calls.push(id); active++; peak = Math.max(peak, active);
    await Promise.resolve(); active--;
    if (id === 3) throw new Error("upstream unavailable");
  }, 2);
  expect(peak).toBeLessThanOrEqual(2);
  expect(calls).toHaveLength(5);
  expect(result.ok).toBe(4);
  expect(result.failures.map((f) => f.item)).toEqual([3]);
});
