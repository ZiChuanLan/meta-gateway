import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * The type floor.
 *
 * Every size below 11px was removed in one sweep (148 declarations across seven
 * sheets): 8px and 9px micro-labels and 10px metadata were the single biggest
 * reason the console read as a dense admin template, and each one was
 * individually defensible — which is exactly why the floor needs a test rather
 * than a convention. 11px is --text-xs.
 */
const ROOT = join(__dirname, "..");
const MIN_PX = 11;

const sheets = (dir: string): string[] =>
  readdirSync(dir).flatMap((entry) => {
    const abs = join(dir, entry);
    if (statSync(abs).isDirectory()) return sheets(abs);
    return abs.endsWith(".css") ? [abs] : [];
  });

describe("type floor", () => {
  it("no font-size below 11px anywhere in the console's CSS", () => {
    const offenders: string[] = [];
    for (const file of sheets(ROOT)) {
      // The token sheet is where the scale is declared, not where it is applied.
      if (file.endsWith("tokens.css")) continue;
      const lines = readFileSync(file, "utf8").split("\n");
      lines.forEach((line, i) => {
        for (const m of line.matchAll(/font-size:\s*([\d.]+)px/g)) {
          if (Number(m[1] ?? 0) < MIN_PX) offenders.push(`${file}:${i + 1}: ${line.trim().slice(0, 70)}`);
        }
        // font: 600 10px/1.4 var(--font)
        for (const m of line.matchAll(/\bfont:[^;{}]*?\s([\d.]+)px\s*\//g)) {
          if (Number(m[1] ?? 0) < MIN_PX) offenders.push(`${file}:${i + 1}: ${line.trim().slice(0, 70)}`);
        }
      });
    }
    expect(offenders).toEqual([]);
  });
});
