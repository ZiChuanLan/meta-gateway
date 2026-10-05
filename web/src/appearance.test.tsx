import { useEffect, useState } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AppearanceProvider, useAppearance } from "./appearance";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

/** The appearance panel now hosts the currency settings, which are a server
 *  query: the mount needs a client, and it must not retry in tests. */
function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}
import { ConsoleShell } from "./components/ConsoleShell";
import { AppearancePanel } from "./features/AppearancePanel";
import { I18nProvider } from "./i18n";

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
  // Query-aware, and complete enough for the appearance layer, which
  // subscribes to both queries: these tests want reduced motion (nothing waits
  // on an animation) but a LIGHT desktop, because the scheme now falls back to
  // the system when nothing is stored. A stub carrying only `matches` throws
  // on addEventListener and took out every test in this file.
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
afterEach(() => { cleanup(); localStorage.clear(); document.documentElement.classList.remove("dark"); delete document.documentElement.dataset.appearance; delete document.documentElement.dataset.palette; vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function PreviewState() {
  const { appearance, scheme } = useAppearance();
  const location = useLocation();
  return <output aria-label="Preferences">{appearance}/{scheme}/{location.pathname}{location.search}</output>;
}
function Fixture() {
  const { appearance, scheme, toggleScheme } = useAppearance();
  return <ConsoleShell appearance={appearance} sections={[]} version="test" theme={scheme} onThemeChange={toggleScheme} onSearch={() => {}} onDisconnect={() => {}} health={{ healthy: 0, total: 0, loading: false }}><PreviewState /><AppearancePanel /></ConsoleShell>;
}
function mount() { return render(<AppearanceProvider><QueryClientProvider client={queryClient()}><I18nProvider><MemoryRouter initialEntries={["/settings?tab=appearance"]}><Fixture /></MemoryRouter></I18nProvider></QueryClientProvider></AppearanceProvider>); }

it("switches complete navigation themes independently of the existing dark preference and route", () => {
  localStorage.setItem("meta-gateway.theme", "dark");
  mount();
  expect(document.querySelector(".gate-console-deck")).toBeInTheDocument();
  expect(document.querySelector(".console-sidebar")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("radio", { name: /Modern workspace/ }));
  expect(document.querySelector(".console-sidebar")).toBeInTheDocument();
  expect(document.querySelector(".gate-console-deck")).not.toBeInTheDocument();
  expect(screen.getByLabelText("Preferences")).toHaveTextContent("modern/dark//settings?tab=appearance");
  expect(localStorage.getItem("meta-gateway.appearance")).toBe("modern");
  expect(localStorage.getItem("meta-gateway.theme")).toBe("dark");
  fireEvent.click(screen.getByRole("button", { name: "Light" }));
  expect(screen.getByLabelText("Preferences")).toHaveTextContent("modern/light/");
  expect(localStorage.getItem("meta-gateway.appearance")).toBe("modern");
});

it("retains mounted business views and unsaved input while changing theme packages", () => {
  const mounted = vi.fn();
  function Draft() { const [value, setValue] = useState(""); useEffect(() => { mounted(); }, []); return <input aria-label="Draft" value={value} onChange={(event) => setValue(event.target.value)} />; }
  function Workspace() { const { appearance, scheme, setAppearance, toggleScheme } = useAppearance(); return <><button onClick={() => setAppearance(appearance === "classic" ? "modern" : "classic")}>Switch</button><ConsoleShell appearance={appearance} sections={[]} version="test" theme={scheme} onThemeChange={toggleScheme} onSearch={() => {}} onDisconnect={() => {}} health={{ healthy: 0, total: 0, loading: false }}><Draft /></ConsoleShell></>; }
  render(<AppearanceProvider><I18nProvider><MemoryRouter><Workspace /></MemoryRouter></I18nProvider></AppearanceProvider>);
  fireEvent.change(screen.getByLabelText("Draft"), { target: { value: "unsaved route configuration" } });
  fireEvent.click(screen.getByRole("button", { name: "Switch" }));
  fireEvent.click(screen.getByRole("button", { name: "Switch" }));
  expect(screen.getByLabelText("Draft")).toHaveValue("unsaved route configuration");
  expect(mounted).toHaveBeenCalledTimes(1);
});

it("falls back from an unavailable theme and follows preference changes in another tab", () => {
  localStorage.setItem("meta-gateway.appearance", "removed-theme");
  mount();
  expect(screen.getByLabelText("Preferences")).toHaveTextContent("classic/light/");
  act(() => {
    localStorage.setItem("meta-gateway.appearance", "modern");
    window.dispatchEvent(new StorageEvent("storage", { key: "meta-gateway.appearance", storageArea: localStorage }));
  });
  expect(screen.getByLabelText("Preferences")).toHaveTextContent("modern/light/");
});

it("still applies a theme when storage is unavailable", () => {
  mount();
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("quota"); });
  fireEvent.click(screen.getByRole("radio", { name: /Modern workspace/ }));
  expect(screen.getByLabelText("Preferences")).toHaveTextContent("modern/light/");
  expect(document.documentElement.dataset.appearance).toBe("modern");
});

// The desktop decides the scheme while the operator has never chosen one.
// Defaulting to light on a dark machine is what let Chromium force-darken the
// page and repaint the overview's charts solid black — the styles were right,
// the paint was not.
it("starts in the desktop's colour scheme while no preference is stored", () => {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: String(query).includes("prefers-color-scheme"),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  );
  mount();
  expect(screen.getByLabelText("Preferences")).toHaveTextContent("classic/dark/");
  expect(document.documentElement.classList.contains("dark")).toBe(true);
  // Following the desktop is not the same as choosing: nothing is remembered,
  // so the page keeps tracking the system.
  expect(localStorage.getItem("meta-gateway.theme")).toBeNull();
});

// …and a choice the operator made outlives the desktop's preference.
it("keeps an explicit colour scheme against the desktop", () => {
  localStorage.setItem("meta-gateway.theme", "light");
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: String(query).includes("prefers-color-scheme"),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  );
  mount();
  expect(screen.getByLabelText("Preferences")).toHaveTextContent("classic/light/");
  expect(localStorage.getItem("meta-gateway.theme")).toBe("light");
});

it("applies a preset palette without touching the theme or the colour scheme", () => {
  mount();
  expect(document.documentElement.dataset.palette).toBe("default");
  expect(document.querySelectorAll(".palette-swatch")).toHaveLength(7);
  const ocean = screen.getByRole("button", { name: "Ocean" });
  expect(ocean).toHaveAttribute("aria-pressed", "false");
  fireEvent.click(ocean);
  expect(document.documentElement.dataset.palette).toBe("ocean");
  expect(localStorage.getItem("meta-gateway.palette")).toBe("ocean");
  expect(ocean).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByRole("button", { name: "Follow theme" })).toHaveAttribute("aria-pressed", "false");
  expect(screen.getByLabelText("Preferences")).toHaveTextContent("classic/light/");
  expect(screen.getByRole("button", { name: "Dark" })).toHaveAttribute("aria-pressed", "false");
});

it("keeps the chosen palette while the theme and colour scheme change", () => {
  mount();
  fireEvent.click(screen.getByRole("button", { name: "Violet" }));
  fireEvent.click(screen.getByRole("radio", { name: /Modern workspace/ }));
  fireEvent.click(screen.getByRole("button", { name: "Dark" }));
  expect(document.documentElement.dataset.palette).toBe("violet");
  expect(document.documentElement.dataset.appearance).toBe("modern");
  expect(document.documentElement.classList.contains("dark")).toBe(true);
  expect(screen.getByRole("button", { name: "Violet" })).toHaveAttribute("aria-pressed", "true");
});

it("falls back from an unavailable palette and follows preference changes in another tab", () => {
  localStorage.setItem("meta-gateway.palette", "neon");
  mount();
  expect(document.documentElement.dataset.palette).toBe("default");
  act(() => {
    localStorage.setItem("meta-gateway.palette", "sakura");
    window.dispatchEvent(new StorageEvent("storage", { key: "meta-gateway.palette", storageArea: localStorage }));
  });
  expect(document.documentElement.dataset.palette).toBe("sakura");
});

// The panel is the only way to switch entries off, so it must drive the chrome
// it describes: a toggle that writes storage but leaves the bar alone is the
// exact failure this replaced.
it("switches chrome entries from the panel and restores them together", () => {
	mount();
	const search = screen.getByRole("button", { name: /Search & commands/ });
	const checkin = screen.getByRole("button", { name: "Check-in" });
	expect(search).toHaveAttribute("aria-pressed", "true");
	expect(checkin).toHaveAttribute("aria-pressed", "true");

	fireEvent.click(search);
	expect(search).toHaveAttribute("aria-pressed", "false");
	expect(document.querySelector(".deck-palette-btn")).not.toBeInTheDocument();

	// The navigation entries are switched in the same panel, one per page.
	fireEvent.click(checkin);
	expect(checkin).toHaveAttribute("aria-pressed", "false");

	fireEvent.click(screen.getByRole("button", { name: "Reset to defaults" }));
	expect(screen.getByRole("button", { name: /Search & commands/ })).toHaveAttribute(
		"aria-pressed",
		"true",
	);
	expect(document.querySelector(".deck-palette-btn")).toBeInTheDocument();
});
