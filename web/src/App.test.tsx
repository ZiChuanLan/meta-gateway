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
	return vi.fn(async (input: RequestInfo | URL) => {
		const path = String(input).split("?")[0];
		if (path === "/readyz") return new Response(null, { status: 200 });
		if (
			path === "/admin/sites" ||
			path === "/admin/channels" ||
			path === "/admin/channels/overview" ||
			path === "/admin/discovery/models" ||
			path === "/admin/downstream-keys" ||
			path === "/admin/proxy-logs"
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

		expect(
			document.querySelector(".gateway-transition.is-sealing"),
		).toBeInTheDocument();
		expect(sessionStorage.getItem("meta-gateway.admin-token")).toBeNull();
		expect(
			screen.getByRole("button", { name: "Connecting..." }),
		).toBeDisabled();

		await act(async () => {
			vi.advanceTimersByTime(1420);
			await Promise.resolve();
		});

		expect(sessionStorage.getItem("meta-gateway.admin-token")).toBe(
			"transition-token",
		);
		expect(
			document.querySelector(".gateway-transition.is-revealing"),
		).toBeInTheDocument();
		expect(
			screen.getByRole("heading", { name: "Connections" }),
		).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Models" })).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Tokens" })).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Logs" })).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Settings" })).toBeInTheDocument();

		act(() => vi.advanceTimersByTime(1620));
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
	});

	it("lands legacy routes on the channel workspace", async () => {
		sessionStorage.setItem("meta-gateway.admin-token", "redirect-token");
		vi.stubGlobal("fetch", stubAdminFetch());

		renderApp(["/assets"]);

		expect(
			await screen.findByRole("heading", { name: "Connections" }),
		).toBeInTheDocument();
	});

	it("opens models, logs, and maintain from the product nav", async () => {
		sessionStorage.setItem("meta-gateway.admin-token", "nav-token");
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
		expect(screen.getByRole("tab", { name: "Exchange" })).toBeInTheDocument();
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

		expect(sessionStorage.getItem("meta-gateway.admin-token")).toBe(
			"reduced-token",
		);
		expect(
			screen.getByRole("heading", { name: "Connections" }),
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
