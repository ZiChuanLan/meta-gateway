import { afterEach, expect, it, vi } from "vitest";
import { memberLogsSource } from "./MemberLogsSource";

afterEach(() => vi.unstubAllGlobals());
it("preserves member-visible request detail without requesting admin data", async () => {
 const fetcher = vi.fn(async (_input: RequestInfo | URL) => new Response(JSON.stringify([{
  request_id: "req-1", key_id: 7, model: "gpt-test", status: 200,
  latency_ms: 400, tokens: 15, prompt_tokens: 10, completion_tokens: 5,
  cost: 0.03, client_family: "Codex", path: "/v1/responses",
  created_at: "2026-10-05T00:00:00Z", attempts: 1,
 }])));
 vi.stubGlobal("fetch", fetcher);
 const rows = await memberLogsSource.logs({ downstream_key_id: 7, model: "gpt-test" });
 expect(String(fetcher.mock.calls[0]?.[0])).toContain("/me/requests?");
 expect(rows[0]).toMatchObject({ downstream_key_id: 7, prompt_tokens: 10,
  completion_tokens: 5, client_family: "Codex", path: "/v1/responses", cost: 0.03,
  channel_id: 0, upstream_url: "" });
});

it("forwards histogram time bounds to the account endpoint",async()=>{
 const fetcher=vi.fn(async (_input: RequestInfo | URL)=>new Response(JSON.stringify({buckets:[],total:0})));
 vi.stubGlobal("fetch",fetcher);
 await memberLogsSource.latencyHistogram!(1000,undefined,{since:"2026-10-05T00:00:00Z",until:"2026-10-05T01:00:00Z"});
 const url=new URL(String(fetcher.mock.calls[0]?.[0]),"https://example.test");
 expect(url.pathname).toBe("/me/requests/latency-histogram");expect(url.searchParams.get("sample")).toBe("1000");expect(url.searchParams.get("since")).toBe("2026-10-05T00:00:00Z");
});
