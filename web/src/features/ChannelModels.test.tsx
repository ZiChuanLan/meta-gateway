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
];

function jsonResponse(body: unknown, status = 200) {
	return new Response(JSON.stringify(body), {
		status,
		headers: { "Content-Type": "application/json" },
	});
}

function stubPanelEndpoints() {
	return vi.stubGlobal(
		"fetch",
		vi.fn(async (input: RequestInfo | URL) => {
			const path = String(input);
			if (path.includes("/admin/channels")) return jsonResponse([CHANNEL]);
			if (path.includes("/admin/discovery/models"))
				return jsonResponse(MODELS);
			if (path.includes("/admin/routes/overview"))
				return jsonResponse(ROUTE_OVERVIEWS);
			return jsonResponse({ error: "unexpected" }, 500);
		}),
	);
}

function renderPanel() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return render(
		<QueryClientProvider client={queryClient}>
			<I18nProvider>
				<ToastProvider>
					<SessionProvider>
						<ChannelModelsPanel channelId={5} />
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
		// Counts: 3 total, 1 enabled (gpt-4o), 2 disabled (parked + never adopted).
		expect(allButton).toHaveTextContent("3");
		expect(enabledButton).toHaveTextContent("1");
		expect(disabledButton).toHaveTextContent("2");
		expect(enabledButton).toHaveAttribute("aria-pressed", "false");

		fireEvent.click(enabledButton!);
		expect(enabledButton).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("gpt-4o")).toBeInTheDocument();
		expect(screen.queryByText("deepseek-chat")).not.toBeInTheDocument();
		expect(screen.queryByText("grok-2")).not.toBeInTheDocument();

		fireEvent.click(disabledButton!);
		expect(screen.queryByText("gpt-4o")).not.toBeInTheDocument();
		expect(screen.getByText("deepseek-chat")).toBeInTheDocument();
		expect(screen.getByText("grok-2")).toBeInTheDocument();

		fireEvent.click(allButton!);
		expect(screen.getByText("gpt-4o")).toBeInTheDocument();
		expect(screen.getByText("deepseek-chat")).toBeInTheDocument();
		expect(screen.getByText("grok-2")).toBeInTheDocument();
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
