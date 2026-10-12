import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { StrictMode, useState, type ReactNode } from "react";
import {
  Link,
  MemoryRouter,
  NavLink,
  Outlet,
  Route,
  Routes,
  createMemoryRouter,
  useLocation,
  useNavigate,
  RouterProvider,
} from "react-router-dom";
import { AppearanceProvider, useAppearance } from "../appearance";
import { I18nProvider } from "../i18n";
import { UnsavedChangesBoundary } from "./RouterBoundary";
import { useUnsavedChanges } from "./unsavedChanges";

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
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
      <button type="button" onClick={() => void navigate("/other")}>
        programmatic
      </button>
      <button
        type="button"
        onClick={() => {
          // Mirrors the settings page: the caller guards a same-pathname move
          // itself, then navigates.
          if (guard()) void navigate("?tab=guarded", { replace: true });
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

function routes(dirty: boolean, extra?: ReactNode) {
  return (
    <Routes>
      <Route
        path="/editor"
        element={
          <>
            <Editor dirty={dirty} />
            {extra}
          </>
        }
      />
      <Route path="/other" element={<span data-testid="page">other</span>} />
      <Route path="/third" element={<span data-testid="page">third</span>} />
    </Routes>
  );
}

function guarded(children: ReactNode) {
  return (
    <UnsavedChangesBoundary>
      {children}
      <LocationProbe />
    </UnsavedChangesBoundary>
  );
}

/**
 * The production shape: one data router whose single splat route renders the
 * guard, with the app's own <Routes> underneath. StrictMode matches main.tsx
 * and is what would expose a second confirm.
 */
function mount(
  children: ReactNode,
  options: { initialEntries?: string[]; initialIndex?: number; basename?: string } = {},
) {
  const router = createMemoryRouter([{ path: "*", element: children }], {
    initialEntries: options.initialEntries ?? ["/editor"],
    initialIndex: options.initialIndex,
    basename: options.basename,
  });
  render(
    <StrictMode>
      <I18nProvider>
        <AppearanceProvider>
          <RouterProvider router={router} />
        </AppearanceProvider>
      </I18nProvider>
    </StrictMode>,
  );
  return router;
}

it("asks once on an in-app link and leaves when the operator accepts", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(guarded(routes(true)));
  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("other"));
});

it("keeps the page and the draft when the operator chooses to stay", () => {
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  mount(guarded(routes(true)));
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
  mount(guarded(routes(false)));
  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("other"));
});

it("guards a programmatic navigate() the same way", () => {
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  mount(guarded(routes(true)));
  fireEvent.click(screen.getByRole("button", { name: "programmatic" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(screen.getByTestId("location")).toHaveTextContent("/editor");
});

it("holds back both Back and Forward, and lets an accepted POP through", async () => {
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  // /editor sits in the middle, so both directions leave it.
  const router = mount(guarded(routes(true)), {
    initialEntries: ["/other", "/editor", "/third"],
    initialIndex: 1,
  });

  await act(async () => {
    await router.navigate(-1);
  });
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(screen.getByTestId("location")).toHaveTextContent("/editor");

  await act(async () => {
    await router.navigate(1);
  });
  expect(confirm).toHaveBeenCalledTimes(2);
  expect(screen.getByTestId("location")).toHaveTextContent("/editor");

  confirm.mockReturnValue(true);
  await act(async () => {
    await router.navigate(1);
  });
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("third"));
});

it("asks once when two surfaces hold unsaved edits", () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(
    guarded(
      <Routes>
        <Route path="/editor" element={<TwoEditors />} />
        <Route path="/other" element={<span data-testid="page">other</span>} />
      </Routes>,
    ),
  );
  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).toHaveBeenCalledTimes(1);
});

it("does not treat a same-pathname query change as leaving", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(guarded(routes(true)));
  fireEvent.click(screen.getByRole("link", { name: "same-page" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("?tab=other"));
});

it("lets a page's own guard run without the boundary asking again", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(guarded(routes(true)));
  fireEvent.click(screen.getByRole("button", { name: "own-guard" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("?tab=guarded"));
});

it("does not prompt on a theme switch, which is not a navigation", () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(guarded(routes(true, <ThemeToggle />)));
  fireEvent.click(screen.getByRole("button", { name: "theme" }));
  expect(confirm).not.toHaveBeenCalled();
  expect(screen.getByTestId("location")).toHaveTextContent("/editor");
});

it("drops a registration when its surface unmounts", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(guarded(<Toggle />));
  fireEvent.click(screen.getByRole("button", { name: "hide" }));
  fireEvent.click(screen.getByRole("link", { name: "leave" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("other"));
});

it("measures the leave against the basename-stripped pathname, as /console does", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  mount(guarded(routes(true)), { initialEntries: ["/console/editor"], basename: "/console" });
  // A same-page move still carries the basename on its target; it must not be
  // mistaken for a leave.
  fireEvent.click(screen.getByRole("link", { name: "same-page" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("?tab=other"));

  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).toHaveBeenCalledTimes(1);
});

it("still resolves relative links under the splat root, as App's nested routes do", async () => {
  // The migration moved App's own <Routes> under one `path: "*"` root route.
  // Relative targets inside a parent route (the user module's boards) must
  // still resolve against that route, not against the splat.
  mount(
    guarded(
      <Routes>
        <Route
          path="/team"
          element={
            <>
              <NavLink to="members" end>
                members
              </NavLink>
              <Outlet />
            </>
          }
        >
          <Route path="members" element={<span data-testid="page">members board</span>} />
        </Route>
      </Routes>,
    ),
    { initialEntries: ["/team"] },
  );
  const link = screen.getByRole("link", { name: "members" });
  expect(link).toHaveAttribute("href", "/team/members");
  fireEvent.click(link);
  await waitFor(() => expect(screen.getByTestId("page")).toHaveTextContent("members board"));
});

it("degrades to no router guard in a declarative router", async () => {
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  // Only main.tsx installs the boundary, and only under RouterProvider. In a
  // declarative router it must not throw — it just cannot hold a move back.
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/editor"]}>
        <UnsavedChangesBoundary>
          <Link to="/other">go</Link>
          <LocationProbe />
        </UnsavedChangesBoundary>
      </MemoryRouter>
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("link", { name: "go" }));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/other"));
});
