import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { Dialog } from "./ui";
import { SearchableSelect } from "./SearchableSelect";

const options = ["OpenAI", "New API", "Gemini", "Anthropic", "One API"].map((label) => ({ label, value: label }));
beforeEach(() => localStorage.setItem("meta-gateway.locale", "en"));
afterEach(() => { cleanup(); localStorage.clear(); });

describe("searchable select", () => {
  it("keeps the parent dialog open when Escape closes the portalled list", () => {
    const close = vi.fn();
    render(<I18nProvider><Dialog title="Connection" onClose={close}>
      <SearchableSelect options={options} value="OpenAI" onChange={vi.fn()} placeholder="Provider" />
    </Dialog></I18nProvider>);
    const trigger = screen.getByRole("button", { name: "OpenAI" });
    fireEvent.click(trigger);
    const search = screen.getByRole("textbox", { name: "Provider" });
    expect(search).toHaveFocus();
    fireEvent.scroll(screen.getByRole("listbox"));
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    fireEvent.keyDown(search, { key: "Escape" });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(close).not.toHaveBeenCalled();
    expect(trigger).toHaveFocus();
  });

  it("keeps search mounted during filtering and preserves a custom type", () => {
    function Picker() {
      const [value, setValue] = useState("OpenAI");
      return <SearchableSelect options={options} value={value} onChange={setValue} placeholder="Provider" allowCustom />;
    }
    render(<I18nProvider><Picker /></I18nProvider>);
    fireEvent.click(screen.getByRole("button", { name: "OpenAI" }));
    const search = screen.getByRole("textbox", { name: "Provider" });
    fireEvent.change(search, { target: { value: "my-provider" } });
    expect(search).toHaveFocus();
    fireEvent.keyDown(search, { key: "Enter", isComposing: true });
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("option", { name: "Custom type: my-provider" }));
    expect(screen.getByPlaceholderText("custom-type-id")).toHaveValue("my-provider");
    expect(screen.getByPlaceholderText("custom-type-id")).toHaveFocus();
  });

  it("moves from search to options with the keyboard and restores the trigger", () => {
    const change = vi.fn();
    render(<I18nProvider><SearchableSelect options={options} value="OpenAI" onChange={change} placeholder="Provider" /></I18nProvider>);
    const trigger = screen.getByRole("button", { name: "OpenAI" });
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Provider" }), { key: "ArrowDown" });
    expect(screen.getByRole("option", { name: "OpenAI" })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: "ArrowDown" });
    const next = screen.getByRole("option", { name: "New API" });
    expect(next).toHaveFocus();
    fireEvent.click(next);
    expect(change).toHaveBeenCalledWith("New API");
    expect(trigger).toHaveFocus();
  });
});
