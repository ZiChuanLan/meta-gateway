import { beforeEach, describe, expect, it } from "vitest";
import { readHiddenPlugins, setPluginNavHidden } from "./pluginNav";

describe("pluginNav", () => {
	beforeEach(() => {
		localStorage.clear();
	});

	it("starts with everything visible", () => {
		expect([...readHiddenPlugins()]).toEqual([]);
	});

	it("hides and shows one plugin without touching the others", () => {
		setPluginNavHidden("jev-router", true);
		setPluginNavHidden("other-plugin", true);
		expect([...readHiddenPlugins()].sort()).toEqual(["jev-router", "other-plugin"]);
		setPluginNavHidden("jev-router", false);
		expect([...readHiddenPlugins()]).toEqual(["other-plugin"]);
	});

	it("ignores a hand-edited value instead of throwing", () => {
		// The value lives in localStorage, so it is one devtools edit away from
		// being something else entirely — and it is read during render.
		for (const raw of ["{not json", `{"a":1}`, "42", `[1,2]`, `["ok",null]`]) {
			localStorage.setItem("meta-gateway.plugin-nav-hidden", raw);
			expect([...readHiddenPlugins()]).toEqual(raw === `["ok",null]` ? ["ok"] : []);
		}
	});
});
