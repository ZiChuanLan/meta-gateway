import { describe, expect, it } from "vitest";
import { logCostUsd } from "./format";

describe("logCostUsd", () => {
  it("bills cache-read at prompt price when no cache price configured", () => {
    const cost = logCostUsd({
      promptTokens: 1000,
      completionTokens: 500,
      cacheReadTokens: 300,
      pricePromptPer1k: 1,
      priceCompletionPer1k: 2,
    });
    // uncached 700/1000*1 + cache 300/1000*1 + completion 500/1000*2
    expect(cost).toBeCloseTo(0.7 + 0.3 + 1.0);
  });

  it("bills cache-read at its own price when configured", () => {
    const cost = logCostUsd({
      promptTokens: 1000,
      completionTokens: 0,
      cacheReadTokens: 400,
      pricePromptPer1k: 2,
      priceCompletionPer1k: 0,
      priceCachePer1k: 0.1,
    });
    // uncached 600/1000*2 + cache 400/1000*0.1
    expect(cost).toBeCloseTo(1.2 + 0.04);
  });

  it("full cache hit costs only the cache price", () => {
    const cost = logCostUsd({
      promptTokens: 800,
      completionTokens: 200,
      cacheReadTokens: 800,
      pricePromptPer1k: 3,
      priceCompletionPer1k: 6,
      priceCachePer1k: 0.3,
    });
    // 0.8*0.3 + 0.2*6
    expect(cost).toBeCloseTo(0.24 + 1.2);
  });

  it("returns null when no pricing configured", () => {
    expect(
      logCostUsd({ promptTokens: 100, completionTokens: 50, cacheReadTokens: 0 }),
    ).toBeNull();
  });

  it("returns null when cost is zero", () => {
    expect(
      logCostUsd({
        promptTokens: 0,
        completionTokens: 0,
        cacheReadTokens: 0,
        pricePromptPer1k: 1,
        priceCompletionPer1k: 1,
      }),
    ).toBeNull();
  });

  it("clamps negative token counts", () => {
    const cost = logCostUsd({
      promptTokens: -5,
      completionTokens: 1000,
      cacheReadTokens: 99999,
      pricePromptPer1k: 1,
      priceCompletionPer1k: 2,
    });
    // uncached 0 (cache >= prompt) + cache... cache clamped to prompt? no:
    // cache = 99999, prompt = 0 → uncached = 0, cache billed at prompt price
    expect(cost).toBeCloseTo(0 + 2.0);
  });
});
