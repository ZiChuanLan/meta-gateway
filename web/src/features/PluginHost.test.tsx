import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { PluginHost } from "./PluginHost";

const session = vi.hoisted(() => ({
  client: {
    getToken: () => "",
    request: vi.fn(),
  } as { getToken: () => string; request: ReturnType<typeof vi.fn> },
}));

vi.mock("../session", () => ({ useSession: () => ({ client: session.client }) }));
vi.mock("../hooks/useModules", () => ({
  useModules: () => ({ modules: [{ id: "opencode-host", name: "订阅账号宿主" }] }),
}));
vi.mock("../lib/pluginNav", () => ({
  useHiddenPlugins: () => new Set<string>(),
  setPluginNavHidden: () => undefined,
}));

beforeEach(() => {
  localStorage.setItem("meta-gateway.locale", "en");
  session.client.getToken = () => "";
  session.client.request.mockReset();
});
afterEach(() => {
  cleanup();
  localStorage.clear();
});

function frame() {
  return document.querySelector("iframe");
}

function renderHost() {
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/plugins/opencode-host"]}>
        <Routes>
          <Route path="/plugins/:id" element={<PluginHost />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

// A cookie-session console has no bearer token, so the iframe used to be built
// with an empty ?t= and the plugin page showed {"error":"unauthorized"} whatever
// the operator did. It now asks for a short-lived embed token first.
it("mints an embed token when the console holds no bearer", async () => {
  session.client.request.mockResolvedValue({ token: "mg-sess.embed" });
  renderHost();

  expect(session.client.request).toHaveBeenCalledWith("/admin/plugins/opencode-host/embed-token", {
    method: "POST",
  });
  await waitFor(() =>
    expect(frame()?.getAttribute("src")).toBe(
      "/admin/plugins/opencode-host/proxy/?t=mg-sess.embed",
    ),
  );
});

it("reports a failure instead of framing an unauthenticated page", async () => {
  session.client.request.mockRejectedValue(new Error("unauthorized"));
  renderHost();

  await waitFor(() => expect(screen.getByText(/The plugin page could not be loaded/)).toBeTruthy());
  expect(frame()).toBeNull();
});

it("uses the token it already holds and asks for nothing", () => {
  session.client.getToken = () => "mg-sess.existing";
  renderHost();

  expect(frame()?.getAttribute("src")).toBe(
    "/admin/plugins/opencode-host/proxy/?t=mg-sess.existing",
  );
  expect(session.client.request).not.toHaveBeenCalled();
});
