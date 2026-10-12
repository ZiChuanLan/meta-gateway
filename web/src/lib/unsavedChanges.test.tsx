import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useState, type ReactNode } from "react";
import { Link, MemoryRouter, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { AppearanceProvider, useAppearance } from "../appearance";
import { I18nProvider } from "../i18n";
import { UnsavedChangesBoundary, useUnsavedChanges } from "./unsavedChanges";

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
  // AppearanceProvider subscribes to both queries; a stub carrying only
  // `matches` throws on addEventListener.
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: String(query).includes("prefers-reduced-motion"),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  );
});

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
});

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">
      {location.pathname}
      {location.search}
    </div>
  );
}

/** A page that owns a draft and hands it to the shared guard while dirty. */
function Editor({ dirty }: { dirty: boolean }) {
  const guard = useUnsavedChanges(dirty);
  const navigate = useNavigate();
  const [text, setText] = useState("");
  return (
    <>
      <Link to="/other">go</Link>
      {/* A tab-style move: same pathname, different query. */}
      <Link to="/editor?tab=other">same-page</Link>
      <button type="button" onClick={() => navigate("/other")}>
        programmatic
      </button>
      <button type="button" onClick={() => navigate(-1)}>
        back
      </button>
      <button
        type="button"
        onClick={() => {
          // Mirrors the settings page: the caller guards a same-pathname move
          // itself, then navigates.
          if (guard()) navigate("?tab=guarded", { replace: true });
        }}
      >
        own-guard
      </button>
      <input aria-label="draft" value={text} onChange={(e) => setText(e.target.value)} />
      <span data-testid="page">editor</span>
    </>
  );
}

/** Two mounted surfaces holding unsaved edits at the same pathname. */
function TwoEditors() {
  useUnsavedChanges(true);
  useUnsavedChanges(true);
  return <Link to="/other">go</Link>;
}

/** The console's theme switch, which only changes appearance state. */
function ThemeToggle() {
  const { toggleScheme } = useAppearance();
  return (
    <button type="button" onClick={toggleScheme}>
      theme
    </button>
  );
}

/** Removes its dirty child without leaving the page, to prove no leak. */
function Toggle() {
  const [show, setShow] = useState(true);
  return (
    <>
      <Link to="/other">leave</Link>
      <button type="button" onClick={() => setShow(false)}>
        hide
      </button>
      <Routes>
        <Route
          path="/editor"
          element={show ? <Editor dirty /> : <span data-testid="page">editor hidden</span>}
        />
        <Route path="/other" element={<span data-testid="page">other</span>} />
      </Routes>
    </>
  );
}

function routes(dirty: boolean) {
  return (
    <Routes>
      <Route path="/editor" element={<Editor dirty={dirty} />} />
      <Route path="/other" element={<span data-testid="page">other</span>} />
    </Routes>
  );
}

function mount(children: ReactNode, initialEntries: string[] = ["/editor"]) {
  return render(
    <I18nProvider>
      <AppearanceProvider>
        <MemoryRouter initialEntries={initialEntries}>
          <UnsavedChangesBoundary>
            {children}
            <LocationProbe />
          </UnsavedChangesBoundary>
        </MemoryRouter>
      </AppearanceProvider>
    </I18nProvider>,
  );
}

/** A cancelable page unload; `defaultPrevented` is the browser contract. */
function unload() {
  const event = new Event("beforeunload", { cancelable: true });
  fireEvent(window, event);
  return event;
}

it("arms the browser prompt only while there are unsaved edits", () => {
  mount(routes(true));
  expect(unload().defaultPrevented).toBe(true);
  cleanup();
  mount(routes(false));
  expect(unload().defaultPrevented).toBe(false);
});

it("asks once on an in-app link and leaves when the operator accepts", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(routes(true));
  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("other"));
});

it("keeps the page and the draft when the operator chooses to stay", () => {
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  mount(routes(true));
  fireEvent.change(screen.getByLabelText("draft"), { target: { value: "half typed" } });
  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  // Neither the URL nor the mounted form moved.
  expect(screen.getByTestId("location")).toHaveTextContent("/editor");
  expect(screen.getByLabelText("draft")).toHaveValue("half typed");
});

it("does not prompt when nothing is unsaved", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(routes(false));
  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("other"));
});

it("guards a programmatic navigate() the same way", () => {
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  mount(routes(true));
  fireEvent.click(screen.getByRole("button", { name: "programmatic" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(screen.getByTestId("location")).toHaveTextContent("/editor");
});

it("guards router-level back, and lets an accepted back through", async () => {
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  // One POP available: the editor was pushed on top of /other.
  mount(routes(true), ["/other", "/editor"]);
  fireEvent.click(screen.getByRole("button", { name: "back" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(screen.getByTestId("location")).toHaveTextContent("/editor");

  confirm.mockReturnValue(true);
  fireEvent.click(screen.getByRole("button", { name: "back" }));
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("other"));
});

it("asks once when two surfaces hold unsaved edits", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(
    <Routes>
      <Route path="/editor" element={<TwoEditors />} />
      <Route path="/other" element={<span data-testid="page">other</span>} />
    </Routes>,
  );
  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("other"));
});

it("does not treat a same-pathname query change as leaving", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(routes(true));
  fireEvent.click(screen.getByRole("link", { name: "same-page" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("?tab=other"));
});

it("lets a page's own guard run without the boundary asking again", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(routes(true));
  fireEvent.click(screen.getByRole("button", { name: "own-guard" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("?tab=guarded"));
});

it("does not prompt on a theme switch, which is not a navigation", () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(
    <Routes>
      <Route
        path="/editor"
        element={
          <>
            <Editor dirty />
            <ThemeToggle />
          </>
        }
      />
      <Route path="/other" element={<span data-testid="page">other</span>} />
    </Routes>,
  );
  fireEvent.click(screen.getByRole("button", { name: "theme" }));
  expect(confirm).not.toHaveBeenCalled();
  expect(screen.getByTestId("location")).toHaveTextContent("/editor");
});

it("drops a registration when its surface unmounts", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(<Toggle />);
  fireEvent.click(screen.getByRole("button", { name: "hide" }));
  fireEvent.click(screen.getByRole("link", { name: "leave" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("other"));
});

it("measures the leave against the stripped pathname, as production runs under /console", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  // Production mounts the boundary under `basename="/console"`, so a resolved
  // navigation target carries the basename while `useLocation` does not.
  render(
    <I18nProvider>
      <AppearanceProvider>
        <MemoryRouter basename="/console" initialEntries={["/console/editor"]}>
          <UnsavedChangesBoundary>
            {routes(true)}
            <LocationProbe />
          </UnsavedChangesBoundary>
        </MemoryRouter>
      </AppearanceProvider>
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("link", { name: "same-page" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("?tab=other"));

  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).toHaveBeenCalledTimes(1);
});
