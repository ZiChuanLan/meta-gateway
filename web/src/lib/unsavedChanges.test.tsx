import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { leavesDirtyZone, useUnsavedChanges } from "./unsavedChanges";

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
});

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
});

let allowed = true;
function Harness({ dirty }: { dirty: boolean }) {
  const guard = useUnsavedChanges(dirty);
  return (
    <button
      type="button"
      onClick={() => {
        allowed = guard();
      }}
    >
      leave
    </button>
  );
}

function mount(dirty: boolean) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/settings"]}>
        <Harness dirty={dirty} />
      </MemoryRouter>
    </I18nProvider>,
  );
}

/** A cancelable page unload; `defaultPrevented` is the browser contract. */
function unload() {
  const event = new Event("beforeunload", { cancelable: true });
  fireEvent(window, event);
  return event;
}

it("arms the browser prompt only while there are unsaved edits", () => {
  mount(true);
  expect(unload().defaultPrevented).toBe(true);
  cleanup();
  mount(false);
  expect(unload().defaultPrevented).toBe(false);
});

it("asks before letting an in-app navigation discard the draft", () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(true);
  fireEvent.click(screen.getByRole("button", { name: "leave" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(allowed).toBe(true);

  confirm.mockReturnValue(false);
  fireEvent.click(screen.getByRole("button", { name: "leave" }));
  expect(allowed).toBe(false);

  // Clean state: no prompt at all, not a prompt that happens to be accepted.
  cleanup();
  confirm.mockClear();
  mount(false);
  fireEvent.click(screen.getByRole("button", { name: "leave" }));
  expect(confirm).not.toHaveBeenCalled();
  expect(allowed).toBe(true);
});

it("registers a dirty surface against its pathname, and drops it on unmount", () => {
  // This is the seam the router guard reads: leaving the dirty page blocks, a
  // move within it (a tab switch the page guards itself) does not, and a move
  // from any other page is not this zone's business.
  expect(leavesDirtyZone("/settings", "/elsewhere")).toBe(false);
  const view = mount(true);
  expect(leavesDirtyZone("/settings", "/settings")).toBe(false);
  expect(leavesDirtyZone("/settings", "/elsewhere")).toBe(true);
  // A dirty page that is mounted but off-screen must not freeze navigation
  // from the page the operator is actually looking at.
  expect(leavesDirtyZone("/somewhere-else", "/third")).toBe(false);
  view.unmount();
  expect(leavesDirtyZone("/settings", "/elsewhere")).toBe(false);
});

it("does not register a surface that has nothing unsaved", () => {
  mount(false);
  expect(leavesDirtyZone("/settings", "/elsewhere")).toBe(false);
});
