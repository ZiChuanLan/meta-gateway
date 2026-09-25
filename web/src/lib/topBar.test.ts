import { beforeEach, describe, expect, it } from "vitest";
import {
	TOP_BAR_CONTROLS,
	defaultChromePrefs,
	readChromePrefs,
	resetChromePrefs,
	setNavVisible,
	setTopBarControl,
} from "./topBar";

const STORAGE_KEY = "meta-gateway.topbar-items";

describe("chrome preferences", () => {
	beforeEach(() => {
		localStorage.clear();
	});

	it("shows every entry before anyone touches the picker", () => {
		const prefs = readChromePrefs();
		for (const id of TOP_BAR_CONTROLS) {
			expect(prefs.controls[id]).toBe(true);
		}
		expect(prefs.hiddenNav).toEqual([]);
	});

	it("remembers one entry at a time, leaving the others alone", () => {
		setNavVisible("/checkins", false);
		expect(readChromePrefs().hiddenNav).toEqual(["/checkins"]);
		expect(readChromePrefs().controls.theme).toBe(true);

		setTopBarControl("theme", false);
		const prefs = readChromePrefs();
		expect(prefs.controls.theme).toBe(false);
		expect(prefs.controls.language).toBe(true);
		expect(prefs.hiddenNav).toEqual(["/checkins"]);

		setNavVisible("/checkins", true);
		expect(readChromePrefs().hiddenNav).toEqual([]);
	});

	it("restores every entry on reset", () => {
		setNavVisible("/workbench", false);
		setTopBarControl("search", false);
		resetChromePrefs();
		expect(readChromePrefs()).toEqual(defaultChromePrefs());
	});

	it("ignores a hand-edited value instead of throwing", () => {
		// The value lives in localStorage, so it is one devtools edit away from
		// being something else entirely — and it is read during render.
		for (const raw of [
			"{not json",
			"42",
			`["checkin"]`,
			`{"controls":{"nope":false},"hiddenNav":"checkins"}`,
		]) {
			localStorage.setItem(STORAGE_KEY, raw);
			expect(readChromePrefs()).toEqual(defaultChromePrefs());
		}
		// Known keys with wrong value types fall back per key, not wholesale.
		localStorage.setItem(
			STORAGE_KEY,
			`{"controls":{"theme":"no","language":false},"hiddenNav":[7,"/logs"]}`,
		);
		const prefs = readChromePrefs();
		expect(prefs.controls.theme).toBe(true);
		expect(prefs.controls.language).toBe(false);
		expect(prefs.hiddenNav).toEqual(["/logs"]);
	});

	// The stored shape predates the navigation flags: it was a flat map where
	// `checkin` was the one entry that described a page. Upgrading it keeps both
	// halves of the operator's choice instead of resetting the panel.
	it("upgrades the flat check-in shape", () => {
		localStorage.setItem(
			STORAGE_KEY,
			`{"checkin":false,"update":true,"theme":false,"language":true}`,
		);
		const prefs = readChromePrefs();
		expect(prefs.hiddenNav).toEqual(["/checkins"]);
		expect(prefs.controls).toEqual({
			search: true,
			update: true,
			theme: false,
			language: true,
		});

		// An operator who kept check-in visible keeps it visible.
		localStorage.setItem(STORAGE_KEY, `{"checkin":true}`);
		expect(readChromePrefs().hiddenNav).toEqual([]);
	});
});
