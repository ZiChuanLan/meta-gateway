import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { useUnsavedChanges } from "./unsavedChanges";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
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
  localStorage.setItem("meta-gateway.locale", "en");
  render(
    <I18nProvider>
      <Harness dirty={dirty} />
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
