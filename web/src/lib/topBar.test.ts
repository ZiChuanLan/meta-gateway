import { beforeEach, describe, expect, it } from "vitest";
import { TOP_BAR_ITEMS, readTopBarPrefs, setTopBarItem } from "./topBar";

describe("topBar preferences", () => {
	beforeEach(() => {
		localStorage.clear();
	});

	it("shows every entry before anyone touches the picker", () => {
		const prefs = readTopBarPrefs();
		for (const id of TOP_BAR_ITEMS) {
			expect(prefs[id]).toBe(true);
		}
	});

	it("remembers one entry at a time, leaving the others alone", () => {
		setTopBarItem("checkin", false);
		expect(readTopBarPrefs().checkin).toBe(false);
		expect(readTopBarPrefs().theme).toBe(true);
		setTopBarItem("theme", false);
		expect(readTopBarPrefs()).toMatchObject({ checkin: false, theme: false, language: true });
		setTopBarItem("checkin", true);
		expect(readTopBarPrefs().checkin).toBe(true);
	});

	it("ignores a hand-edited value instead of throwing", () => {
		// The value lives in localStorage, so it is one devtools edit away from
		// being something else entirely — and it is read during render.
		for (const raw of ["{not json", "42", `["checkin"]`, `{"nope":false}`]) {
			localStorage.setItem("meta-gateway.topbar-items", raw);
			expect(readTopBarPrefs()).toMatchObject({ checkin: true, update: true, theme: true, language: true });
		}
		// Known keys with wrong value types fall back per key, not wholesale.
		localStorage.setItem("meta-gateway.topbar-items", `{"checkin":"no","theme":false}`);
		expect(readTopBarPrefs()).toMatchObject({ checkin: true, theme: false });
	});
});
