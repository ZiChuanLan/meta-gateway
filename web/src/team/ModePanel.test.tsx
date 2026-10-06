import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { I18nProvider } from "../i18n";
import { ModePanel } from "./ModePanel";
import type { ModeInfo, TeamRequest } from "./types";

afterEach(cleanup);
beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
});

function setup(mode: "personal" | "team", hasOwner: boolean) {
  const calls: Array<[string, RequestInit | undefined]> = [];
  let current: ModeInfo = { mode, has_owner: hasOwner, role: "owner" };
  const request: TeamRequest = async <T,>(path: string, init?: RequestInit) => {
    calls.push([path, init]);
    if (path === "/admin/mode" && (!init || init.method === undefined)) return current as T;
    if (path === "/admin/mode" && init?.method === "PATCH") {
      current = { ...current, mode: JSON.parse(String(init.body)).mode };
      return current as T;
    }
    if (path === "/admin/mode/owner") return { ok: true } as T;
    return {} as T;
  };
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <I18nProvider>
        <ModePanel request={request} locale="en" />
      </I18nProvider>
    </QueryClientProvider>,
  );
  return calls;
}

describe("operating mode", () => {
  it("lets team mode be selected with no owner yet, and says where the owner comes from", async () => {
    setup("personal", false);
    // The switch is what creates the owner, so it must not be gated on one
    // existing — that gate is what used to force a second account for the
    // operator, under a name they could not even reuse.
    const select = await screen.findByLabelText("Operating mode");
    expect(select.querySelector('option[value="team"]')).not.toBeDisabled();
    expect(
      screen.getByText(/creates the owner from the deployment administrator/i),
    ).toBeInTheDocument();
    // The manual form stays, but only as the fallback for a deployment with no
    // admin secret to derive a first password from.
    expect(screen.getByRole("heading", { name: "Create owner account" })).toBeInTheDocument();
  });

  it("switches the instance to team mode through the settings panel", async () => {
    const calls = setup("personal", true);
    const select = await screen.findByLabelText("Operating mode");
    fireEvent.change(select, { target: { value: "team" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(
        calls.some(
          ([path, init]) =>
            path === "/admin/mode" &&
            init?.method === "PATCH" &&
            JSON.parse(String(init.body)).mode === "team",
        ),
      ).toBe(true),
    );
  });

  it("surfaces a link to the user-management area only in team mode", async () => {
    setup("team", true);
    expect(await screen.findByRole("link", { name: /User management/ })).toHaveAttribute(
      "href",
      "/console/users/members",
    );
  });
});
