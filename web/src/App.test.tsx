import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	act,
	cleanup,
	fireEvent,
	render,
	screen,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
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
						<MemoryRouter initialEntries={initialEntries}>
							<App />
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

async function flushAsyncWork() {
	await act(async () => {
		await Promise.resolve();
		await Promise.resolve();
		await Promise.resolve();
	});
}

function stubAdminFetch() {
	return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
		const path = String(input).split("?")[0];
		const method = init?.method ?? "GET";
		if (method === "POST" && path === "/admin/session") {
			return jsonResponse({ session_token: "mg-sess.test" });
		}
		if (path === "/readyz") return new Response(null, { status: 200 });
		if (path === "/admin/plugins/status") {
			return jsonResponse([
				{
					id: "exchange",
					name: "Exchange",
					version: "0.0.0",
					kind: "addon",
					installed: true,
					enabled: true,
					can_toggle: true,
				},
			]);
		}
		if (
			path === "/admin/sites" ||
			path === "/admin/channels" ||
			path === "/admin/channels/overview" ||
			path === "/admin/discovery/models" ||
			path === "/admin/downstream-keys" ||
			path === "/admin/proxy-logs" ||
			path === "/admin/routes/overview" ||
			path === "/admin/model-capabilities"
		) {
			return jsonResponse([]);
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
});
