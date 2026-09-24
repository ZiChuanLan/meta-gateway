import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { I18nProvider } from "../../i18n";
import {
	ModelGroupsField,
	formatModelGroups,
	parseModelGroups,
	suggestModelGroups,
} from "./ModelGroupsField";

describe("parseModelGroups", () => {
	it("reads the shape the editor writes", () => {
		const groups = parseModelGroups(
			`[{"name":"code","hint":"写代码","models":["a","b"]},{"name":"simple","models":["c"]}]`,
		);
		expect(groups).toEqual([
			{ name: "code", hint: "写代码", models: ["a", "b"] },
			{ name: "simple", hint: "", models: ["c"] },
		]);
	});

	it("returns nothing rather than throwing on a value it cannot read", () => {
		// A damaged field must not take the whole settings panel down with it.
		for (const raw of ["", "   ", "code (写代码): a", "not json", "{}", "[1,2]", "{oops"]) {
			expect(parseModelGroups(raw)).toEqual([]);
		}
	});

	it("keeps an unnamed entry and ignores models that are not strings", () => {
		const groups = parseModelGroups(
			`[{"models":["a"]},{"name":"ok","models":["a",42,null,""]}]`,
		);
		expect(groups).toEqual([
			{ name: "", hint: "", models: ["a"] },
			{ name: "ok", hint: "", models: ["a"] },
		]);
	});

	it("is the inverse of the formatter", () => {
		// The value is a controlled prop: anything parse drops, the editor loses
		// on the next render. Round-tripping has to be lossless for every state
		// the editor can put itself in, including a row that is still empty.
		const shapes = [
			[],
			[{ name: "", hint: "", models: [] }],
			[{ name: "code", hint: "写代码", models: ["a", "b"] }],
			[
				{ name: "code", hint: "", models: ["a"] },
				{ name: "", hint: "", models: [] },
			],
		];
		for (const shape of shapes) {
			expect(parseModelGroups(formatModelGroups(shape))).toEqual(shape);
		}
	});
});

describe("formatModelGroups", () => {
	it("keeps a half-finished scenario so the editor can hold it", () => {
		// Dropping it here is what made the Add-scenario button look broken: the
		// row was created and immediately erased on the next render.
		const raw = formatModelGroups([{ name: "", hint: "", models: [] }]);
		expect(JSON.parse(raw)).toEqual([{ name: "", hint: "", models: [] }]);
	});

	it("trims the text fields", () => {
		const raw = formatModelGroups([{ name: "  code  ", hint: "  写代码  ", models: ["a"] }]);
		expect(JSON.parse(raw)).toEqual([{ name: "code", hint: "写代码", models: ["a"] }]);
	});

	it("round-trips through the parser", () => {
		const groups = [{ name: "code", hint: "写代码", models: ["a", "b"] }];
		expect(parseModelGroups(formatModelGroups(groups))).toEqual(groups);
	});
});

describe("suggestModelGroups", () => {
	it("suggests the five work kinds as empty skeletons", () => {
		const suggestions = suggestModelGroups();
		expect(suggestions.map((group) => group.name)).toEqual([
			"code-simple",
			"code-complex",
			"thinking",
			"bugfix",
			"chat",
		]);
		expect(suggestions.every((group) => group.models.length === 0)).toBe(true);
		expect(suggestions[0]?.hint).not.toBe("");
	});
});

function renderField(
	value: string,
	models: string[],
	onChange: (next: string) => void = () => {},
) {
	return render(
		<I18nProvider>
			<ModelGroupsField value={value} onChange={onChange} models={models} />
		</I18nProvider>,
	);
}

describe("ModelGroupsField", () => {
	beforeEach(() => {
		localStorage.setItem("meta-gateway.locale", "en");
	});
	afterEach(() => cleanup());

	it("offers only the models the gateway routes", () => {
		renderField(`[{"name":"code","hint":"","models":[]}]`, ["gpt-5", "deepseek-chat"]);
		const picker = screen.getByLabelText("Add model") as HTMLSelectElement;
		const options = Array.from(picker.querySelectorAll("option")).map((o) => o.value);
		expect(options).toEqual(["", "gpt-5", "deepseek-chat"]);
	});

	it("adds the picked model to the scenario it was picked in", () => {
		let next = "";
		renderField(
			`[{"name":"code","hint":"","models":["gpt-6"]},{"name":"simple","hint":"","models":[]}]`,
			["gpt-5", "gpt-6"],
			(value) => {
				next = value;
			},
		);
		const pickers = screen.getAllByLabelText("Add model");
		fireEvent.change(pickers[1]!, { target: { value: "gpt-5" } });
		expect(JSON.parse(next)).toEqual([
			{ name: "code", hint: "", models: ["gpt-6"] },
			{ name: "simple", hint: "", models: ["gpt-5"] },
		]);
	});

	it("lets an empty scenario exist so it can be filled in", () => {
		let next = "";
		renderField("[]", ["gpt-5"], (value) => {
			next = value;
		});
		fireEvent.click(screen.getByRole("button", { name: "Add scenario" }));
		expect(JSON.parse(next)).toEqual([{ name: "", hint: "", models: [] }]);
	});

	it("never offers a model the scenario already has", () => {
		renderField(`[{"name":"code","hint":"","models":["gpt-5"]}]`, ["gpt-5", "gpt-6"]);
		const picker = screen.getByLabelText("Add model") as HTMLSelectElement;
		const options = Array.from(picker.querySelectorAll("option")).map((o) => o.value);
		expect(options).toEqual(["", "gpt-6"]);
	});

	it("removes a model chip", () => {
		let next = "";
		renderField(`[{"name":"code","hint":"","models":["gpt-5","gpt-6"]}]`, ["gpt-5", "gpt-6"], (value) => {
			next = value;
		});
		fireEvent.click(screen.getByLabelText("Remove gpt-5"));
		expect(JSON.parse(next)[0].models).toEqual(["gpt-6"]);
	});

	it("removes a scenario", () => {
		let next = "";
		renderField(
			`[{"name":"code","hint":"","models":["gpt-5"]},{"name":"simple","hint":"","models":["gpt-5"]}]`,
			["gpt-5"],
			(value) => {
				next = value;
			},
		);
		fireEvent.click(screen.getAllByLabelText("Remove scenario")[0]!);
		const parsed = JSON.parse(next);
		expect(parsed).toHaveLength(1);
		expect(parsed[0].name).toBe("simple");
	});

	it("says so when the gateway has no routable models", () => {
		renderField("[]", []);
		expect(screen.getByText(/No routable models yet/)).toBeTruthy();
	});

	it("offers a one-click preset start from the empty state", () => {
		let next = "";
		renderField("[]", ["claude-sonnet-4"], (value) => {
			next = value;
		});
		fireEvent.click(
			screen.getByRole("button", { name: "Start with suggested scenarios" }),
		);
		const groups = JSON.parse(next);
		expect(groups.map((group: { name: string }) => group.name)).toEqual([
			"code-simple",
			"code-complex",
			"thinking",
			"bugfix",
			"chat",
		]);
		// Models are the operator's call: skeletons come empty.
		expect(groups.every((group: { models: string[] }) => group.models.length === 0)).toBe(true);
	});

	it("does not offer presets when the editor already has scenarios", () => {
		renderField(`[{"name":"mine","hint":"","models":["gpt-5"]}]`, ["gpt-5"]);
		expect(
			screen.queryByRole("button", { name: "Start with suggested scenarios" }),
		).toBeNull();
	});
});
