import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	act,
	cleanup,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { ENTRANCE_CHARGE_MS, ENTRANCE_REVEAL_MS } from "./lib/entranceMotion";
import { I18nProvider } from "./i18n";
import { SessionProvider } from "./session";
import { ToastProvider } from "./toast";

function renderApp(initialEntries: string[] = ["/"]) {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return render(
		<QueryClientProvider client={queryClient}>
			<I18nProvider>
				<ToastProvider>
					<SessionProvider>
						{/* basename is production's (main.tsx). A test router mounted at "/"
						    cannot see the difference between a route-relative target
						    ("/plugins/x") and a mistaken full-path one ("{BASENAME}/plugins/x"),
						    which is exactly how a link can pass here and bounce off the
						    catch-all in production. */}
						<MemoryRouter
							basename={BASENAME}
							initialEntries={initialEntries.map((entry) => `${BASENAME}${entry}`)}
						>
							<App />
							<LocationProbe />
						</MemoryRouter>
					</SessionProvider>
				</ToastProvider>
			</I18nProvider>
		</QueryClientProvider>,
	);
}

/** Mirrors `basename` in main.tsx — the console is served under /console. */
const BASENAME = "/console";

function LocationProbe() {
	const location = useLocation();
	return <span data-testid="location">{location.pathname}</span>;
}

function jsonResponse(body: unknown, status = 200) {
	return new Response(JSON.stringify(body), {
		status,
		headers: { "Content-Type": "application/json" },
	});
}

async function flushAsyncWork() {
	await act(async () => {
		await Promise.resolve();
		await Promise.resolve();
		await Promise.resolve();
	});
}

function stubAdminFetch(overrides: {
	modules?: unknown[];
	hooks?: unknown[];
	routes?: unknown[];
} = {}) {
	return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
		const path = String(input).split("?")[0];
		const method = init?.method ?? "GET";
		if (method === "POST" && path === "/admin/session") {
			return jsonResponse({ session_token: "mg-sess.test" });
		}
		if (path === "/readyz") return new Response(null, { status: 200 });
		if (path === "/admin/plugins/status") {
			return jsonResponse(
				overrides.modules ?? [
					{
						id: "exchange",
						name: "Exchange",
						version: "0.0.0",
						kind: "addon",
						installed: true,
						enabled: true,
						can_toggle: true,
					},
				],
			);
		}
		if (path === "/admin/plugins/hooks") {
			return jsonResponse({ hooks: overrides.hooks ?? [] });
		}
		if (
			path === "/admin/sites" ||
			path === "/admin/channels" ||
			path === "/admin/channels/overview" ||
			path === "/admin/discovery/models" ||
			path === "/admin/downstream-keys" ||
			path === "/admin/proxy-logs" ||
			path === "/admin/model-capabilities"
		) {
			return jsonResponse([]);
		}
		if (path === "/admin/routes/overview") {
			return jsonResponse(overrides.routes ?? []);
		}
		return jsonResponse({ error: `unexpected GET ${path}` }, 500);
	});
}

describe("channel-first shell", () => {
	beforeEach(() => {
		localStorage.clear();
		sessionStorage.clear();
		localStorage.setItem("meta-gateway.locale", "en");
		vi.stubGlobal(
			"matchMedia",
			vi.fn().mockImplementation(() => ({
				matches: false,
				media: "(prefers-reduced-motion: reduce)",
				onchange: null,
				addEventListener: vi.fn(),
				removeEventListener: vi.fn(),
				addListener: vi.fn(),
				removeListener: vi.fn(),
				dispatchEvent: vi.fn(),
			})),
		);
	});

	afterEach(() => {
		cleanup();
		vi.useRealTimers();
		vi.unstubAllGlobals();
	});

	it("seals the request before mounting and revealing the channel workspace", async () => {
		vi.useFakeTimers();
		vi.stubGlobal("fetch", stubAdminFetch());

		renderApp();
		fireEvent.change(screen.getByLabelText("Admin token"), {
			target: { value: "transition-token" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Connect" }));

	await flushAsyncWork();

	// Authentication succeeds before the entrance transition publishes the session.
	await act(async () => {
		vi.advanceTimersByTime(100);
		await Promise.resolve();
	});

	expect(
		document.querySelector(".gateway-transition.is-sealing"),
	).toBeInTheDocument();
		expect(sessionStorage.getItem("meta-gateway.admin-token")).toBeNull();
		expect(localStorage.getItem("meta-gateway.admin-token")).toBeNull();
		expect(
			screen.getByRole("button", { name: "Connecting..." }),
		).toBeDisabled();

		await act(async () => {
			vi.advanceTimersByTime(ENTRANCE_CHARGE_MS);
			await Promise.resolve();
		});

		expect(sessionStorage.getItem("meta-gateway.admin-token")).toBeNull();
		expect(localStorage.getItem("meta-gateway.admin-token")).toBe(
			"mg-sess.test",
		);
		expect(
			document.querySelector(".gateway-transition.is-revealing"),
		).toBeInTheDocument();
		expect(
			screen.getByRole("heading", { name: "Overview" }),
		).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Models" })).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Tokens" })).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Logs" })).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Settings" })).toBeInTheDocument();

		act(() => vi.advanceTimersByTime(ENTRANCE_REVEAL_MS));
		expect(
			document.querySelector(".gateway-transition"),
		).not.toBeInTheDocument();
	});

	it("does not play the transition when authorization fails", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => jsonResponse({ error: "invalid token" }, 401)),
		);

		renderApp();
		fireEvent.change(screen.getByLabelText("Admin token"), {
			target: { value: "bad-token" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Connect" }));

		expect(await screen.findByText("invalid token")).toBeInTheDocument();
		expect(
			document.querySelector(".gateway-transition"),
		).not.toBeInTheDocument();
		expect(sessionStorage.getItem("meta-gateway.admin-token")).toBeNull();
		expect(localStorage.getItem("meta-gateway.admin-token")).toBeNull();
	});

	it("skips the visual sequence after successful authentication without logging in twice", async () => {
		vi.useFakeTimers();
		const fetch = stubAdminFetch();
		vi.stubGlobal("fetch", fetch);
		renderApp();
		fireEvent.change(screen.getByLabelText("Admin token"), { target: { value: "skip-token" } });
		fireEvent.click(screen.getByRole("button", { name: "Connect" }));
		await flushAsyncWork();
		fireEvent.click(screen.getByRole("button", { name: /Skip animation/ }));
		await flushAsyncWork();
		expect(screen.queryByRole("dialog", { name: "Workspace entrance" })).not.toBeInTheDocument();
		expect(localStorage.getItem("meta-gateway.admin-token")).toBe("mg-sess.test");
		expect(screen.getByRole("heading", { name: "Overview" })).toBeInTheDocument();
		act(() => vi.advanceTimersByTime(5000));
		expect(fetch.mock.calls.filter(([input]) => String(input) === "/admin/session")).toHaveLength(1);
		expect(localStorage.getItem("meta-gateway.admin-token")).toBe("mg-sess.test");
	});

	it("lands legacy routes on the channel workspace", async () => {
		localStorage.setItem("meta-gateway.admin-token", "redirect-token");
		vi.stubGlobal("fetch", stubAdminFetch());

		renderApp(["/assets"]);

		expect(
			await screen.findByRole("heading", { name: "Overview" }),
		).toBeInTheDocument();
	});

	it("opens the model workbench from the product nav", async () => {
		localStorage.setItem("meta-gateway.admin-token", "nav-token");
		vi.stubGlobal("fetch", stubAdminFetch());

		renderApp(["/workbench"]);
		expect(
			await screen.findByRole("heading", { name: "Model workbench" }),
		).toBeInTheDocument();
		expect(screen.getByRole("tab", { name: "Images" })).toBeInTheDocument();
		// The capability registry is no longer a workbench tab — it is a popup
		// on the Models page, where the model list it describes lives.
		expect(screen.getByRole("tab", { name: "Playground" })).toBeInTheDocument();
		expect(screen.queryByRole("tab", { name: "Capabilities" })).not.toBeInTheDocument();
	});

	it("hides a navigation entry on request without losing the page", async () => {
		// Entries are switched in Settings → Appearance. Hiding is a display
		// choice: the route keeps mounting and the palette keeps listing it, which
		// is what makes the panel safe to experiment with.
		localStorage.setItem("meta-gateway.admin-token", "nav-token");
		vi.stubGlobal("fetch", stubAdminFetch());

		// The nav label is read off the navigation itself rather than by role,
		// because both theme packages render navigation of their own (classic
		// deck rail / modern sidebar).
		const navLabels = () =>
			[
				...document.querySelectorAll(
					".console-navigation .console-nav-link, .deck-sector .deck-sector-label",
				),
			].map((element) => element.textContent?.trim());

		renderApp(["/models"]);
		expect(
			await screen.findByRole("heading", { level: 1, name: "Models" }),
		).toBeInTheDocument();
		expect(navLabels()).toContain("Check-in");
		expect(navLabels()).toContain("Workbench");

		cleanup();
		localStorage.setItem(
			"meta-gateway.topbar-items",
			JSON.stringify({ controls: {}, hiddenNav: ["/checkins", "/workbench"] }),
		);
		renderApp(["/models"]);
		expect(
			await screen.findByRole("heading", { level: 1, name: "Models" }),
		).toBeInTheDocument();
		expect(navLabels()).not.toContain("Check-in");
		expect(navLabels()).not.toContain("Workbench");

		// Hiding an entry is a display choice: the page itself still mounts, and
		// the command palette still offers it.
		cleanup();
		renderApp(["/checkins"]);
		expect(
			await screen.findByRole("heading", { level: 1, name: "Check-in" }),
		).toBeInTheDocument();
	});

	// The top bar used to carry a check-in shortcut next to the navigation entry
	// pointing at the same page, and needed a switch of its own just to get out
	// of the way. There is one entry per page now.
	it("has no check-in shortcut in the chrome", async () => {
		localStorage.setItem("meta-gateway.admin-token", "nav-token");
		vi.stubGlobal("fetch", stubAdminFetch());
		renderApp(["/models"]);
		expect(
			await screen.findByRole("heading", { level: 1, name: "Models" }),
		).toBeInTheDocument();
		expect(document.querySelector(".console-checkin, .deck-checkin-btn")).toBeNull();
	});

	it("opens models, logs, and maintain from the product nav", async () => {
		localStorage.setItem("meta-gateway.admin-token", "nav-token");
		vi.stubGlobal("fetch", stubAdminFetch());

		renderApp(["/models"]);
		expect(
			await screen.findByRole("heading", { level: 1, name: "Models" }),
		).toBeInTheDocument();

		cleanup();
		renderApp(["/logs"]);
		expect(
			await screen.findByRole("heading", { name: "Logs" }),
		).toBeInTheDocument();

		cleanup();
		renderApp(["/settings"]);
		expect(
			await screen.findByRole("heading", { name: "Settings" }),
		).toBeInTheDocument();
		expect(screen.getByRole("tab", { name: "Runtime" })).toBeInTheDocument();
		expect(screen.getByRole("tab", { name: "Backups" })).toBeInTheDocument();
	});

	it("uses the short safe path when reduced motion is requested", async () => {
		vi.useFakeTimers();
		vi.stubGlobal(
			"matchMedia",
			vi.fn().mockImplementation(() => ({
				matches: true,
				media: "(prefers-reduced-motion: reduce)",
				onchange: null,
				addEventListener: vi.fn(),
				removeEventListener: vi.fn(),
				addListener: vi.fn(),
				removeListener: vi.fn(),
				dispatchEvent: vi.fn(),
			})),
		);
		vi.stubGlobal("fetch", stubAdminFetch());

		renderApp();
		fireEvent.change(screen.getByLabelText("Admin token"), {
			target: { value: "reduced-token" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Connect" }));
	await flushAsyncWork();
	await act(async () => {
		vi.advanceTimersByTime(620);
		await Promise.resolve();
	});

	expect(localStorage.getItem("meta-gateway.admin-token")).toBe(
		"mg-sess.test",
	);
		expect(
			screen.getByRole("heading", { name: "Overview" }),
		).toBeInTheDocument();
		await act(async () => {
			vi.advanceTimersByTime(200);
			await Promise.resolve();
		});
		expect(
			document.querySelector(".gateway-transition"),
		).not.toBeInTheDocument();
	});

	// A plugin-answered model ("auto-jev" is answered by jev-router's route hook)
	// has no route and no members, so its row in the catalog is the only way to
	// reach the plugin from there. The console router mounts under /console
	// (main.tsx), so that target has to stay route-relative: a
	// "/console/plugins/…" href resolves to /console/console/plugins/…, matches
	// no route, and quietly lands in the catch-all redirect back to the overview
	// — reported from production as "查看插件 jumps to the home page".
	it("opens the plugin page from a plugin-answered model row", async () => {
		localStorage.setItem("meta-gateway.admin-token", "nav-token");
		vi.stubGlobal(
			"fetch",
			stubAdminFetch({
				// One real route, because the catalog renders its plugin rows inside the
				// table: with nothing routable the page shows the empty state instead.
				routes: [
					{
						route: {
							id: 1,
							model_pattern: "gpt-5.2",
							enabled: true,
							routing_mode: "priority",
							model_group: "default",
							created_at: "2026-01-01T00:00:00Z",
							updated_at: "2026-01-01T00:00:00Z",
						},
						members: [],
					},
				],
				modules: [
					{
						id: "jev-router",
						name: "Jev auto routing",
						version: "1.0.0",
						kind: "addon",
						source: "sidecar",
						installed: true,
						enabled: true,
						can_toggle: true,
						open_path: "/plugins/jev-router",
					},
				],
				hooks: [
					{
						plugin_id: "jev-router",
						plugin_name: "Jev auto routing",
						point: "route",
						path: "/route",
						match_models: ["auto-jev", "auto-*"],
					},
				],
			}),
		);

		renderApp(["/models"]);
		await screen.findByRole("heading", { level: 1, name: "Models" });

		// The plugin's model is a row of its own, owned by the plugin — and only the
		// literal name: a wildcard is a matcher, not something a client can call.
		const pluginModel = await screen.findByText("auto-jev");
		expect(pluginModel.closest("tr")).toHaveTextContent("Jev auto routing");
		expect(screen.queryByText("auto-*")).not.toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "Open plugin" }));

		// Destination is the plugin's own page: the router resolves it after the
		// /console basename, not into a doubled prefix that matches nothing.
		// (Router navigation is a transition in v7, so it lands a tick later.)
		await waitFor(() =>
			expect(screen.getByTestId("location")).toHaveTextContent(
				/^\/plugins\/jev-router$/,
			),
		);
		expect(
			document.querySelector("iframe.plugin-host-frame")?.getAttribute("src"),
		).toContain("/admin/plugins/jev-router/proxy/");
	});
});
