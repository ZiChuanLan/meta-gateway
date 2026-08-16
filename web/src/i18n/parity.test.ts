import { describe, expect, it } from "vitest";

import { en } from "./en";
import { zh } from "./zh";

// Both dictionaries must stay in lockstep: a key only in one locale silently
// falls back to English (or renders the raw key) for the other locale's users.
describe("i18n dictionary parity", () => {
  const enKeys = Object.keys(en).sort();
  const zhKeys = Object.keys(zh).sort();

  it("zh covers every en key", () => {
    const missing = enKeys.filter((key) => !(key in zh));
    expect(missing).toEqual([]);
  });

  it("en covers every zh key", () => {
    const missing = zhKeys.filter((key) => !(key in en));
    expect(missing).toEqual([]);
  });

  it("no empty translations", () => {
    const emptyEn = enKeys.filter((key) => !en[key]?.trim());
    const emptyZh = zhKeys.filter((key) => !zh[key]?.trim());
    expect({ emptyEn, emptyZh }).toEqual({ emptyEn: [], emptyZh: [] });
  });
});
