import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { QuotaDialog } from "./MemberForms";
import { teamText } from "./text";
import type { TeamUser, TeamRequest } from "./types";
afterEach(cleanup);
const user: TeamUser = {
  id: 1,
  username: "member",
  name: "Member",
  role: "member",
  status: "active",
  policy_id: 1,
  created_at: "",
  key_count: 0,
  quota_total_tokens: 1000,
  quota_used_tokens: 50,
  quota_total_cost: 10,
  quota_used_cost: 2,
};
it("rejects invalid quotas, treats blank as unlimited and prevents duplicate writes", async () => {
  let finish!: (value: unknown) => void;
  const request = vi.fn(
    (_path: string, _init?: RequestInit) =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  render(
    <I18nProvider>
      <QuotaDialog
        request={request as TeamRequest}
        user={user}
        locale="en"
        t={teamText("en")}
        onClose={vi.fn()}
        onSaved={vi.fn()}
      />
    </I18nProvider>,
  );
  const inputs = screen.getAllByRole("spinbutton");
  const save = screen.getByRole("button", { name: "Save" });
  fireEvent.change(inputs[0]!, { target: { value: "1.5" } });
  expect(save).toBeDisabled();
  fireEvent.change(inputs[0]!, { target: { value: "-1" } });
  expect(save).toBeDisabled();
  fireEvent.change(inputs[0]!, { target: { value: "" } });
  fireEvent.change(inputs[1]!, { target: { value: "1000001" } });
  expect(save).toBeDisabled();
  fireEvent.change(inputs[1]!, { target: { value: "" } });
  expect(save).toBeEnabled();
  fireEvent.click(save);
  fireEvent.click(save);
  expect(request).toHaveBeenCalledTimes(1);
  expect(JSON.parse(String(request.mock.calls[0]?.[1]?.body))).toEqual({
    quota_total_tokens: 0,
    quota_total_cost: 0,
    quota_reset: false,
  });
  await act(async () => {
    finish({});
  });
});
