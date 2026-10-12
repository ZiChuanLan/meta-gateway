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
it("capability edits preserve legacy prices and rule text without a second pricing editor", () => {
  const save = vi.fn();
  const value = {
    ...base,
    price_prompt_per_1k: 1,
    price_tiers: " { legacy-invalid } ",
    price_schedule: "[]",
  };
  render(
    <I18nProvider>
      <ModelMetadataDialog value={value} pending={false} onClose={vi.fn()} onSave={save} />
    </I18nProvider>,
  );
  expect(screen.queryByRole("button", { name: "Add tier" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Vendor"), { target: { value: "Updated" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith({ ...base, vendor: "Updated" });
});
