import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { I18nProvider, translate } from "../../i18n";
import { HookSection } from "./HookSection";
import type { PluginHookStatus } from "../../api/types";

const en = (key: string, vars?: Record<string, string | number>) =>
	translate("en", key, vars);

const routeHook: PluginHookStatus = {
	plugin_id: "jev-router",
	plugin_name: "Jev 自动选路",
	point: "route",
	path: "/hooks/route",
	match_models: ["auto"],
	timeout_ms: 2000,
	priority: 10,
};

function renderSection(hooks: PluginHookStatus[]) {
	return render(
		<I18nProvider>
			<HookSection hooks={hooks} />
		</I18nProvider>,
	);
}

describe("HookSection", () => {
	beforeEach(() => {
		localStorage.setItem("meta-gateway.locale", "en");
	});

	afterEach(() => {
		cleanup();
	});

	it("renders nothing when no plugin declares a hook", () => {
		const { container } = renderSection([]);
		expect(container.firstChild).toBeNull();
	});

	it("names the plugin, the models it matches and its timeout", () => {
		renderSection([routeHook]);
		expect(screen.getByText(en("store.hooks"))).toBeTruthy();
		expect(screen.getByText("Jev 自动选路")).toBeTruthy();
		expect(screen.getByText("auto")).toBeTruthy();
		expect(screen.getByText(en("store.hookTimeout", { ms: 2000 }))).toBeTruthy();
	});

	it("states the trust boundary rather than implying it", () => {
		renderSection([routeHook]);
		expect(screen.getByText(en("store.hooksWarning"))).toBeTruthy();
	});

	it("flags a tripped breaker", () => {
		renderSection([{ ...routeHook, tripped: true }]);
		expect(screen.getByText(en("store.hookTripped"))).toBeTruthy();
	});

	it("falls back to the plugin id when no name was declared", () => {
		renderSection([{ ...routeHook, plugin_name: undefined }]);
		expect(screen.getByText("jev-router")).toBeTruthy();
	});

	it("renders an unknown point by its raw name instead of a translation key", () => {
		renderSection([{ ...routeHook, point: "stream" }]);
		expect(screen.getByText("stream")).toBeTruthy();
		expect(screen.queryByText(/^store\./)).toBeNull();
	});

	it("lists every model a hook matches", () => {
		renderSection([{ ...routeHook, match_models: ["auto", "smart", "cheap-*"] }]);
		expect(screen.getByText("auto, smart, cheap-*")).toBeTruthy();
	});
});
