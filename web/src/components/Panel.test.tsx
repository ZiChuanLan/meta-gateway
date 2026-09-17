import type { ReactElement } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { Panel } from "./ui";

afterEach(() => {
	cleanup();
	sessionStorage.clear();
});

function renderPanel(node: ReactElement) {
	return render(<I18nProvider>{node}</I18nProvider>);
}

describe("Panel", () => {
	it("leaves a plain panel without a disclosure handle", () => {
		const { container } = renderPanel(
			<Panel title="Throughput">
				<p>body</p>
			</Panel>,
		);
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		expect(screen.getByText("body")).toBeVisible();
		expect(container.querySelector(".panel-body")).toBeNull();
	});

	it("folds a collapsible panel that starts closed and keeps its summary", () => {
		renderPanel(
			<Panel
				title="Sticky sessions"
				collapsible
				defaultOpen={false}
				storageKey="test.sticky"
				summary={<strong>3 bound</strong>}
			>
				<p>bindings</p>
			</Panel>,
		);
		const toggle = screen.getByRole("button", { name: "Sticky sessions" });
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByText("bindings")).not.toBeVisible();
		// The folded header still carries the numbers worth glancing at.
		expect(screen.getByText("3 bound")).toBeVisible();
		// Mounting never persists; only an explicit toggle does, so a changed
		// default still reaches operators who never touched the handle.
		expect(sessionStorage.getItem("panel.test.sticky")).toBeNull();
	});

	it("expands on click and remembers the choice on the next mount", () => {
		const { unmount } = renderPanel(
			<Panel
				title="Sticky sessions"
				collapsible
				defaultOpen={false}
				storageKey="test.sticky"
				summary={<strong>3 bound</strong>}
			>
				<p>bindings</p>
			</Panel>,
		);
		const toggle = screen.getByRole("button", { name: "Sticky sessions" });
		fireEvent.click(toggle);
		expect(toggle).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("bindings")).toBeVisible();
		expect(screen.queryByText("3 bound")).not.toBeInTheDocument();
		expect(sessionStorage.getItem("panel.test.sticky")).toBe("1");

		unmount();
		renderPanel(
			<Panel
				title="Sticky sessions"
				collapsible
				defaultOpen={false}
				storageKey="test.sticky"
				summary={<strong>3 bound</strong>}
			>
				<p>bindings</p>
			</Panel>,
		);
		// defaultOpen is only the seed; the stored choice wins.
		expect(
			screen.getByRole("button", { name: "Sticky sessions" }),
		).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("bindings")).toBeVisible();
	});

	it("falls back to defaultOpen when storage is unavailable", () => {
		const getItem = vi
			.spyOn(Storage.prototype, "getItem")
			.mockImplementation(() => {
				throw new Error("storage disabled");
			});
		const setItem = vi
			.spyOn(Storage.prototype, "setItem")
			.mockImplementation(() => {
				throw new Error("storage disabled");
			});
		try {
			renderPanel(
				<Panel title="Sticky sessions" collapsible storageKey="test.sticky">
					<p>bindings</p>
				</Panel>,
			);
			const toggle = screen.getByRole("button", { name: "Sticky sessions" });
			expect(toggle).toHaveAttribute("aria-expanded", "true");
			fireEvent.click(toggle);
			expect(toggle).toHaveAttribute("aria-expanded", "false");
		} finally {
			getItem.mockRestore();
			setItem.mockRestore();
		}
	});
});
