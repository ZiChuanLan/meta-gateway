import { useEffect, useState } from "react";

const STORAGE_KEY = "meta-gateway.topbar-items";
// Same-tab listeners need an event of their own: the `storage` event only fires
// in other tabs, so a toggle would not redraw the top bar until a reload.
const CHANGE_EVENT = "meta-gateway:topbar-changed";

/**
 * What the console's chrome shows.
 *
 * Two kinds of entry live behind one preference:
 *
 *   - controls — the top bar's own buttons (search, update notice, light/dark,
 *     language);
 *   - navigation — the rail/sidebar entries, named by path.
 *
 * Everything here is a display choice, so this is a browser preference like the
 * theme, the palette or the hidden plugin rows, not gateway state. The logout
 * button is deliberately not part of the set: the chrome is the only place the
 * console offers it. Nor is there a check-in *shortcut* — the top bar used to
 * carry its own button next to the navigation entry that already points at the
 * same page, and a duplicate that needs a switch of its own is just clutter.
 *
 * Hiding is never the same as removing: the routes stay mounted, so a hidden
 * entry can still be reached from the command palette (⌘K) or by URL. That is
 * what keeps the panel safe to experiment with — and what the reset button
 * restores.
 */
export const TOP_BAR_CONTROLS = ["search", "update", "theme", "language"] as const;
export type TopBarControlId = (typeof TOP_BAR_CONTROLS)[number];

export interface ChromePrefs {
	/** One flag per top-bar control. */
	controls: Record<TopBarControlId, boolean>;
	/** Navigation paths the operator hid. Absent means shown. */
	hiddenNav: string[];
}

const DEFAULT_CONTROLS: Record<TopBarControlId, boolean> = {
	search: true,
	update: true,
	theme: true,
	language: true,
};

export function defaultChromePrefs(): ChromePrefs {
	return { controls: { ...DEFAULT_CONTROLS }, hiddenNav: [] };
}

function isControlId(value: unknown): value is TopBarControlId {
	return typeof value === "string" && (TOP_BAR_CONTROLS as readonly string[]).includes(value);
}

/**
 * Reads the preference, tolerating everything localStorage can hold.
 *
 * The stored shape predates the navigation flags: it was a flat map of booleans
 * (`{"checkin":false,"theme":true,…}`), where the check-in shortcut was the
 * only entry that ever governed a page. A record in that shape is upgraded
 * rather than discarded — the other keys are still exactly the controls, and
 * `checkin:false` was an operator saying they did not want check-in in their
 * chrome, which is now the navigation entry's flag.
 */
export function readChromePrefs(): ChromePrefs {
	const prefs = defaultChromePrefs();
	let parsed: unknown;
	try {
		const raw = localStorage.getItem(STORAGE_KEY);
		if (!raw) return prefs;
		parsed = JSON.parse(raw);
	} catch {
		// Storage disabled or hand-edited: fall back to the defaults.
		return prefs;
	}
	if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return prefs;
	const record = parsed as Record<string, unknown>;

	const controls = record.controls;
	if (controls && typeof controls === "object" && !Array.isArray(controls)) {
		for (const [key, value] of Object.entries(controls as Record<string, unknown>)) {
			if (isControlId(key) && typeof value === "boolean") prefs.controls[key] = value;
		}
		if (Array.isArray(record.hiddenNav)) {
			prefs.hiddenNav = record.hiddenNav.filter((path): path is string => typeof path === "string");
		}
		return prefs;
	}

	// Legacy flat shape. Values keep their meaning per key; the check-in flag
	// moves over to the navigation entry it always described.
	for (const [key, value] of Object.entries(record)) {
		if (typeof value !== "boolean") continue;
		if (key === "checkin") {
			if (!value) prefs.hiddenNav = ["/checkins"];
			continue;
		}
		if (isControlId(key)) prefs.controls[key] = value;
	}
	return prefs;
}

function write(prefs: ChromePrefs): void {
	try {
		localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
	} catch {
		/* storage unavailable: the choice just does not persist */
	}
	window.dispatchEvent(new Event(CHANGE_EVENT));
}

export function setTopBarControl(id: TopBarControlId, on: boolean): void {
	const prefs = readChromePrefs();
	prefs.controls[id] = on;
	write(prefs);
}

export function setNavVisible(path: string, visible: boolean): void {
	const prefs = readChromePrefs();
	const hidden = new Set(prefs.hiddenNav);
	if (visible) hidden.delete(path);
	else hidden.add(path);
	prefs.hiddenNav = [...hidden];
	write(prefs);
}

export function resetChromePrefs(): void {
	write(defaultChromePrefs());
}

/** Subscribes to the preference, so every theme's chrome reacts at once. */
export function useChromePrefs(): ChromePrefs {
	const [prefs, setPrefs] = useState<ChromePrefs>(readChromePrefs);
	useEffect(() => {
		const sync = () => setPrefs(readChromePrefs());
		window.addEventListener(CHANGE_EVENT, sync);
		window.addEventListener("storage", sync);
		return () => {
			window.removeEventListener(CHANGE_EVENT, sync);
			window.removeEventListener("storage", sync);
		};
	}, []);
	return prefs;
}

/** Just the control flags — what both theme packages' chrome needs. */
export function useTopBarControls(): Record<TopBarControlId, boolean> {
	return useChromePrefs().controls;
}
