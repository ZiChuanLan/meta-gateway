import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from "react";
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
const validStyle = (value: string | null): Appearance => APPEARANCES.find((style) => style === value) ?? "classic";
const validScheme = (value: string | null): ColorScheme => value === "dark" ? "dark" : "light";
function read(key: string) { try { return localStorage.getItem(key); } catch { return null; } }
function persist(key: string, value: string) { try { localStorage.setItem(key, value); } catch { /* Appearance still works without browser storage. */ } }

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
  if (document.startViewTransition && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    const result = document.startViewTransition(() => flushSync(apply));
    void result.ready.catch(() => {});
    void result.finished.catch(() => {});
  } else apply();
}

export function AppearanceProvider({ children }: { children: ReactNode }) {
  const [appearance, updateAppearance] = useState<Appearance>(() => validStyle(read(STYLE_KEY)));
  const [scheme, updateScheme] = useState<ColorScheme>(() => validScheme(read(SCHEME_KEY)));
  const [palette, updatePalette] = useState<PaletteId>(() => normalizePalette(read(PALETTE_KEY)));
  useLayoutEffect(() => {
    document.documentElement.dataset.appearance = appearance;
    document.documentElement.dataset.palette = palette;
    document.documentElement.classList.toggle("dark", scheme === "dark");
  }, [appearance, palette, scheme]);
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.storageArea && event.storageArea !== localStorage) return;
      if (event.key === STYLE_KEY || event.key === null) updateAppearance(validStyle(read(STYLE_KEY)));
      if (event.key === SCHEME_KEY || event.key === null) updateScheme(validScheme(read(SCHEME_KEY)));
      if (event.key === PALETTE_KEY || event.key === null) updatePalette(normalizePalette(read(PALETTE_KEY)));
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);
  const setAppearance = useCallback((value: Appearance) => {
    if (value === appearance) return;
    transition(() => {
      document.documentElement.dataset.appearance = value;
      updateAppearance(value);
      persist(STYLE_KEY, value);
    });
  }, [appearance]);
  const setScheme = useCallback((value: ColorScheme) => {
    if (value === scheme) return;
    transition(() => {
      document.documentElement.classList.toggle("dark", value === "dark");
      updateScheme(value);
      persist(SCHEME_KEY, value);
    });
  }, [scheme]);
  const toggleScheme = useCallback(() => setScheme(scheme === "dark" ? "light" : "dark"), [scheme, setScheme]);
  // Swapping palettes is colour tuning rather than a mode change, so it skips
  // the clip-path wipe that setAppearance/setScheme use.
  const setPalette = useCallback((value: PaletteId) => {
    if (value === palette) return;
    document.documentElement.dataset.palette = value;
    updatePalette(value);
    persist(PALETTE_KEY, value);
  }, [palette]);
  const value = useMemo(() => ({ appearance, scheme, palette, setAppearance, setScheme, setPalette, toggleScheme }), [appearance, scheme, palette, setAppearance, setScheme, setPalette, toggleScheme]);
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
