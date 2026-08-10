import { describe, expect, it } from "vitest";
import {
  UA_PRESETS,
  isValidUserAgent,
  setUAInHeaderOverride,
  uaFromHeaderOverride,
} from "./uaPresets";

describe("uaPresets", () => {
  it("exposes the cc-switch preset list", () => {
    expect(UA_PRESETS).toContain("claude-cli/2.1.161 (external, cli)");
    expect(UA_PRESETS).toContain("Kilo-Code/1.0");
  });

  it("reads the User-Agent out of a header_override JSON", () => {
    expect(
      uaFromHeaderOverride('{"User-Agent": "claude-cli/2.1.161", "X-A": "1"}'),
    ).toBe("claude-cli/2.1.161");
    expect(uaFromHeaderOverride('{"X-A": "1"}')).toBe("");
    expect(uaFromHeaderOverride("not json")).toBe("");
  });

  it("sets the User-Agent and preserves other headers", () => {
    const next = setUAInHeaderOverride(
      '{"X-A": "1", "X-B": "2"}',
      "claude-cli/2.1.161 (external, cli)",
    );
    expect(JSON.parse(next)).toEqual({
      "X-A": "1",
      "X-B": "2",
      "User-Agent": "claude-cli/2.1.161 (external, cli)",
    });
  });

  it("removes the User-Agent key when blank", () => {
    const next = setUAInHeaderOverride(
      '{"User-Agent": "claude-cli/2.1.161", "X-A": "1"}',
      "",
    );
    expect(JSON.parse(next)).toEqual({ "X-A": "1" });
  });

  it("replaces invalid JSON with a fresh override object", () => {
    const next = setUAInHeaderOverride("not json", "Kilo-Code/1.0");
    expect(JSON.parse(next)).toEqual({ "User-Agent": "Kilo-Code/1.0" });
  });

  it("rejects control characters in the UA header", () => {
    expect(isValidUserAgent("claude-cli/2.1.161")).toBe(true);
    expect(isValidUserAgent("claude-cli/2.1.161\nX-Evil: 1")).toBe(false);
  });
});
