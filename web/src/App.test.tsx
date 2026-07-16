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

function renderApp() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return render(
		<QueryClientProvider client={queryClient}>
			<I18nProvider>
				<SessionProvider>
					<MemoryRouter>
						<App />
					</MemoryRouter>
				</SessionProvider>
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

describe("login transition", () => {
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

	it("seals the request before mounting and revealing the dashboard", async () => {
		vi.useFakeTimers();
		vi.stubGlobal(
			"fetch",
			vi.fn(async (input: RequestInfo | URL) => {
				const path = String(input);
				if (path === "/readyz") return new Response(null, { status: 200 });
				return jsonResponse([]);
			}),
		);

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
			vi.advanceTimersByTime(720);
			await Promise.resolve();
		});

		expect(sessionStorage.getItem("meta-gateway.admin-token")).toBe(
			"transition-token",
		);
		expect(
			document.querySelector(".gateway-transition.is-revealing"),
		).toBeInTheDocument();
		expect(
			screen.getByRole("heading", { name: "Dashboard" }),
		).toBeInTheDocument();

		act(() => vi.advanceTimersByTime(820));
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
		vi.stubGlobal(
			"fetch",
			vi.fn(async (input: RequestInfo | URL) => {
				if (String(input) === "/readyz")
					return new Response(null, { status: 200 });
				return jsonResponse([]);
			}),
		);

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
			document.querySelector(".gateway-transition.is-revealing"),
		).toBeInTheDocument();

		act(() => vi.advanceTimersByTime(160));
		expect(
			document.querySelector(".gateway-transition"),
		).not.toBeInTheDocument();
	});
});
