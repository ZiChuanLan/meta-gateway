import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Link, MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { UnsavedChangesBoundary } from "../lib/unsavedChanges";
import { OAuthPanel } from "./OAuthPanel";
import { teamText } from "./text";
import type { OAuthSettings, TeamRequest } from "./types";

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
});

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
});

function provider(overrides: Partial<OAuthSettings["providers"][number]> = {}) {
  return {
    id: "github",
    label: "GitHub",
    enabled: false,
    client_id: "",
    has_secret: false,
    authorize_url: "",
    token_url: "",
    userinfo_url: "",
    scopes: "",
    callback_url: "https://gateway/console/auth/oauth/github/callback",
    default_authorize_url: "https://github.com/login/oauth/authorize",
    default_token_url: "https://github.com/login/oauth/access_token",
    default_userinfo_url: "https://api.github.com/user",
    default_scopes: "read:user",
    ...overrides,
  };
}

function document_(overrides: Partial<OAuthSettings> = {}): OAuthSettings {
  return { auto_register: false, default_policy_id: 0, providers: [provider()], ...overrides };
}

interface Stub {
  request: TeamRequest;
  calls: Array<[string, RequestInit | undefined]>;
}

/** A transport that answers the two endpoints the panel uses. */
function stub(options: { settings?: OAuthSettings; failSave?: boolean } = {}): Stub {
  const current = options.settings ?? document_();
  const calls: Stub["calls"] = [];
  const request: TeamRequest = async <T,>(path: string, init?: RequestInit) => {
    calls.push([path, init]);
    if (path !== "/admin/team/oauth") throw new Error(`unexpected path ${path}`);
    if (init?.method === "PUT") {
      if (options.failSave) throw new Error("save failed");
      const body = JSON.parse(String(init.body));
      return {
        auto_register: body.auto_register,
        default_policy_id: body.default_policy_id,
        providers: current.providers.map((item, index) => ({
          ...item,
          enabled: body.providers[index].enabled,
          client_id: body.providers[index].client_id,
        })),
      } as T;
    }
    return current as T;
  };
  return { request, calls };
}

/** The panel on its board, with the sidebar link that prompted the fix. */
function board(request: TeamRequest) {
  return (
    <I18nProvider>
      <MemoryRouter initialEntries={["/team/oauth"]}>
        <UnsavedChangesBoundary>
          <Link to="/team/members">members</Link>
          <Routes>
            <Route
              path="/team/oauth"
              element={
                <OAuthPanel request={request} policies={[]} locale="en" t={teamText("en")} />
              }
            />
            <Route path="/team/members" element={<span data-testid="page">members</span>} />
          </Routes>
        </UnsavedChangesBoundary>
      </MemoryRouter>
    </I18nProvider>
  );
}

it("keeps a half-typed provider field when the operator leaves the board", async () => {
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  render(board(stub().request));

  fireEvent.change(await screen.findByLabelText("Client ID"), { target: { value: "gh-client" } });
  fireEvent.click(screen.getByRole("link", { name: "members" }));

  expect(confirm).toHaveBeenCalledTimes(1);
  expect(screen.getByLabelText("Client ID")).toHaveValue("gh-client");
  expect(screen.queryByTestId("page")).not.toBeInTheDocument();
});

it("leaves without a prompt once the board is saved", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  render(board(stub().request));

  fireEvent.change(await screen.findByLabelText("Client ID"), { target: { value: "gh-client" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await screen.findByText("Saved");

  fireEvent.click(screen.getByRole("link", { name: "members" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("members"));
});

it("keeps the draft and the guard when a save fails", async () => {
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  render(board(stub({ failSave: true }).request));

  fireEvent.change(await screen.findByLabelText("Client ID"), { target: { value: "gh-client" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await screen.findByRole("alert");

  expect(screen.getByLabelText("Client ID")).toHaveValue("gh-client");
  fireEvent.click(screen.getByRole("link", { name: "members" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(screen.getByLabelText("Client ID")).toHaveValue("gh-client");
});

it("does not let a late server document overwrite an unsaved edit", async () => {
  let land!: (value: OAuthSettings) => void;
  const late: TeamRequest = <T,>(path: string) => {
    if (path !== "/admin/team/oauth") return Promise.reject(new Error(`unexpected path ${path}`));
    return new Promise<T>((resolve) => {
      land = (value) => resolve(value as unknown as T);
    });
  };

  const view = render(board(stub().request));
  fireEvent.change(await screen.findByLabelText("Client ID"), { target: { value: "typed" } });

  // A second fetch — the transport prop changed while the operator was typing.
  await act(async () => {
    view.rerender(board(late));
  });
  await act(async () => {
    land(document_({ providers: [provider({ client_id: "server-value" })] }));
  });

  expect(screen.getByLabelText("Client ID")).toHaveValue("typed");
});
