import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { OperatorClaimPanel, OperatorUpgradePrompt } from "./OperatorProfilePanel";
import { UpdateChannelPanel } from "./UpdateChannelPanel";
import type { ReactNode } from "react";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});
function mount(children: ReactNode) {
  localStorage.setItem("meta-gateway.locale", "en");
  localStorage.setItem("meta-gateway.admin-token", "session-token");
  return render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
        })
      }
    >
      <I18nProvider>
        <SessionProvider>{children}</SessionProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}
it("claims the owner account by confirming the token and TOTP, then clears sensitive input", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
    if (String(input).includes("/admin/operator/claim")) {
      return new Response(
        JSON.stringify({
          onboarding: {
            required: false,
            username: "new-admin",
            has_owner: true,
            token_login: false,
            totp_login: true,
            mode: "personal",
          },
        }),
      );
    }
    return new Response(
      JSON.stringify({
        required: true,
        username: "admin",
        has_owner: false,
        token_login: true,
        totp_login: true,
        mode: "personal",
      }),
    );
  });
  vi.stubGlobal("fetch", fetcher);
  mount(<OperatorClaimPanel />);
  fireEvent.change(await screen.findByLabelText("Username"), { target: { value: "new-admin" } });
  fireEvent.change(screen.getByLabelText("New password"), {
    target: { value: "boss-password-123" },
  });
  fireEvent.change(screen.getByLabelText("Confirm new password"), {
    target: { value: "boss-password-123" },
  });
  fireEvent.change(screen.getByLabelText("Current deployment token (ADMIN_TOKEN)"), {
    target: { value: "original-secret" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: /verification|authenticator|2FA|code/i }), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Set up the account" }));
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(true),
  );
  const call = fetcher.mock.calls.find(([, init]) => init?.method === "POST")!;
  expect(JSON.parse(String(call[1]?.body))).toEqual({
    username: "new-admin",
    password: "boss-password-123",
    token: "original-secret",
    totp_code: "123456",
  });
  await waitFor(() =>
    expect(screen.getByLabelText("Current deployment token (ADMIN_TOKEN)")).toHaveValue(""),
  );
});

it("refuses to submit a password that does not match its confirmation", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            required: true,
            username: "admin",
            has_owner: false,
            token_login: true,
            totp_login: false,
            mode: "personal",
          }),
        ),
    ),
  );
  mount(<OperatorClaimPanel />);
  fireEvent.change(await screen.findByLabelText("New password"), {
    target: { value: "one-password" },
  });
  fireEvent.change(screen.getByLabelText("Confirm new password"), {
    target: { value: "another-password" },
  });
  fireEvent.change(screen.getByLabelText("Current deployment token (ADMIN_TOKEN)"), {
    target: { value: "original-secret" },
  });
  expect(screen.getByText("The two passwords do not match.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Set up the account" })).toBeDisabled();
});
it("makes beta an explicit choice and explains watchtower tag limits", async () => {
  const fetcher = vi.fn(
    async (_input: RequestInfo | URL, init?: RequestInit) =>
      new Response(
        JSON.stringify({
          channel: init?.method === "PUT" ? "beta" : "stable",
          mode: "watchtower",
          tracking_tag: "latest",
        }),
      ),
  );
  vi.stubGlobal("fetch", fetcher);
  mount(<UpdateChannelPanel />);
  const select = await screen.findByLabelText("Update channel");
  await waitFor(() => expect(select).toBeEnabled());
  fireEvent.change(select, { target: { value: "beta" } });
  expect(screen.getByText(/Beta may be unstable/)).toBeInTheDocument();
  expect(screen.getByText(/Watchtower tracks: latest/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true),
  );
  expect(fetcher.mock.calls.some(([path]) => String(path).includes("self-update/apply"))).toBe(
    false,
  );
});

it("prompts an unclaimed deployment and allows deferral", async () => {
  sessionStorage.removeItem("operator-claim-dismissed");
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            required: true,
            username: "admin",
            has_owner: false,
            token_login: true,
            totp_login: false,
            mode: "personal",
          }),
        ),
    ),
  );
  mount(<OperatorUpgradePrompt />);
  expect(await screen.findByRole("dialog")).toHaveTextContent("Set up the administrator account");
  fireEvent.click(screen.getByRole("button", { name: "Set up later" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  sessionStorage.removeItem("operator-claim-dismissed");
});

it("stays quiet once the deployment has an owner account", async () => {
  sessionStorage.removeItem("operator-claim-dismissed");
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            required: false,
            username: "boss",
            has_owner: true,
            token_login: false,
            totp_login: false,
            mode: "personal",
          }),
        ),
    ),
  );
  mount(<OperatorUpgradePrompt />);
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});
