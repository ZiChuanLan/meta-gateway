import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { ErrorBoundary } from "./ErrorBoundary";

function Boom(): never {
	throw new Error("boom: null is not an array");
}

describe("ErrorBoundary", () => {
	beforeEach(() => {
		localStorage.setItem("meta-gateway.locale", "zh-CN");
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("renders children when nothing throws", () => {
		render(
			<I18nProvider>
				<ErrorBoundary>
					<p>页面内容</p>
				</ErrorBoundary>
			</I18nProvider>,
		);
		expect(screen.getByText("页面内容")).toBeTruthy();
	});

	// The failure mode this exists for: a thrown render error unmounted the
	// whole tree and left a blank page with no way back.
	it("shows a recoverable card instead of a blank page", () => {
		vi.spyOn(console, "error").mockImplementation(() => undefined);
		render(
			<I18nProvider>
				<ErrorBoundary>
					<Boom />
				</ErrorBoundary>
			</I18nProvider>,
		);
		expect(screen.getByRole("alert")).toBeTruthy();
		expect(screen.getByText("控制台遇到了意外错误")).toBeTruthy();
		expect(screen.getByText(/boom: null is not an array/)).toBeTruthy();
		expect(screen.getByRole("button", { name: "重新加载" })).toBeTruthy();
	});
});
