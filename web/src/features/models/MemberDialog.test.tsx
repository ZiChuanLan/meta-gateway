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
it("routing edits never resend legacy price or health values", () => {
  const save = mount();
  expect(screen.queryByLabelText(/Input price/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /Upstream model name/ }));
  fireEvent.click(screen.getByRole("option", { name: "Custom model name" }));
  fireEvent.change(screen.getByPlaceholderText("Blank forwards the route name as-is"), {
    target: { value: "actual-model" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith({
    id: 17,
    mapping_json: JSON.stringify({ real: "actual-model" }),
  });
});
it("changing weight explicitly opts into a member weight override", () => {
  const save = mount();
  const inputs = document.querySelectorAll<HTMLInputElement>(".form-grid input");
  fireEvent.change(inputs[1]!, { target: { value: "55" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith({ id: 17, weight: 55, manual_override: true });
});
// Every choice the dropdown offers, in order: the blank target, one row per
// usable upstream model, then the free-text escape hatch.
function upstreamOptionLabels() {
  return [...document.querySelectorAll<HTMLElement>('[role="option"]')].map((option) =>
    option.textContent?.trim(),
  );
}
const blankTarget = "Blank (forward the route name)";
it("offers the selected channel's own models as upstream name suggestions", () => {
  localStorage.setItem("meta-gateway.locale", "en");
  const save = vi.fn();
  render(
    <I18nProvider>
      <MemberDialog
        value={{}}
        channels={[{ id: 5, name: "WONG", models_csv: "gpt-4o, gpt-4o-mini, gpt-*" }]}
        groups={[]}
        pending={false}
        error={null}
        onClose={() => {}}
        onSave={save}
      />
    </I18nProvider>,
  );
  fireEvent.change(screen.getByLabelText(/Channel/), { target: { value: "5" } });
  fireEvent.click(screen.getByRole("button", { name: /Upstream model name/ }));
  // The wildcard is a route matcher, never a callable upstream model name.
  expect(upstreamOptionLabels()).toEqual([
    blankTarget,
    "gpt-4o",
    "gpt-4o-mini",
    "Custom model name",
  ]);
  // Picking one must reach the saved payload, not just the trigger.
  fireEvent.click(screen.getByRole("option", { name: "gpt-4o-mini" }));
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith({
    channel_id: 5,
    mapping_json: JSON.stringify({ real: "gpt-4o-mini" }),
  });
});
it("suggests models from the member's own channel while editing", () => {
  localStorage.setItem("meta-gateway.locale", "en");
  render(
    <I18nProvider>
      <MemberDialog
        value={{ ...member, channel_id: 5 }}
        channels={[{ id: 5, name: "WONG", models_csv: "gpt-4o,?pattern,gpt-4o" }]}
        groups={[]}
        pending={false}
        error={null}
        onClose={() => {}}
        onSave={() => {}}
      />
    </I18nProvider>,
  );
  // No channel picker while editing, and duplicates in a hand-imported list
  // must not repeat a React key.
  expect(screen.queryByLabelText(/Channel/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /Upstream model name/ }));
  expect(upstreamOptionLabels()).toEqual([blankTarget, "gpt-4o", "Custom model name"]);
});
it("resets the upstream target when the channel changes", () => {
  localStorage.setItem("meta-gateway.locale", "en");
  const save = vi.fn();
  render(
    <I18nProvider>
      <MemberDialog
        value={{}}
        channels={[
          { id: 5, name: "WONG", models_csv: "gpt-4o" },
          { id: 6, name: "OTHER", models_csv: "claude-x" },
        ]}
        groups={[]}
        pending={false}
        error={null}
        onClose={() => {}}
        onSave={save}
      />
    </I18nProvider>,
  );
  fireEvent.change(screen.getByLabelText(/Channel/), { target: { value: "5" } });
  fireEvent.click(screen.getByRole("button", { name: /Upstream model name/ }));
  fireEvent.click(screen.getByRole("option", { name: "gpt-4o" }));
  expect(screen.getByRole("button", { name: /Upstream model name/ })).toHaveTextContent("gpt-4o");

  fireEvent.change(screen.getByLabelText(/Channel/), { target: { value: "6" } });
  // The previous channel's target is gone, and the options are the new one's.
  expect(screen.getByRole("button", { name: /Upstream model name/ })).not.toHaveTextContent(
    "gpt-4o",
  );
  fireEvent.click(screen.getByRole("button", { name: /Upstream model name/ }));
  expect(upstreamOptionLabels()).toEqual([blankTarget, "claude-x", "Custom model name"]);
  // Save without touching the picker: if switching left the old mapping in
  // place, this payload is where it shows up.
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith({ channel_id: 6, mapping_json: "" });
});
it("abandons a custom upstream name when the channel changes", () => {
  localStorage.setItem("meta-gateway.locale", "en");
  const save = vi.fn();
  render(
    <I18nProvider>
      <MemberDialog
        value={{}}
        channels={[
          { id: 5, name: "WONG", models_csv: "gpt-4o" },
          { id: 6, name: "OTHER", models_csv: "claude-x" },
        ]}
        groups={[]}
        pending={false}
        error={null}
        onClose={() => {}}
        onSave={save}
      />
    </I18nProvider>,
  );
  fireEvent.change(screen.getByLabelText(/Channel/), { target: { value: "5" } });
  fireEvent.click(screen.getByRole("button", { name: /Upstream model name/ }));
  fireEvent.click(screen.getByRole("option", { name: "Custom model name" }));
  const customField = () => screen.queryByPlaceholderText(/Blank forwards the route name/);
  fireEvent.change(customField()!, { target: { value: "hand-typed-name" } });
  expect(customField()).toBeInTheDocument();

  fireEvent.change(screen.getByLabelText(/Channel/), { target: { value: "6" } });
  // Back to an empty dropdown: no leftover free-text box, no stale name.
  expect(customField()).toBeNull();
  const trigger = screen.getByRole("button", { name: /Upstream model name/ });
  expect(trigger).not.toHaveTextContent("hand-typed-name");
  fireEvent.click(trigger);
  expect(upstreamOptionLabels()).toEqual([blankTarget, "claude-x", "Custom model name"]);
  fireEvent.keyDown(document, { key: "Escape" });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith({ channel_id: 6, mapping_json: "" });
});
// Clicking the custom row is a promise to type, not a choice of "nothing".
it("keeps the picked upstream name when the custom row is clicked by mistake", async () => {
  localStorage.setItem("meta-gateway.locale", "en");
  const save = vi.fn();
  render(
    <I18nProvider>
      <MemberDialog
        value={{}}
        channels={[{ id: 5, name: "WONG", models_csv: "gpt-4o" }]}
        groups={[]}
        pending={false}
        error={null}
        onClose={() => {}}
        onSave={save}
      />
    </I18nProvider>,
  );
  fireEvent.change(screen.getByLabelText(/Channel/), { target: { value: "5" } });
  fireEvent.click(screen.getByRole("button", { name: /Upstream model name/ }));
  fireEvent.click(screen.getByRole("option", { name: "gpt-4o" }));
  // The trigger ignores clicks for ~300ms after a commit (double-Enter guard).
  await new Promise((resolve) => setTimeout(resolve, 320));
  fireEvent.click(screen.getByRole("button", { name: /Upstream model name/ }));
  fireEvent.click(screen.getByRole("option", { name: "Custom model name" }));
  fireEvent.click(screen.getByRole("button", { name: "Back to list" }));
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(save).toHaveBeenCalledWith({ channel_id: 5, mapping_json: '{"real":"gpt-4o"}' });
});
