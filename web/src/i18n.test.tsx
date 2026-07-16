import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { I18nProvider, detectLocale, translate, useI18n } from "./i18n";

const wrapper = ({ children }: { children: ReactNode }) => (
	<I18nProvider>{children}</I18nProvider>
);

describe("i18n", () => {
	beforeEach(() => {
		localStorage.clear();
		document.documentElement.lang = "en";
	});

	it("detects chinese browser language", () => {
		vi.stubGlobal("navigator", { language: "zh-CN" });
		expect(detectLocale()).toBe("zh-CN");
		vi.unstubAllGlobals();
	});

	it("prefers saved locale over browser language", () => {
		localStorage.setItem("meta-gateway.locale", "en");
		vi.stubGlobal("navigator", { language: "zh-CN" });
		expect(detectLocale()).toBe("en");
		vi.unstubAllGlobals();
	});

	it("switches locale, persists, and updates document language", () => {
		const { result } = renderHook(() => useI18n(), { wrapper });
		expect(result.current.t("app.nav.dashboard")).toBeTruthy();
		act(() => result.current.setLocale("zh-CN"));
		expect(result.current.locale).toBe("zh-CN");
		expect(localStorage.getItem("meta-gateway.locale")).toBe("zh-CN");
		expect(document.documentElement.lang).toBe("zh-CN");
		expect(result.current.t("app.nav.dashboard")).toBe("仪表盘");
		expect(
			result.current.t("assets.refreshResult", { models: 2, routes: 1 }),
		).toContain("2");
	});

	it("falls back to english for missing keys and interpolates values", () => {
		expect(translate("zh-CN", "missing.key")).toBe("missing.key");
		expect(translate("en", "common.attempt", { n: 3 })).toBe("Attempt 3");
		expect(translate("zh-CN", "common.attempt", { n: 3 })).toBe("第 3 次");
	});
});
