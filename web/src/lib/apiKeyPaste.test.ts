import { describe, expect, it } from "vitest";
import { apiKeyLooksWrong, keyHintFor, splitApiKeys } from "./apiKeyPaste";

describe("splitApiKeys", () => {
  it("reads a single key unchanged", () => {
    expect(splitApiKeys("sk-abc123")).toEqual({ keys: ["sk-abc123"], duplicates: 0 });
  });

  it("splits the paste shapes operators actually use", () => {
    expect(splitApiKeys("sk-a\nsk-b\nsk-c").keys).toEqual(["sk-a", "sk-b", "sk-c"]);
    expect(splitApiKeys("sk-a, sk-b,sk-c").keys).toEqual(["sk-a", "sk-b", "sk-c"]);
    expect(splitApiKeys("sk-a ; sk-b").keys).toEqual(["sk-a", "sk-b"]);
    expect(splitApiKeys("  sk-a  \n\n  sk-b  ").keys).toEqual(["sk-a", "sk-b"]);
  });

  it("drops duplicate values and counts them", () => {
    const result = splitApiKeys("sk-a\nsk-b\nsk-a\nsk-a");
    expect(result.keys).toEqual(["sk-a", "sk-b"]);
    expect(result.duplicates).toBe(2);
  });

  it("strips quotes and an inline note", () => {
    expect(splitApiKeys('"sk-a" sk-b#backup').keys).toEqual(["sk-a", "sk-b"]);
    expect(splitApiKeys("sk-a # first key").keys).toEqual(["sk-a"]);
  });

  it("returns nothing for an empty paste", () => {
    expect(splitApiKeys("   \n  ")).toEqual({ keys: [], duplicates: 0 });
  });
});

describe("key hints", () => {
  it("knows the documented vendor shapes", () => {
    expect(keyHintFor("anthropic")).toBe("sk-ant-…");
    expect(keyHintFor("openrouter")).toBe("sk-or-v1-…");
    expect(keyHintFor("gemini")).toBe("AIza…");
  });

  it("stays silent for relay and unknown types", () => {
    expect(keyHintFor("new-api")).toBe("");
    expect(keyHintFor("anyrouter")).toBe("");
    expect(keyHintFor("custom")).toBe("");
  });

  it("only complains about an obvious prefix mismatch, and never for relay types", () => {
    expect(apiKeyLooksWrong("anthropic", "sk-ant-api03-xyz")).toBe(false);
    expect(apiKeyLooksWrong("anthropic", "abc123")).toBe(true);
    expect(apiKeyLooksWrong("new-api", "abc123")).toBe(false);
    expect(apiKeyLooksWrong("zhipu", "abc.def")).toBe(false);
    expect(apiKeyLooksWrong("anthropic", "")).toBe(false);
  });
});
