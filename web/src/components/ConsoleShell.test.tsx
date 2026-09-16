import { useState } from "react";
import { Cable, Boxes } from "lucide-react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { ConsoleShell } from "./ConsoleShell";
import { Button, Page, PageActions } from "./ui";

beforeEach(() => localStorage.setItem("meta-gateway.locale", "en"));
afterEach(() => { cleanup(); localStorage.clear(); });

it.each(["classic", "modern"] as const)("closes mobile navigation in the %s theme after choosing a destination", (appearance) => {
  function CurrentLocation() { return <output data-testid="destination">{useLocation().pathname}</output>; }
  render(<I18nProvider><MemoryRouter initialEntries={["/models"]}>
    <ConsoleShell appearance={appearance} sections={[{ label: "Workspace", items: [
      { to: "/channels", label: "Connections", icon: Cable },
      { to: "/models", label: "Models", icon: Boxes },
    ] }]} version="dev" theme="light" onThemeChange={vi.fn()} onSearch={vi.fn()} onDisconnect={vi.fn()} health={{ total: 2, healthy: 1, loading: false }}>
      <CurrentLocation />
    </ConsoleShell>
  </MemoryRouter></I18nProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
  const drawer = screen.getByRole("dialog", { name: "Meta Gateway" });
  fireEvent.click(within(drawer).getByRole("link", { name: "Connections" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(screen.getByTestId("destination")).toHaveTextContent("/channels");
  expect(screen.getByRole("link", { name: "Connections" })).toHaveAttribute("aria-current", "page");
});

it("keeps page header actions bound to the current selection", () => {
  const edit = vi.fn();
  function Catalog() {
    const [selected, setSelected] = useState("gpt-image");
    return <>
      <PageActions><Button onClick={() => edit(selected)}>Edit {selected}</Button></PageActions>
      <button onClick={() => setSelected("gemini")}>Select Gemini</button>
    </>;
  }
  render(<Page title="Models" description="Routing workspace"><Catalog /></Page>);
  expect(screen.getByRole("button", { name: "Edit gpt-image" }).closest("header")).not.toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Select Gemini" }));
  fireEvent.click(screen.getByRole("button", { name: "Edit gemini" }));
  expect(edit).toHaveBeenCalledWith("gemini");
  expect(screen.queryByRole("button", { name: "Edit gpt-image" })).not.toBeInTheDocument();
});
