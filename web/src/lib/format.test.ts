import { act, renderHook } from "@testing-library/react";
import { beforeEach, expect, it } from "vitest";
import { formatUnitPrice, setCurrency, useCurrency } from "./format";
beforeEach(()=>setCurrency({symbol:"$",rate:1}));
it("preserves nonzero tiny prices and large fractional prices",()=>{
 expect(formatUnitPrice(0.0000001)).toBe("$1e-7");
 expect(formatUnitPrice(1234.56789)).toBe("$1,234.56789");
 expect(formatUnitPrice(0)).toBe("$0.00");
});
it("notifies mounted prices when display currency arrives without altering ledger input",()=>{
 const { result, unmount } = renderHook(()=>useCurrency());
 act(()=>setCurrency({symbol:"¥",rate:7}));
 expect(result.current).toEqual({symbol:"¥",rate:7});
 expect(formatUnitPrice(1)).toBe("¥7.00");
 unmount();
});
