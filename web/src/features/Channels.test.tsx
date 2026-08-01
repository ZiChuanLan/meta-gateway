import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	cleanup,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { ToastProvider } from "../toast";
import { Channels, capabilityFlags, channelHealth } from "./Channels";
import type { ChannelOverview } from "../api/types";

function renderChannels() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return render(
		<QueryClientProvider client={queryClient}>
			<I18nProvider>
				<ToastProvider>
					<SessionProvider>
						<MemoryRouter initialEntries={["/"]}>
							<Routes>
								<Route path="/" element={<Channels />} />
							</Routes>
						</MemoryRouter>
					</SessionProvider>
				</ToastProvider>
			</I18nProvider>
		</QueryClientProvider>,
	);
}

function jsonResponse(body: unknown, status = 200) {
	return new Response(JSON.stringify(body), {
		status,
		headers: { "Content-Type": "application/json" },
	});
}

describe("Channels two-phase create", () => {
	beforeEach(() => {
		localStorage.clear();
		sessionStorage.clear();
		localStorage.setItem("meta-gateway.locale", "en");
		sessionStorage.setItem("meta-gateway.admin-token", "test-token");
	});

	afterEach(() => {
		cleanup();
		vi.unstubAllGlobals();
	});

	it("saves the connection before verify and retries without re-creating", async () => {
		const createSite = vi.fn(async () =>
			jsonResponse({
				id: 1,
				name: "api.example.com",
				base_url: "https://api.example.com",
				platform: "openai-compatible",
				status: "enabled",
			}),
		);
		const createCredential = vi.fn(async () =>
			jsonResponse({
				id: 11,
				site_id: 1,
				kind: "api_key",
				status: "enabled",
				has_secret: true,
			}),
		);
		const createChannel = vi.fn(async () =>
			jsonResponse({
				id: 21,
				name: "api.example.com",
				site_id: 1,
				credential_id: 11,
				base_url: "",
				group_name: "default",
				priority: 0,
				weight: 100,
				status: "enabled",
				type_hint: "openai-compatible",
			}),
		);
		let refreshAttempts = 0;
		const refreshChannel = vi.fn(async () => {
			refreshAttempts += 1;
			if (refreshAttempts === 1) {
				return jsonResponse({ error: "upstream unauthorized" }, 502);
			}
			return jsonResponse({
				channel_id: 21,
				models: [{ id: "gpt-test" }],
				created_routes: 1,
				latency_ms: 12,
			});
		});

		const overviews: unknown[] = [];
		vi.stubGlobal(
			"fetch",
			vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
				const path = String(input).split("?")[0];
				const method = (init?.method ?? "GET").toUpperCase();
				if (path === "/admin/channels/overview" && method === "GET") {
					return jsonResponse(overviews);
				}
				if (path === "/admin/sites" && method === "GET") {
					return jsonResponse([]);
				}
				if (path === "/admin/sites" && method === "POST") {
					const body = JSON.parse(String(init?.body ?? "{}"));
					overviews.length = 0;
					const response = await createSite();
					const site = await response.clone().json();
					// Keep list empty until channel exists; channel create fills overview.
					void site;
					void body;
					return response;
				}
				if (path === "/admin/sites/1/credentials" && method === "POST") {
					return createCredential();
				}
				if (path === "/admin/channels" && method === "POST") {
					const response = await createChannel();
					const channel = await response.clone().json();
					overviews.push({
						channel,
						credential_kind: "api_key",
						checkin_enabled: false,
						has_user_credential: false,
						has_platform_user_id: false,
						has_api_key: true,
						site_usable: true,
						credential_usable: true,
						model_count: 0,
						cooling_member_count: 0,
						failure_count: 0,
						last_error: "",
						last_checked_at: null,
						last_latency_ms: 0,
					});
					return response;
				}
				if (path === "/admin/discovery/channels/21/refresh" && method === "POST") {
					const response = await refreshChannel();
					if (response.ok) {
						const current = overviews[0] as {
							model_count: number;
							channel: { id: number };
						};
						current.model_count = 1;
					}
					return response;
				}
				return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
			}),
		);

		renderChannels();
		expect(
			await screen.findByRole("heading", { name: "Connections" }),
		).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "Add connection" }));
		fireEvent.change(screen.getByPlaceholderText("https://api.example.com"), {
			target: { value: "https://api.example.com" },
		});
		const secretInput = document.querySelector(
			'input[type="password"]',
		) as HTMLInputElement;
		fireEvent.change(secretInput, { target: { value: "sk-test" } });
		fireEvent.click(screen.getByRole("button", { name: "Save & verify" }));

		await waitFor(() => expect(createChannel).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(refreshChannel).toHaveBeenCalledTimes(1));
		expect(
			await screen.findByText(/was saved, but model sync failed/i),
		).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "Retry verify" }));
		await waitFor(() => expect(refreshChannel).toHaveBeenCalledTimes(2));
		await waitFor(() =>
			expect(
				screen.getByText(/saved and fetched 1 models/i),
			).toBeInTheDocument(),
		);
		expect(createSite).toHaveBeenCalledTimes(1);
		expect(createCredential).toHaveBeenCalledTimes(1);
		expect(createChannel).toHaveBeenCalledTimes(1);
	});
});


describe("capabilityFlags", () => {
	it("marks access-token-only connections as check-in ready and missing API key", () => {
		const flags = capabilityFlags({
			channel: {
				id: 1,
				name: "demo",
				base_url: "",
				models_csv: "",
				group_name: "default",
				priority: 0,
				weight: 100,
				status: "enabled",
				created_at: "",
				updated_at: "",
			},
			credential_kind: "access_token",
			checkin_enabled: true,
			has_user_credential: true,
			has_platform_user_id: true,
			has_api_key: false,
			last_probe_ok: true,
			site_usable: true,
			credential_usable: true,
			model_count: 0,
			last_latency_ms: 0,
			route_count: 0,
			enabled_member_count: 0,
			cooling_member_count: 0,
			failure_count: 0,
		});
		expect(flags.checkinCapable).toBe(true);
		expect(flags.checkinOff).toBe(false);
		expect(flags.checkinScheduled).toBe(true);
		expect(flags.missingAPIKey).toBe(true);
		expect(flags.modelsReady).toBe(false);
		expect(channelHealth({
			channel: {
				id: 1,
				name: "demo",
				base_url: "",
				models_csv: "",
				group_name: "default",
				priority: 0,
				weight: 100,
				status: "enabled",
				created_at: "",
				updated_at: "",
			},
			checkin_enabled: false,
			has_user_credential: true,
			has_platform_user_id: false,
			has_api_key: false,
			site_usable: true,
			credential_usable: true,
			model_count: 0,
			last_latency_ms: 0,
			route_count: 0,
			enabled_member_count: 0,
			cooling_member_count: 0,
			failure_count: 0,
		})).toBe("unverified");
	});

	it("does not flag missing API key until the access token passes verification", () => {
		const base: ChannelOverview = {
			channel: {
				id: 9,
				name: "demo",
				base_url: "",
				models_csv: "",
				group_name: "default",
				priority: 0,
				weight: 100,
				status: "enabled",
				created_at: "",
				updated_at: "",
			},
			credential_kind: "access_token",
			checkin_enabled: true,
			has_user_credential: true,
			has_platform_user_id: true,
			has_api_key: false,
			site_usable: true,
			credential_usable: true,
			model_count: 0,
			last_latency_ms: 0,
			route_count: 0,
			enabled_member_count: 0,
			cooling_member_count: 0,
			failure_count: 0,
		};
		// Never probed → no verdict yet.
		expect(capabilityFlags({ ...base }).missingAPIKey).toBe(false);
		// Probe failed → token is not effective, do not blame a missing API key.
		expect(capabilityFlags({ ...base, last_probe_ok: false }).missingAPIKey).toBe(
			false,
		);
		// Probe passed → now the missing API key is the actionable gap.
		expect(capabilityFlags({ ...base, last_probe_ok: true }).missingAPIKey).toBe(
			true,
		);
	});

	it("marks check-in as needing user id when token exists without platform_user_id", () => {
		const flags = capabilityFlags({
			channel: {
				id: 3,
				name: "demo",
				base_url: "",
				models_csv: "",
				group_name: "default",
				priority: 0,
				weight: 100,
				status: "enabled",
				created_at: "",
				updated_at: "",
			},
			credential_kind: "access_token",
			checkin_enabled: true,
			has_user_credential: true,
			has_platform_user_id: false,
			has_api_key: false,
			site_usable: true,
			credential_usable: true,
			model_count: 0,
			last_latency_ms: 0,
			route_count: 0,
			enabled_member_count: 0,
			cooling_member_count: 0,
			failure_count: 0,
		});
		expect(flags.checkinCapable).toBe(false);
		expect(flags.checkinNeedsUserID).toBe(true);
		expect(flags.checkinScheduled).toBe(false);
	});

	it("shows check-in schedule off when user token exists but checkin_enabled is false", () => {
		const flags = capabilityFlags({
			channel: {
				id: 2,
				name: "demo",
				base_url: "",
				models_csv: "",
				group_name: "default",
				priority: 0,
				weight: 100,
				status: "enabled",
				created_at: "",
				updated_at: "",
			},
			credential_kind: "api_key",
			checkin_enabled: false,
			has_user_credential: true,
			has_platform_user_id: true,
			has_api_key: true,
			site_usable: true,
			credential_usable: true,
			model_count: 3,
			last_latency_ms: 10,
			route_count: 1,
			enabled_member_count: 1,
			cooling_member_count: 0,
			failure_count: 0,
		});
		expect(flags.checkinCapable).toBe(true);
		expect(flags.checkinScheduled).toBe(false);
		expect(flags.checkinOff).toBe(true);
		expect(flags.missingAPIKey).toBe(false);
		expect(flags.noUserToken).toBe(false);
	});
});
