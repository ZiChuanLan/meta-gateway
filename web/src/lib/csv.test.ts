import { expect, it } from "vitest";
import { toCSV } from "./csv";
it("neutralizes spreadsheet formulas in untrusted text, not numeric cells", () => {
  expect(toCSV([["=1+1", "+SUM(A1)", "@SUM(A1)", "-2", -2, 0]])).toBe(
    "'=1+1,'+SUM(A1),'@SUM(A1),'-2,-2,0",
  );
  expect(toCSV([["  =1+1", "\t=1+1"]])).toBe("'  =1+1,'\t=1+1");
});
it("preserves CSV quoting and ordinary values", () => {
  expect(toCSV([["a,b", 'say "yes"', null, true, "模型"]])).toBe('"a,b","say ""yes""",,true,模型');
  expect(toCSV([["a\nb"]])).toBe('"a\nb"');
});
