import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { MemberDialog } from "./MemberDialog";
afterEach(() => {
  cleanup();
  localStorage.clear();
});
const member = {
  id: 17,
  channel_id: 5,
  priority: 9,
  weight: 30,
  enabled: true,
  manual_override: false,
  price_prompt_per_1k: 1,
};
function mount() {
  localStorage.setItem("meta-gateway.locale", "en");
  const save = vi.fn();
  render(
    <I18nProvider>
      <MemberDialog
        value={member}
        channels={[]}
        groups={[]}
        pending={false}
        error={null}
        onClose={() => {}}
        onSave={save}
      />
    </I18nProvider>,
  );
  return save;
}
it("changing price does not freeze inherited weights or resend enabled/health state", () => {
  const save = mount();
  const input = document.querySelector<HTMLInputElement>(".form-grid input[min='0'][step='any']")!;
  expect(input).toBeTruthy();
  fireEvent.change(input, { target: { value: "2" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith({ id: 17, price_prompt_per_1k: 2 });
});
it("changing weight explicitly opts into a member weight override", () => {
  const save = mount();
  const inputs = document.querySelectorAll<HTMLInputElement>(".form-grid input");
  fireEvent.change(inputs[1]!, { target: { value: "55" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith({ id: 17, weight: 55, manual_override: true });
});
