import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { flushSync } from "react-dom";
import { normalizePalette, type PaletteId } from "./palettes";
import { UI_THEMES } from "./themes/registry";
import type { UIThemeId } from "./themes/types";

export const APPEARANCES = Object.keys(UI_THEMES) as UIThemeId[];
export type Appearance = UIThemeId;
export type ColorScheme = "light" | "dark";
const STYLE_KEY = "meta-gateway.appearance";
// Keep the original key so existing light/dark preferences survive the upgrade.
const SCHEME_KEY = "meta-gateway.theme";
const PALETTE_KEY = "meta-gateway.palette";
const validStyle = (value: string | null): Appearance =>
  APPEARANCES.find((style) => style === value) ?? "classic";
const validScheme = (value: string | null): ColorScheme => (value === "dark" ? "dark" : "light");
/**
 * The colour scheme to start in.
 *
 * An explicit choice wins; with none, the system decides. Defaulting to
 * "light" regardless is what made the member app look broken on a dark
 * desktop: the page declared `color-scheme: light` while the browser sat in
 * dark mode, and Chromium's forced darkening repainted the charts solid black.
 * Computed styles stayed correct throughout — the damage happened at paint
 * time, which is why no style assertion caught it. Following the system gives
 * a real dark theme instead of a light one being repainted.
 */
function preferredScheme(): ColorScheme {
  const stored = read(SCHEME_KEY);
  if (stored === "dark" || stored === "light") return stored;
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
function read(key: string) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function persist(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* Appearance still works without browser storage. */
  }
}

type AppearanceContextValue = {
  appearance: Appearance;
  scheme: ColorScheme;
  palette: PaletteId;
  setAppearance: (value: Appearance) => void;
  setScheme: (value: ColorScheme) => void;
  setPalette: (value: PaletteId) => void;
  toggleScheme: () => void;
};
const AppearanceContext = createContext<AppearanceContextValue | null>(null);

function transition(apply: () => void) {
  if (
    document.startViewTransition &&
    !window.matchMedia("(prefers-reduced-motion: reduce)").matches
  ) {
    const result = document.startViewTransition(() => flushSync(apply));
    void result.ready.catch(() => {});
    void result.finished.catch(() => {});
  } else apply();
}

export function AppearanceProvider({ children }: { children: ReactNode }) {
  const [appearance, updateAppearance] = useState<Appearance>(() => validStyle(read(STYLE_KEY)));
  const [scheme, updateScheme] = useState<ColorScheme>(preferredScheme);
  const [palette, updatePalette] = useState<PaletteId>(() => normalizePalette(read(PALETTE_KEY)));
  useLayoutEffect(() => {
    document.documentElement.dataset.appearance = appearance;
    document.documentElement.dataset.palette = palette;
    document.documentElement.classList.toggle("dark", scheme === "dark");
  }, [appearance, palette, scheme]);
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.storageArea && event.storageArea !== localStorage) return;
      if (event.key === STYLE_KEY || event.key === null)
        updateAppearance(validStyle(read(STYLE_KEY)));
      if (event.key === SCHEME_KEY || event.key === null)
        updateScheme(validScheme(read(SCHEME_KEY)));
      if (event.key === PALETTE_KEY || event.key === null)
        updatePalette(normalizePalette(read(PALETTE_KEY)));
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);
  const setAppearance = useCallback(
    (value: Appearance) => {
      if (value === appearance) return;
      transition(() => {
        document.documentElement.dataset.appearance = value;
        updateAppearance(value);
        persist(STYLE_KEY, value);
      });
    },
    [appearance],
  );
  // `remember` distinguishes "the operator picked this" from "the system
  // changed"; only the former is written to storage, so a system-driven scheme
  // keeps following the system afterwards.
  const applyScheme = useCallback((value: ColorScheme, remember: boolean) => {
    transition(() => {
      document.documentElement.classList.toggle("dark", value === "dark");
      updateScheme(value);
      if (remember) persist(SCHEME_KEY, value);
    });
  }, []);
  const setScheme = useCallback(
    (value: ColorScheme) => {
      if (value === scheme) return;
      applyScheme(value, true);
    },
    [scheme, applyScheme],
  );
  // Follow the desktop while the operator has not chosen a scheme themselves.
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const follow = (event: MediaQueryListEvent) => {
      if (read(SCHEME_KEY)) return;
      applyScheme(event.matches ? "dark" : "light", false);
    };
    media.addEventListener("change", follow);
    return () => media.removeEventListener("change", follow);
  }, [applyScheme]);
  const toggleScheme = useCallback(
    () => setScheme(scheme === "dark" ? "light" : "dark"),
    [scheme, setScheme],
  );
  // Swapping palettes is colour tuning rather than a mode change, so it skips
  // the clip-path wipe that setAppearance/setScheme use.
  const setPalette = useCallback(
    (value: PaletteId) => {
      if (value === palette) return;
      document.documentElement.dataset.palette = value;
      updatePalette(value);
      persist(PALETTE_KEY, value);
    },
    [palette],
  );
  const value = useMemo(
    () => ({ appearance, scheme, palette, setAppearance, setScheme, setPalette, toggleScheme }),
    [appearance, scheme, palette, setAppearance, setScheme, setPalette, toggleScheme],
  );
  return <AppearanceContext.Provider value={value}>{children}</AppearanceContext.Provider>;
}

export function useAppearance() {
  const value = useContext(AppearanceContext);
  if (!value) throw new Error("useAppearance requires AppearanceProvider");
  return value;
}

/** Standalone business views use the modern presentation unless hosted by a theme. */
export function useUITheme() {
  return useContext(AppearanceContext)?.appearance ?? "modern";
}
