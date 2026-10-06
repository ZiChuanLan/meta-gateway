import { afterEach, expect, it, vi } from "vitest";
import { memberKeysSource } from "./MemberKeysSource";
import type { UsageSummary } from "../api/types";

afterEach(() => vi.unstubAllGlobals());

it("uses the account aggregate, preserving totals beyond 500 requests and ledger cost", async () => {
  const summary: UsageSummary = {
    request_count: 1200,
    prompt_tokens: 6000,
    completion_tokens: 4000,
    total_tokens: 10000,
    cost: 25,
  };
  const fetcher = vi.fn(
    async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify(summary)),
  );
  vi.stubGlobal("fetch", fetcher);
  const signal = new AbortController().signal;
  expect(await memberKeysSource.usageSummary(signal)).toEqual(summary);
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0]?.[0]).toBe("/me/usage/summary");
  expect(fetcher.mock.calls[0]?.[1]?.signal).toBe(signal);
});

it("does not turn aggregate failures into zero usage", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response('{"error":"unavailable"}', { status: 503 })),
  );
  await expect(memberKeysSource.usageSummary()).rejects.toMatchObject({ status: 503 });
});

it("preserves cancellation instead of reporting a zero summary", async () => {
  const controller = new AbortController();
  controller.abort();
  const aborted = new DOMException("Cancelled", "AbortError");
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      throw aborted;
    }),
  );
  await expect(memberKeysSource.usageSummary(controller.signal)).rejects.toBe(aborted);
});
