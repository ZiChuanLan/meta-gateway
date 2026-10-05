import { QueryClient,QueryClientProvider } from "@tanstack/react-query";
import { cleanup,fireEvent,render,screen,waitFor } from "@testing-library/react";
import { afterEach,expect,it,vi } from "vitest";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { OperatorProfilePanel, OperatorUpgradePrompt } from "./OperatorProfilePanel";
import { UpdateChannelPanel } from "./UpdateChannelPanel";
import type { ReactNode } from "react";
afterEach(()=>{cleanup();vi.unstubAllGlobals();localStorage.clear();});
function mount(children:ReactNode){localStorage.setItem("meta-gateway.locale","en");localStorage.setItem("meta-gateway.admin-token","session-token");return render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}})}><I18nProvider><SessionProvider>{children}</SessionProvider></I18nProvider></QueryClientProvider>);}
it("confirms operator token and TOTP before saving, then clears sensitive input",async()=>{
 const fetcher=vi.fn(async (_input:RequestInfo|URL,init?:RequestInit)=>new Response(JSON.stringify({username:init?.method==="POST"?"new-admin":"admin",configured:init?.method==="POST",totp_enabled:true})));
 vi.stubGlobal("fetch",fetcher);mount(<OperatorProfilePanel/>);
 fireEvent.change(await screen.findByLabelText("Username"),{target:{value:"new-admin"}});
 fireEvent.change(screen.getByLabelText("Confirm deployment token (ADMIN_TOKEN)"),{target:{value:"original-secret"}});
 fireEvent.change(screen.getByRole("textbox",{name:/verification|authenticator|2FA|code/i}),{target:{value:"123456"}});
 fireEvent.click(screen.getByRole("button",{name:"Save"}));
 await waitFor(()=>expect(fetcher.mock.calls.some(([,init])=>init?.method==="POST")).toBe(true));
 const call=fetcher.mock.calls.find(([,init])=>init?.method==="POST")!;
 expect(JSON.parse(String(call[1]?.body))).toEqual({username:"new-admin",token:"original-secret",totp_code:"123456"});
 await waitFor(()=>expect(screen.getByLabelText("Confirm deployment token (ADMIN_TOKEN)")).toHaveValue(""));
});
it("makes beta an explicit choice and explains watchtower tag limits",async()=>{
 const fetcher=vi.fn(async (_input:RequestInfo|URL,init?:RequestInit)=>new Response(JSON.stringify({channel:init?.method==="PUT"?"beta":"stable",mode:"watchtower",tracking_tag:"latest"})));
 vi.stubGlobal("fetch",fetcher);mount(<UpdateChannelPanel/>);
 const select=await screen.findByLabelText("Update channel");await waitFor(()=>expect(select).toBeEnabled());
 fireEvent.change(select,{target:{value:"beta"}});
 expect(screen.getByText(/Beta may be unstable/)).toBeInTheDocument();
 expect(screen.getByText(/Watchtower tracks: latest/)).toBeInTheDocument();
 fireEvent.click(screen.getByRole("button",{name:"Save"}));
 await waitFor(()=>expect(fetcher.mock.calls.some(([,init])=>init?.method==="PUT")).toBe(true));
 expect(fetcher.mock.calls.some(([path])=>String(path).includes("self-update/apply"))).toBe(false);
});

it("prompts an unconfigured deployment administrator and allows deferral",async()=>{
 sessionStorage.removeItem("operator-name-dismissed");
 vi.stubGlobal("fetch",vi.fn(async()=>new Response(JSON.stringify({username:"admin",configured:false,totp_enabled:false}))));
 mount(<OperatorUpgradePrompt/>);
 expect(await screen.findByRole("dialog")).toHaveTextContent("Administrator sign-in");
 fireEvent.click(screen.getByRole("button",{name:"Set up later"}));
 expect(screen.queryByRole("dialog")).toBeNull();
 sessionStorage.removeItem("operator-name-dismissed");
});
