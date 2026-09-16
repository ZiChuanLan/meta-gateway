import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { ActionMenu } from "./ActionMenu";
import { Button, ConfirmDialog, Dialog, InfoTip } from "./ui";
import { registerOverlay } from "./overlayStack";

afterEach(() => { cleanup(); localStorage.clear(); });

describe("action menus and overlays", () => {
  it("supports arrow navigation, disabled items and Escape focus restoration", () => {
    const unavailable = vi.fn();
    render(<ActionMenu label="Actions" items={[
      { key: "view", label: "View", onSelect: vi.fn() },
      { key: "edit", label: "Edit", disabled: true, disabledReason: "Saving", onSelect: unavailable },
      { key: "delete", label: "Delete", danger: true, onSelect: vi.fn() },
    ]} />);
    const trigger = screen.getByRole("button", { name: "Actions" });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    const view = screen.getByRole("menuitem", { name: "View" });
    expect(view).toHaveFocus();
    fireEvent.keyDown(view, { key: "ArrowDown" });
    const edit = screen.getByRole("menuitem", { name: "Edit" });
    expect(edit).toHaveFocus();
    expect(edit).toHaveAttribute("aria-disabled", "true");
    expect(edit).toHaveAccessibleDescription("Saving");
    fireEvent.click(edit);
    expect(unavailable).not.toHaveBeenCalled();
    fireEvent.keyDown(edit, { key: "End" });
    expect(screen.getByRole("menuitem", { name: "Delete" })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("keeps a context menu open while its own contents scroll", () => {
    function ContextMenu() {
      const [open, setOpen] = useState(true);
      return <ActionMenu label="Row actions" open={open} onOpenChange={setOpen}
        position={{ top: 120, left: 120 }} items={[{ key: "view", label: "View", onSelect: vi.fn() }]} />;
    }
    render(<ContextMenu />);
    fireEvent.scroll(screen.getByRole("menu"));
    expect(screen.getByRole("menu")).toBeInTheDocument();
    fireEvent.scroll(window);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("closes the menu before its parent dialog on Escape", () => {
    localStorage.setItem("meta-gateway.locale", "en");
    const close = vi.fn();
    render(<I18nProvider><Dialog title="Edit route" onClose={close}>
      <ActionMenu label="Actions" items={[{ key: "view", label: "View", onSelect: vi.fn() }]} />
    </Dialog></I18nProvider>);
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    fireEvent.keyDown(screen.getByRole("menuitem"), { key: "Escape" });
    expect(close).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(document.body.style.overflow).toBe("hidden");
    fireEvent.keyDown(window, { key: "Escape", isComposing: true });
    expect(close).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(close).toHaveBeenCalledOnce();
  });

  it("does not steal focus from an outside control when dismissed", () => {
    render(<><ActionMenu label="Actions" items={[{ key: "view", label: "View", onSelect: vi.fn() }]} /><button>Outside</button></>);
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    const outside = screen.getByRole("button", { name: "Outside" });
    outside.focus();
    fireEvent.pointerDown(outside);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(outside).toHaveFocus();
  });

  it("keeps scrolling locked when an older modal closes first", () => {
    document.body.style.overflow = "auto";
    const first = registerOverlay(vi.fn());
    const second = registerOverlay(vi.fn());
    first();
    expect(document.body.style.overflow).toBe("hidden");
    second();
    expect(document.body.style.overflow).toBe("auto");
    document.body.style.overflow = "";
  });

  it("keeps a pending destructive action visible until it settles", () => {
    localStorage.setItem("meta-gateway.locale", "en");
    const close = vi.fn();
    const confirm = vi.fn();
    render(<I18nProvider><ConfirmDialog title="Delete route" message="Confirm deletion" pending onClose={close} onConfirm={confirm} /></I18nProvider>);
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.mouseDown(screen.getByRole("dialog").parentElement!);
    expect(close).not.toHaveBeenCalled();
    expect(confirm).not.toHaveBeenCalled();
  });

  it("does not implicitly submit forms from ordinary buttons", () => {
    const submit = vi.fn();
    render(<form onSubmit={(event) => { event.preventDefault(); submit(); }}><Button>More</Button><Button type="submit">Save</Button></form>);
    fireEvent.click(screen.getByRole("button", { name: "More" }));
    expect(submit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(submit).toHaveBeenCalledOnce();
  });

  it("dismisses a visible field hint before its containing editor", () => {
    const close = vi.fn();
    render(<I18nProvider><Dialog title="Edit route" onClose={close}>
      <InfoTip label="Optional route configuration" />
    </Dialog></I18nProvider>);
    fireEvent.focus(screen.getByRole("img", { name: "Optional route configuration" }));
    expect(screen.getByRole("tooltip")).toBeInTheDocument();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
    expect(close).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(close).toHaveBeenCalledOnce();
  });
});
