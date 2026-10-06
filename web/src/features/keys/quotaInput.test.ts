import { expect, it } from "vitest";
import { parseQuotaInput } from "./quotaInput";

it("distinguishes explicit unlimited input from invalid amounts", () => {
  for (const raw of ["", " ", "0"]) expect(parseQuotaInput(raw)).toBe(0);
  for (const raw of ["-1", "NaN", "Infinity", "1e999", "invalid"]) {
    expect(parseQuotaInput(raw)).toBeNull();
  }
  expect(parseQuotaInput("1.25")).toBe(1.25);
});

it("requires token quotas to be safe whole numbers", () => {
  expect(parseQuotaInput("123", true)).toBe(123);
  expect(parseQuotaInput("1.5", true)).toBeNull();
  expect(parseQuotaInput("9007199254740992", true)).toBeNull();
});
