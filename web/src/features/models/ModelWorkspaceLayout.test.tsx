import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { ModelDirectoryTable } from "./ModelDirectoryTable";
import { ModelWorkspaceLayout } from "./ModelWorkspaceLayout";
afterEach(cleanup);
it("opens route details on a card without opening them for plugin-only cards",()=>{
 localStorage.setItem("meta-gateway.locale","en");
 render(<I18nProvider><ModelWorkspaceLayout variant="cards" directory={<ModelDirectoryTable showUpstream={false} rows={[
 {name:"plugin-model",status:"enabled"},
 {name:"route-model",status:"enabled",onClick:vi.fn(),tabIndex:0},
 ]}/>} detail={<p>Route actions</p>}/></I18nProvider>);
 expect(screen.queryByRole("dialog")).toBeNull();
 fireEvent.click(screen.getByText("plugin-model"));expect(screen.queryByRole("dialog")).toBeNull();
 fireEvent.click(screen.getByText("route-model"));expect(screen.getByRole("dialog")).toHaveTextContent("Route actions");
});

it("keeps management in the original persistent list/detail layout by default",()=>{
 render(<I18nProvider><ModelWorkspaceLayout directory={<p>Management list</p>} detail={<p>Routing editor</p>}/></I18nProvider>);
 expect(screen.getByText("Routing editor")).toBeInTheDocument();
 expect(document.querySelector(".models-split .ops-detail-card")).toBeInTheDocument();
 expect(document.querySelector(".model-card-workspace")).toBeNull();
 expect(screen.queryByRole("dialog")).toBeNull();
});
