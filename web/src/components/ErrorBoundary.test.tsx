import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { ErrorBoundary } from "./ErrorBoundary";

function Boom(): never {
  throw new Error("boom: null is not an array");
}

/** What the browser reports when a chunk was removed by a deploy. */
function Stale(): never {
  throw new TypeError(
    "Failed to fetch dynamically imported module: https://gw.test/console/assets/Exchange-BqgMnj-G.js",
  );
}

describe("ErrorBoundary", () => {
  beforeEach(() => {
    localStorage.setItem("meta-gateway.locale", "zh-CN");
    sessionStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders children when nothing throws", () => {
    render(
      <I18nProvider>
        <ErrorBoundary>
          <p>页面内容</p>
        </ErrorBoundary>
      </I18nProvider>,
    );
    expect(screen.getByText("页面内容")).toBeTruthy();
  });

  // The failure mode this exists for: a thrown render error unmounted the
  // whole tree and left a blank page with no way back.
  it("shows a recoverable card instead of a blank page", () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    render(
      <I18nProvider>
        <ErrorBoundary>
          <Boom />
        </ErrorBoundary>
      </I18nProvider>,
    );
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByText("控制台遇到了意外错误")).toBeTruthy();
    expect(screen.getByText(/boom: null is not an array/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "重新加载" })).toBeTruthy();
  });

  // A tab opened before a deploy holds an old build; its next lazy page 404s.
  // That is not a crash: say the app was updated and offer the reload.
  it("explains a chunk that a deploy removed", () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    render(
      <I18nProvider>
        <ErrorBoundary>
          <Stale />
        </ErrorBoundary>
      </I18nProvider>,
    );
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByText("页面已更新")).toBeTruthy();
    expect(screen.getByText(/刷新一次即可继续/)).toBeTruthy();
    // No stack trace: there is nothing here for the user to report.
    expect(screen.queryByText(/Exchange-BqgMnj-G/)).toBeNull();
    expect(screen.getByRole("button", { name: "重新加载" })).toBeTruthy();
  });

  // React does not always keep the browser's wording for a lazy chunk that
  // never arrived, so the boundary also trusts the stamp the preload handler
  // writes — otherwise the one case it exists for falls back to "unexpected
  // error" with a stack trace the user cannot use.
  it("treats an unrelated-looking error as stale when a reload just happened", () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    sessionStorage.setItem("meta-gateway.preload-reload", String(Date.now()));
    render(
      <I18nProvider>
        <ErrorBoundary>
          <Boom />
        </ErrorBoundary>
      </I18nProvider>,
    );
    expect(screen.getByText("页面已更新")).toBeTruthy();
    expect(screen.queryByText(/boom: null is not an array/)).toBeNull();
  });
});
