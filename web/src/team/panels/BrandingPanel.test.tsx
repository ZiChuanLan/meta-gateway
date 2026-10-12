import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, Link, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { I18nProvider } from "../../i18n";
import { UnsavedChangesBoundary } from "../../lib/RouterBoundary";
import { teamText } from "../text";
import { BrandingPanel } from "./BrandingPanel";
import type { TeamSettings, TeamRequest } from "../types";
const settings: TeamSettings = {
  mode: "team",
  branding: {
    name: "Gateway",
    accent: "#275b85",
    logo_url: "",
    notice: "",
    login_description: "",
    api_base_url: "",
    show_usage: true,
    show_routing: true,
  },
};
beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
function mount(fail = false) {
  let update!: (value: TeamSettings) => void;
  const request = vi.fn<TeamRequest>(async (_path, init) => {
    if (fail) throw new Error("save_failed");
    return JSON.parse(String(init?.body));
  });
  function Host() {
    const [value, setValue] = useState(settings);
    update = setValue;
    return (
      <UnsavedChangesBoundary>
        <Link to="/other">leave</Link>
        <Outlet
          context={{
            settings: value,
            request,
            t: teamText("en"),
            locale: "en",
            owner: true,
            hasOwner: true,
          }}
        />
      </UnsavedChangesBoundary>
    );
  }
  const router = createMemoryRouter(
    [
      {
        path: "/",
        element: <Host />,
        children: [
          { path: "brand", element: <BrandingPanel /> },
          { path: "other", element: <p>Other page</p> },
        ],
      },
    ],
    { initialEntries: ["/brand"] },
  );
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <I18nProvider>
        <RouterProvider router={router} />
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { request, update: (v: TeamSettings) => act(() => update(v)) };
}
it("keeps branding drafts through refresh and a refused navigation", async () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  const { update } = mount();
  const field = screen.getByLabelText("Site name");
  fireEvent.change(field, { target: { value: "Draft" } });
  update({ ...settings, branding: { ...settings.branding, name: "Remote" } });
  expect(field).toHaveValue("Draft");
  fireEvent.click(screen.getByRole("link", { name: "leave" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(field).toHaveValue("Draft");
});
it("uses the real unwrapped settings response and clears the leave guard after save", async () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  const { request } = mount();
  fireEvent.change(screen.getByLabelText("Site name"), { target: { value: "Saved brand" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => {
    expect(screen.getByLabelText("Site name")).toBeEnabled();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });
  expect(request).toHaveBeenCalledWith(
    "/admin/team/settings",
    expect.objectContaining({ method: "PUT" }),
  );
  // Let the post-save refetch settle: it rewrites the baseline, and a
  // navigation attempted before that would still look dirty.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  fireEvent.click(screen.getByRole("link", { name: "leave" }));
  await screen.findByText("Other page");
  expect(confirm).not.toHaveBeenCalled();
});
it("retains branding input and leave guard when saving fails", async () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  mount(true);
  fireEvent.change(screen.getByLabelText("Site name"), { target: { value: "Unsaved brand" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("link", { name: "leave" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(screen.getByLabelText("Site name")).toHaveValue("Unsaved brand");
});
