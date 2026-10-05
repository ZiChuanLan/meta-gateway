import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { ModelsWorkspace } from "./ModelsWorkspace";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it("shares directory search, preserves connect, and clears an empty filter", async () => {
 localStorage.setItem("meta-gateway.locale", "en");
 const requests: string[] = [];
 vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
  requests.push(String(input));
  return new Response(JSON.stringify([{ name: "gpt-test", vendor: "OpenAI", kind: "chat",
   context_window: 32000, input_modalities: "text", output_modalities: "text", candidates: 1 }]));
 }));
 const connect = vi.fn();
 const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const { container } = render(<QueryClientProvider client={qc}><I18nProvider>
  <ModelsWorkspace userID={1} locale="en" canRoute={false} plans={[]} planId={0}
   onPlanChange={vi.fn()} onCreatePlan={vi.fn()} onConnect={connect} />
 </I18nProvider></QueryClientProvider>);
 expect((await screen.findAllByText("gpt-test"))[0]).toBeInTheDocument();
 expect(container.querySelector(".model-directory .models-simple-toolbar")).toBeInTheDocument();
 expect(container.querySelector(".model-directory-table")).toBeInTheDocument();
 expect(container.querySelector(".model-directory-table .model-facts")).toBeInTheDocument();
 expect(container.querySelector(".model-facts")).toHaveTextContent("32.0k");
 expect(container.querySelector(".model-facts")).not.toHaveTextContent("Unknown");
 expect(screen.queryByRole("combobox", {name:"Model family"})).toBeNull();
 expect(container.querySelector(".model-price-input")).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "View gpt-test details" }));
 fireEvent.click(screen.getAllByRole("button", { name: /connect/i })[0]!);
 expect(connect).toHaveBeenCalledWith("gpt-test");
 fireEvent.click(screen.getAllByRole("button", {name:"Close"})[0]!);
 fireEvent.change(container.querySelector(".models-search input")!, { target: { value: "missing" } });
 expect(screen.getByText("No matching models")).toBeInTheDocument();
 fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
 expect(screen.getAllByText("gpt-test")[0]).toBeInTheDocument();
 expect(requests).toContain("/me/model-catalog");
 expect(requests.every((path)=>path.startsWith("/me/"))).toBe(true);
 expect(container.querySelector(".model-card-workspace")).toBeInTheDocument();
});
