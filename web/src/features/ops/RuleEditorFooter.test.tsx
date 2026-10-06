import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import { I18nProvider } from "../../i18n";
import { RuleEditorFooter } from "./RuleEditorFooter";

afterEach(() => {
  cleanup();
  localStorage.clear();
});

function mount(props: Partial<ComponentProps<typeof RuleEditorFooter>> = {}) {
  localStorage.setItem("meta-gateway.locale", "en");
  const onCancel = vi.fn();
  const onSave = vi.fn();
  render(
    <I18nProvider>
      <RuleEditorFooter pending={false} onCancel={onCancel} onSave={onSave} {...props} />
    </I18nProvider>,
  );
  return { onCancel, onSave };
}

it("submits the form and cancels through the shared footer", () => {
  const { onCancel, onSave } = mount();
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(onSave).toHaveBeenCalledTimes(1);
  expect(onCancel).toHaveBeenCalledTimes(1);
});

// The shared Dialog blocks closing while busy; the footer has to block the
// second submission itself, or a slow write turns one rule into two.
it("blocks a second submission while the write is in flight", () => {
  const { onCancel, onSave } = mount({ pending: true });
  const save = screen.getByRole("button", { name: "Working…" });
  const cancel = screen.getByRole("button", { name: "Cancel" });
  expect(save).toBeDisabled();
  expect(cancel).toBeDisabled();
  fireEvent.click(save);
  fireEvent.click(cancel);
  expect(onSave).not.toHaveBeenCalled();
  expect(onCancel).not.toHaveBeenCalled();
});

it("surfaces the server's failure message above the buttons", () => {
  mount({ error: new Error("rule name already exists") });
  expect(screen.getByText("rule name already exists")).toBeInTheDocument();
});
