import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { ModelMetadataDialog } from "./ModelMetadataDialog";
import type { ModelMetadata } from "../../api/types";
const base: ModelMetadata = {
  model_name: "test",
  context_window: 1000,
  input_modalities: "text",
  output_modalities: "text",
  supports_thinking: -1,
  vendor: "Demo",
  notes: "",
};
beforeEach(() => localStorage.setItem("meta-gateway.locale", "en"));
afterEach(cleanup);
it("preserves untouched legacy rule text on metadata saves", () => {
  const save = vi.fn();
  const value = {
    ...base,
    price_tiers: " { legacy-invalid } ",
    price_schedule: ' [ { "days": [], "from_hour":22, "to_hour":6, "multiplier":0.001 } ] ',
  };
  render(
    <I18nProvider>
      <ModelMetadataDialog value={value} pending={false} onClose={vi.fn()} onSave={save} />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith(
    expect.objectContaining({
      price_tiers: value.price_tiers,
      price_schedule: value.price_schedule,
    }),
  );
});
it("blocks save after a tier price is cleared instead of making it free", () => {
  const save = vi.fn();
  render(
    <I18nProvider>
      <ModelMetadataDialog
        value={{
          ...base,
          price_tiers:
            '[{"max_prompt_tokens":0,"prompt":1,"completion":2,"cache":0,"per_request":0}]',
        }}
        pending={false}
        onClose={vi.fn()}
        onSave={save}
      />
    </I18nProvider>,
  );
  const input = document.querySelector<HTMLInputElement>(
    ".pricing-row-tier label:nth-child(2) input",
  );
  expect(input).toBeTruthy();
  fireEvent.change(input!, { target: { value: "" } });
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  expect(save).not.toHaveBeenCalled();
});

it("ignores completely blank new rules and keeps explicit zero tiers", () => {
  const save = vi.fn();
  render(
    <I18nProvider>
      <ModelMetadataDialog value={base} pending={false} onClose={vi.fn()} onSave={save} />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Add tier" }));
  fireEvent.click(screen.getByRole("button", { name: "Add window" }));
  expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenLastCalledWith(
    expect.objectContaining({ price_tiers: "", price_schedule: "" }),
  );
  const inputs = document.querySelectorAll<HTMLInputElement>(".pricing-row-tier input");
  fireEvent.change(inputs[0]!, { target: { value: "0" } });
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  inputs.forEach((input) => fireEvent.change(input, { target: { value: "0" } }));
  expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(JSON.parse(save.mock.calls.at(-1)![0].price_tiers)).toEqual([
    { max_prompt_tokens: 0, prompt: 0, completion: 0, cache: 0, per_request: 0 },
  ]);
});
