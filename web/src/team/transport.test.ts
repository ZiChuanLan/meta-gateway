import {afterEach,describe,expect,it,vi} from "vitest";
import {accountRequest,setTeamCSRF,COOKIE_SESSION} from "./transport";
// This is a transport boundary test; the admin client is tested in api/client.test.ts.
afterEach(()=>{vi.restoreAllMocks();setTeamCSRF("")});
describe("account transport",()=>{
  it("uses same-origin cookies and CSRF, never an inference or admin bearer",async()=>{
    setTeamCSRF("csrf-only");
    const fetch=vi.spyOn(globalThis,"fetch").mockResolvedValue(new Response('{"ok":true}'));
    await accountRequest("/me/keys",{method:"POST",body:'{"name":"app"}'});
    const init=fetch.mock.calls[0]![1]!;
    expect(init.credentials).toBe("same-origin");
    expect(new Headers(init.headers).get("X-Meta-CSRF")).toBe("csrf-only");
    expect(new Headers(init.headers).has("Authorization")).toBe(false);
    expect(JSON.stringify(init)).not.toContain(COOKIE_SESSION);
  });
  it("notifies the UI when a server-side session is revoked",async()=>{
    vi.spyOn(globalThis,"fetch").mockResolvedValue(new Response('{"error":"account_login_required"}',{status:401}));
    const listener=vi.fn();window.addEventListener("meta-team-expired",listener);
    await expect(accountRequest("/me/keys")).rejects.toThrow("account_login_required");
    expect(listener).toHaveBeenCalledOnce();
    window.removeEventListener("meta-team-expired",listener);
  });
});

it("preserves cancellation instead of reporting a network failure",async()=>{
 const abort=new DOMException("Cancelled","AbortError");
 vi.spyOn(globalThis,"fetch").mockRejectedValue(abort);
 await expect(accountRequest("/me/models")).rejects.toBe(abort);
});
it("recognizes expiry on the account root too",async()=>{
 vi.spyOn(globalThis,"fetch").mockResolvedValue(new Response('{"error":"account_login_required"}',{status:401}));
 const listener=vi.fn();window.addEventListener("meta-team-expired",listener);
 try{await expect(accountRequest("/me")).rejects.toThrow();expect(listener).toHaveBeenCalledOnce();}
 finally{window.removeEventListener("meta-team-expired",listener);}
});

it("ignores a stale unauthorized response after a new session is adopted",async()=>{
 let finish!:(response:Response)=>void;
 vi.spyOn(globalThis,"fetch").mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));
 setTeamCSRF("old-session");
 const expired=vi.fn();window.addEventListener("meta-team-expired",expired);
 try{
  const pending=accountRequest("/me/keys");
  setTeamCSRF("new-session");
  finish(new Response('{"error":"account_login_required"}',{status:401}));
  await expect(pending).rejects.toMatchObject({name:"AbortError"});
  expect(expired).not.toHaveBeenCalled();
 }finally{window.removeEventListener("meta-team-expired",expired);}
});
it("does not deliver old account data to a new session",async()=>{
 let finish!:(response:Response)=>void;
 vi.spyOn(globalThis,"fetch").mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));
 setTeamCSRF("old");const pending=accountRequest("/me/requests");setTeamCSRF("new");
 finish(new Response('[{"request_id":"old-account"}]'));
 await expect(pending).rejects.toMatchObject({name:"AbortError"});
});
