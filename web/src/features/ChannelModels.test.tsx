import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	cleanup,
	fireEvent,
	render,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { ToastProvider } from "../toast";
import { ChannelModelsPanel } from "./ChannelModels";

// One manual-sync channel: gpt-4o adopted and enabled, deepseek-chat adopted
// but parked, grok-2 never adopted. Covers every filter bucket.
const CHANNEL = {
	id: 5,
	name: "WONG",
	base_url: "https://upstream.example",
	models_csv: "",
	group_name: "default",
	priority: 0,
	weight: 100,
	status: "enabled",
	model_sync_mode: "manual",
};

const AUTO_CHANNEL = {
	id: 9,
	name: "auto-chan",
	base_url: "https://auto.example",
	models_csv: "",
	group_name: "default",
	priority: 0,
	weight: 100,
	status: "enabled",
	model_sync_mode: "auto",
};

const MODELS = [
	{ id: 1, channel_id: 5, model_name: "gpt-4o", available: true, source: "probe", latency_ms: 0, checked_at: "2026-09-11T00:00:00Z" },
	{ id: 2, channel_id: 5, model_name: "deepseek-chat", available: true, source: "probe", latency_ms: 0, checked_at: "2026-09-11T00:00:00Z" },
	{ id: 3, channel_id: 5, model_name: "grok-2", available: true, source: "probe", latency_ms: 0, checked_at: "2026-09-11T00:00:00Z" },
];

const ROUTE_OVERVIEWS = [
	{
		route: { id: 11, model_pattern: "gpt-4o", enabled: true },
		members: [
			{
				member: {
					id: 21,
					route_id: 11,
					channel_id: 5,
					enabled: true,
					auto: true,
					manual_override: false,
				},
			},
		],
	},
	{
		route: { id: 12, model_pattern: "deepseek-chat", enabled: true },
		members: [
			{
				member: {
					id: 22,
					route_id: 12,
					channel_id: 5,
					enabled: false,
					auto: true,
					manual_override: false,
				},
			},
		],
	},
	{
		// Shared route: channel 5's parked member cleans up, channel 9's live
		// member keeps the route alive.
		route: { id: 13, model_pattern: "shared-model", enabled: true },
		members: [
			{
				member: {
					id: 23,
					route_id: 13,
					channel_id: 5,
					enabled: true,
					auto: true,
					manual_override: false,
				},
			},
			{
				member: {
					id: 24,
					route_id: 13,
					channel_id: 9,
					enabled: true,
					auto: true,
					manual_override: false,
				},
			},
		],
	},
];

function jsonResponse(body: unknown, status = 200) {
	return new Response(JSON.stringify(body), {
		status,
		headers: { "Content-Type": "application/json" },
	});
}

function stubPanelEndpoints(
	deletions: string[] = [],
	puts: string[] = [],
) {
	return vi.stubGlobal(
		"fetch",
		vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
			const path = String(input);
			const method = (init?.method ?? "GET").toUpperCase();
			if (method === "PUT") {
				puts.push(path);
				return jsonResponse({ ok: true });
			}
			if (method === "DELETE") {
				deletions.push(path);
				return jsonResponse({ ok: true });
			}
			if (path.includes("/admin/channels"))
				return jsonResponse([CHANNEL, AUTO_CHANNEL]);
			if (path.includes("/admin/discovery/models"))
				return jsonResponse(MODELS);
			if (path.includes("/admin/routes/overview"))
				return jsonResponse(ROUTE_OVERVIEWS);
			return jsonResponse({ error: "unexpected" }, 500);
		}),
	);
}

function renderPanel(channelId = 5) {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return render(
		<QueryClientProvider client={queryClient}>
			<I18nProvider>
				<ToastProvider>
					<SessionProvider>
						<ChannelModelsPanel channelId={channelId} />
					</SessionProvider>
				</ToastProvider>
			</I18nProvider>
		</QueryClientProvider>,
	);
}

// Vendor groups start collapsed; model rows only render once their group is
// expanded, so open everything before asserting on row visibility.
async function expandAllGroups() {
	await waitFor(() => {
		expect(
			document.querySelectorAll(".channel-model-group-head").length,
		).toBeGreaterThan(0);
	});
	for (const head of Array.from(
		document.querySelectorAll(".channel-model-group-head"),
	)) {
		if (head.classList.contains("is-collapsed")) fireEvent.click(head);
	}
}

describe("ChannelModelsPanel", () => {
	beforeEach(() => {
		localStorage.clear();
		sessionStorage.clear();
		localStorage.setItem("meta-gateway.locale", "zh-CN");
		localStorage.setItem("meta-gateway.admin-token", "test-token");
	});

	afterEach(() => {
		cleanup();
		vi.unstubAllGlobals();
	});

	it("sync bar reflects the channel's saved sync mode", async () => {
		stubPanelEndpoints();
		renderPanel();

		const manual = await screen.findByRole("button", { name: "按需勾选" });
		expect(manual).toHaveAttribute("aria-pressed", "true");
		expect(
			screen.getByRole("button", { name: "自动同步" }),
		).toHaveAttribute("aria-pressed", "false");
	});

	it("narrows the model list by enabled state", async () => {
		stubPanelEndpoints();
		renderPanel();
		await expandAllGroups();
		expect(screen.getByText("gpt-4o")).toBeInTheDocument();
		expect(screen.getByText("deepseek-chat")).toBeInTheDocument();

		const filter = screen.getByRole("radiogroup", { name: "按启用状态筛选" });
		const [allButton, enabledButton, disabledButton] = within(
			filter,
		).getAllByRole("button");
		// Counts: 4 total (3 discovered + shared-model as a custom binding),
		// 2 enabled (gpt-4o + shared-model), 2 disabled.
		expect(allButton).toHaveTextContent("4");
		expect(enabledButton).toHaveTextContent("2");
		expect(disabledButton).toHaveTextContent("2");
		expect(enabledButton).toHaveAttribute("aria-pressed", "false");

		fireEvent.click(enabledButton!);
		expect(enabledButton).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("gpt-4o")).toBeInTheDocument();
		expect(screen.getByText("shared-model")).toBeInTheDocument();
		expect(screen.queryByText("deepseek-chat")).not.toBeInTheDocument();
		expect(screen.queryByText("grok-2")).not.toBeInTheDocument();

		fireEvent.click(disabledButton!);
		expect(screen.queryByText("gpt-4o")).not.toBeInTheDocument();
		expect(screen.queryByText("shared-model")).not.toBeInTheDocument();
		expect(screen.getByText("deepseek-chat")).toBeInTheDocument();
		expect(screen.getByText("grok-2")).toBeInTheDocument();

		fireEvent.click(allButton!);
		expect(screen.getByText("gpt-4o")).toBeInTheDocument();
		expect(screen.getByText("deepseek-chat")).toBeInTheDocument();
		expect(screen.getByText("grok-2")).toBeInTheDocument();
		expect(screen.getByText("shared-model")).toBeInTheDocument();
	});

	it("cleans up parked bindings and drops emptied routes", async () => {
		const deletions: string[] = [];
		stubPanelEndpoints(deletions);
		renderPanel();

		// One parked binding on this channel: deepseek-chat. gpt-4o and the
		// shared binding are enabled, grok-2 was never adopted.
		fireEvent.click(await screen.findByRole("button", { name: "清理已停用（1）" }));
		const dialog = await screen.findByRole("dialog");
		fireEvent.click(within(dialog).getByRole("button", { name: "删除" }));
		await waitFor(() => {
			expect(
				deletions.filter((d) => d.includes("/admin/route-members/")).length,
			).toBe(1);
		});
		// The emptied route disappears; the shared route (other channel's live
		// member) survives.
		expect(deletions.some((d) => /\/admin\/routes\/12$/.test(d))).toBe(true);
		expect(deletions.some((d) => /\/admin\/routes\/13$/.test(d))).toBe(false);
		expect(
			await screen.findByText(/已清理 1 个绑定，删除 1 条空路由/),
		).toBeInTheDocument();
	});

	it("unchecking on a manual channel deletes the binding and emptied routes", async () => {
		const deletions: string[] = [];
		const puts: string[] = [];
		stubPanelEndpoints(deletions, puts);
		renderPanel();
		await expandAllGroups();

		// gpt-4o is the route's only member: unchecking removes the binding
		// AND the emptied route.
		const gptRow = screen.getByText("gpt-4o").closest("li")!;
		fireEvent.click(within(gptRow).getByRole("checkbox"));
		await waitFor(() => {
			expect(deletions.some((d) => /\/admin\/route-members\/21$/.test(d))).toBe(
				true,
			);
		});
		expect(deletions.some((d) => /\/admin\/routes\/11$/.test(d))).toBe(true);

		// shared-model has another channel's live member: binding removed,
		// route stays.
		const sharedRow = screen.getByText("shared-model").closest("li")!;
		fireEvent.click(within(sharedRow).getByRole("checkbox"));
		await waitFor(() => {
			expect(deletions.some((d) => /\/admin\/route-members\/23$/.test(d))).toBe(
				true,
			);
		});
		expect(deletions.some((d) => /\/admin\/routes\/13$/.test(d))).toBe(false);
		expect(deletions.filter((d) => d.includes("/admin/route-members/")).length).toBe(
			2,
		);
	});

	it("auto-sync channels keep the park semantic on uncheck", async () => {
		const deletions: string[] = [];
		const puts: string[] = [];
		stubPanelEndpoints(deletions, puts);
		renderPanel(9);
		await expandAllGroups();

		// Auto channels: reconcile respects parked members, so unchecking
		// disables instead of deleting (a deleted member would be re-adopted
		// on the next sync).
		const sharedRow = screen.getByText("shared-model").closest("li")!;
		fireEvent.click(within(sharedRow).getByRole("checkbox"));
		await waitFor(() => {
			expect(puts.some((p) => /\/admin\/route-members\/24$/.test(p))).toBe(
				true,
			);
		});
		expect(deletions.length).toBe(0);
	});

	it("shows the empty-state line when nothing matches", async () => {
		stubPanelEndpoints();
		renderPanel();
		await expandAllGroups();

		fireEvent.change(screen.getByPlaceholderText("搜索模型"), {
			target: { value: "no-such-model" },
		});
		expect(screen.getByText("没有符合筛选条件的模型。")).toBeInTheDocument();
		expect(screen.queryByText("gpt-4o")).not.toBeInTheDocument();
	});
});
