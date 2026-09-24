import { useEffect, useState } from "react";

const STORAGE_KEY = "meta-gateway.topbar-items";
// Same-tab listeners need an event of their own: the `storage` event only fires
// in other tabs, so a toggle would not redraw the top bar until a reload.
const CHANGE_EVENT = "meta-gateway:topbar-changed";

/**
 * Which entries the console puts in its chrome — the top bar's controls and,
 * for check-in, the navigation entry as well.
 *
 * Everything here is a display choice, so this is a browser preference like the
 * theme, the palette or the hidden plugin rows — not gateway state. The logout
 * button is deliberately not part of the set: the top bar is the only place the
 * console offers it.
 *
 * Check-in covers both surfaces on purpose. In the classic console the nav rail
 * lives inside the top bar, so hiding only the shortcut left an identical
 * "签到" two centimetres away and the switch looked broken. The page itself
 * stays reachable — the command palette keeps listing it either way.
 */
export const TOP_BAR_ITEMS = ["checkin", "update", "theme", "language"] as const;
export type TopBarItemId = (typeof TOP_BAR_ITEMS)[number];
export type TopBarPrefs = Record<TopBarItemId, boolean>;

/** What a console shows before anyone touches the picker. */
const DEFAULTS: TopBarPrefs = {
  // Check-in is the one entry that is not a global control but a piece of the
  // gateway's own business, so it is the one worth having on by default.
  checkin: true,
  update: true,
  theme: true,
  language: true,
};

function isItemId(value: unknown): value is TopBarItemId {
  return typeof value === "string" && (TOP_BAR_ITEMS as readonly string[]).includes(value);
}

export function readTopBarPrefs(): TopBarPrefs {
  const prefs: TopBarPrefs = { ...DEFAULTS };
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return prefs;
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return prefs;
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (isItemId(key) && typeof value === "boolean") prefs[key] = value;
    }
  } catch {
    // Storage disabled or hand-edited: fall back to the defaults.
  }
  return prefs;
}

export function setTopBarItem(id: TopBarItemId, on: boolean): void {
  const next = { ...readTopBarPrefs(), [id]: on };
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    /* storage unavailable: the choice just does not persist */
  }
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

/** Subscribes to the preference, so every theme's Chrome reacts at once. */
export function useTopBarPrefs(): TopBarPrefs {
  const [prefs, setPrefs] = useState<TopBarPrefs>(readTopBarPrefs);
  useEffect(() => {
    const sync = () => setPrefs(readTopBarPrefs());
    window.addEventListener(CHANGE_EVENT, sync);
    window.addEventListener("storage", sync);
    return () => {
      window.removeEventListener(CHANGE_EVENT, sync);
      window.removeEventListener("storage", sync);
    };
  }, []);
  return prefs;
}
