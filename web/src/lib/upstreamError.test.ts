import { describe, expect, it } from "vitest";
import { upstreamMessage } from "./upstreamError";

describe("upstreamMessage", () => {
  it("shows the message an OpenAI-compatible upstream wrapped in error", () => {
    // grok2api's Web plane rejection, verbatim from production.
    expect(
      upstreamMessage({
        error: {
          code: "upstream_unavailable",
          message:
            "Grok Web 媒体上游返回 429: 8: Too many requests. Wait a moment and try again.",
        },
      }),
    ).toBe(
      "Grok Web 媒体上游返回 429: 8: Too many requests. Wait a moment and try again.",
    );
  });

  it("shows the message for an exhausted upstream quota", () => {
    // Same upstream, Console plane — the other half of the 429 pair.
    expect(
      upstreamMessage({
        error: { code: "upstream_quota_exhausted", message: "上游账号额度等待恢复" },
      }),
    ).toBe("上游账号额度等待恢复");
  });

  it("trims the message so padding never leaks into the panel", () => {
    expect(upstreamMessage({ error: { message: "  spaced out  " } })).toBe(
      "spaced out",
    );
  });

  it("accepts a bare error string", () => {
    expect(upstreamMessage({ error: "boom" })).toBe("boom");
  });

  it("accepts a plain-text body", () => {
    expect(upstreamMessage("  error code: 502  ")).toBe("error code: 502");
  });

  it("falls back to the raw body when the shape is unknown", () => {
    expect(upstreamMessage({ unexpected: 1 })).toBe('{"unexpected":1}');
  });

  it("returns nothing when there is no body to read", () => {
    expect(upstreamMessage(null)).toBe("");
    expect(upstreamMessage(undefined)).toBe("");
    expect(upstreamMessage(42)).toBe("");
  });

  it("ignores a non-string message field", () => {
    expect(upstreamMessage({ error: { message: 7 } })).toBe('{"error":{"message":7}}');
  });

  it("caps a long body instead of rendering it whole", () => {
    expect(upstreamMessage("x".repeat(1000))).toHaveLength(400);
  });
});
