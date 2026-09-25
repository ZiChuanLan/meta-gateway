import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { I18nProvider } from "../i18n";
import { ModelPicker, type ModelOption } from "./ModelPicker";

const OPTIONS: ModelOption[] = [
	{
		name: "deepseek-flash",
		channels: [{ id: 1, name: "站点A" }],
		aliasOf: ["deepseek-v4-flash"],
	},
	{
		name: "deepseek-v4-flash",
		channels: [
			{ id: 2, name: "站点B" },
			{ id: 3, name: "站点C" },
			{ id: 4, name: "站点D" },
		],
	},
	{ name: "gpt-4o", channels: [{ id: 1, name: "站点A" }], disabled: true },
];

/** Controlled host so clicks can be judged by what onChange reports. */
function Harness({
	options = OPTIONS,
	initial = [],
	flagMissing = false,
}: {
	options?: ModelOption[];
	initial?: string[];
	flagMissing?: boolean;
}) {
	const [selected, setSelected] = useState(initial);
	return (
		<I18nProvider>
			<ModelPicker
				options={options}
				selected={selected}
				onChange={setSelected}
				flagMissing={flagMissing}
			/>
		</I18nProvider>
	);
}

/** Candidate rows, read from the element that carries the model name. */
function candidateNames(): string[] {
	return Array.from(document.querySelectorAll(".model-picker-name")).map(
		(node) => node.textContent?.trim() ?? "",
	);
}

function row(name: string): HTMLElement {
	const node = Array.from(document.querySelectorAll(".model-picker-item")).find(
		(item) =>
			item.querySelector(".model-picker-name")?.textContent?.trim() === name,
	);
	if (!node) throw new Error(`row not found: ${name}`);
	return node as HTMLElement;
}

describe("ModelPicker", () => {
	beforeEach(() => {
		localStorage.setItem("meta-gateway.locale", "zh-CN");
	});
	afterEach(() => cleanup());

	it("shows which channels can reach each model", () => {
		render(<Harness />);
		expect(row("deepseek-flash").textContent).toContain("站点A");
		expect(row("deepseek-v4-flash").textContent).toContain("站点B");
		// Two of three channels fit; the rest collapse into a +N chip.
		expect(row("deepseek-v4-flash").textContent).toContain("+1");
	});

	it("finds a model by channel name, not just by model name", () => {
		render(<Harness />);
		const search = screen.getByPlaceholderText("搜索模型或渠道…");
		fireEvent.change(search, { target: { value: "站点C" } });
		expect(candidateNames()).toEqual(["deepseek-v4-flash"]);
	});

	it("marks an alias route with the upstream model it renames", () => {
		render(<Harness />);
		expect(row("deepseek-flash").textContent).toContain("别名");
		// The upstream name rides on the badge's tooltip so long model names
		// cannot push the channels chip out of the row.
		expect(
			row("deepseek-flash")
				.querySelector(".model-picker-badge.is-alias")
				?.getAttribute("title"),
		).toContain("deepseek-v4-flash");
		// A route parked in the model catalogue stays visible, but flagged.
		expect(row("gpt-4o").textContent).toContain("路由已禁用");
	});

	it("declares a selected name that no route carries any more", () => {
		render(<Harness initial={["old-name"]} flagMissing />);
		expect(
			screen.getByText(/1 个已选模型当前没有可用路由/),
		).toBeTruthy();
		fireEvent.click(screen.getByRole("button", { name: "移除失效项 (1)" }));
		expect(screen.queryByText(/1 个已选模型当前没有可用路由/)).toBeNull();
		expect(candidateNames().length).toBeGreaterThan(0);
	});

	it("adds every model the current filter shows", () => {
		render(<Harness />);
		fireEvent.change(screen.getByPlaceholderText("搜索模型或渠道…"), {
			target: { value: "站点B" },
		});
		fireEvent.click(screen.getByRole("button", { name: "全选当前 1 项" }));
		expect(document.querySelector(".model-picker-selected")?.textContent).toContain(
			"deepseek-v4-flash",
		);
	});

	it("filters the list by channel", () => {
		render(<Harness />);
		fireEvent.change(screen.getByLabelText("按渠道筛选"), {
			target: { value: "3" },
		});
		expect(candidateNames()).toEqual(["deepseek-v4-flash"]);
	});

	it("folds a group away when its header is clicked", () => {
		render(<Harness />);
		const header = screen.getByRole("button", { name: /DeepSeek/ });
		fireEvent.click(header);
		expect(candidateNames()).toEqual(["gpt-4o"]);
		fireEvent.click(header);
		expect(candidateNames()).toContain("deepseek-flash");
	});
});
