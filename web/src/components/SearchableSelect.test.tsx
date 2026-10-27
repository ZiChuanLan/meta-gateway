import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { Dialog } from "./ui";
import { SearchableSelect } from "./SearchableSelect";

const options = ["OpenAI", "New API", "Gemini", "Anthropic", "One API"].map((label) => ({
  label,
  value: label,
}));
beforeEach(() => localStorage.setItem("meta-gateway.locale", "en"));
afterEach(() => {
  cleanup();
  localStorage.clear();
});

describe("searchable select", () => {
  it("keeps the parent dialog open when Escape closes the portalled list", () => {
    const close = vi.fn();
    render(
      <I18nProvider>
        <Dialog title="Connection" onClose={close}>
          <SearchableSelect
            options={options}
            value="OpenAI"
            onChange={vi.fn()}
            placeholder="Provider"
          />
        </Dialog>
      </I18nProvider>,
    );
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
      return (
        <SearchableSelect
          options={options}
          value={value}
          onChange={setValue}
          placeholder="Provider"
          allowCustom
        />
      );
    }
    render(
      <I18nProvider>
        <Picker />
      </I18nProvider>,
    );
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
    render(
      <I18nProvider>
        <SearchableSelect
          options={options}
          value="OpenAI"
          onChange={change}
          placeholder="Provider"
        />
      </I18nProvider>,
    );
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

describe("SearchableSelect custom entry", () => {
  function Harness({ onChange }: { onChange: (value: string) => void }) {
    const [value, setValue] = useState("typesafe");
    return (
      <SearchableSelect
        options={[
          { value: "typesafe", label: "TypeSafe", group: "other" },
          { value: "openai-compatible", label: "OpenAI Compatible", group: "core" },
          { value: "__custom__", label: "Custom…", group: "other" },
        ]}
        groups={["core", "other"]}
        value={value}
        onChange={(next) => {
          setValue(next);
          onChange(next);
        }}
        allowCustom
        placeholder="类型"
      />
    );
  }

  // Choosing "Custom…" means "none of the listed families". Keeping the previous
  // type made the option look like it did nothing — the mapping panel opened
  // while the type still read `typesafe`, and the console saved that back.
  it("emits the custom marker instead of the previously selected type", () => {
    const seen: string[] = [];
    render(
      <I18nProvider>
        <Harness onChange={(value) => seen.push(value)} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "TypeSafe" }));
    fireEvent.click(screen.getByText("Custom…"));
    expect(seen.at(-1)).toBe("__custom__");
    // The free-text field starts empty, ready for the real type id.
    expect((screen.getByPlaceholderText("custom-type-id") as HTMLInputElement).value).toBe("");
  });

  it("still lets a typed id become the value", () => {
    const seen: string[] = [];
    render(
      <I18nProvider>
        <Harness onChange={(value) => seen.push(value)} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "TypeSafe" }));
    fireEvent.click(screen.getByText("Custom…"));
    fireEvent.change(screen.getByPlaceholderText("custom-type-id"), {
      target: { value: "my-gateway" },
    });
    expect(seen.at(-1)).toBe("my-gateway");
  });

  // Picking “Custom…” is a detour, not a commitment: without a way back the
  // field stayed a free-text box until the dialog was reopened.
  it("returns from custom entry to the list trigger", () => {
    render(
      <I18nProvider>
        <Harness onChange={() => {}} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "TypeSafe" }));
    fireEvent.click(screen.getByText("Custom…"));
    fireEvent.change(screen.getByPlaceholderText("custom-type-id"), {
      target: { value: "my-gateway" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Back to list" }));
    // The typed value survives as the selection, the list is one click away,
    // and focus went back to the trigger instead of dropping to <body>.
    const trigger = screen.getByRole("button", { name: "my-gateway" });
    expect(trigger).toHaveFocus();
    fireEvent.click(trigger);
    expect(screen.getByRole("listbox")).toBeInTheDocument();
  });

  // A free-text field must not quietly swap a typed prefix for a full name.
  it("commits what was typed when several rows merely match it", () => {
    const seen: string[] = [];
    render(
      <I18nProvider>
        <SearchableSelect
          options={["gpt-4o", "gpt-4o-mini", "claude-3", "gemini-2", "o3"].map((label) => ({
            label,
            value: label,
          }))}
          value=""
          onChange={(value) => seen.push(value)}
          allowCustom
          placeholder="Upstream"
        />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Upstream" }));
    const search = screen.getByRole("textbox", { name: "Upstream" });
    fireEvent.change(search, { target: { value: "gpt-4" } });
    fireEvent.keyDown(search, { key: "Enter" });
    expect(seen.at(-1)).toBe("gpt-4");
  });

  // Empty Enter used to commit the first row, which for an alias field is
  // "blank" — a stray keypress erased the stored target.
  it("does not commit the blank row on Enter in an empty search", () => {
    const seen: string[] = [];
    render(
      <I18nProvider>
        <SearchableSelect
          options={[{ value: "", label: "Blank" }, ...options]}
          value=""
          onChange={(value) => seen.push(value)}
          placeholder="Upstream"
        />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Blank" }));
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Upstream" }), { key: "Enter" });
    expect(seen).toEqual([]);
  });
});
