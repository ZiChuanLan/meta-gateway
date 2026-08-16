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
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { ToastProvider } from "../toast";
import { Keys } from "./Keys";

function LocationProbe() {
	const location = useLocation();
	return (
		<div data-testid="location">
			{location.pathname}
			{location.search}
		</div>
	);
}

function renderKeys(initialEntry = "/keys") {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return {
		queryClient,
		...render(
			<QueryClientProvider client={queryClient}>
				<I18nProvider>
					<ToastProvider>
					<SessionProvider>
						<MemoryRouter initialEntries={[initialEntry]}>
							<Routes>
								<Route
									path="/keys"
									element={
										<>
											<Keys />
											<LocationProbe />
										</>
									}
								/>
								<Route
									path="/sites/:id"
									element={<div data-testid="site-return">site detail</div>}
								/>
							</Routes>
						</MemoryRouter>
					</SessionProvider>
					</ToastProvider>
				</I18nProvider>
			</QueryClientProvider>,
		),
	};
}

function jsonResponse(body: unknown, status = 200) {
	return new Response(JSON.stringify(body), {
		status,
		headers: { "Content-Type": "application/json" },
	});
}

function firstCreateKeyButton() {
	const buttons = screen.getAllByRole("button", { name: "Create token" });
	const button = buttons[0];
	if (!button) throw new Error("Create token button not found");
	return button;
}

describe("Keys page", () => {
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

	it("names the next step in the empty state and opens create dialog", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn(async (input: RequestInfo | URL) => {
				if (String(input).endsWith("/admin/downstream-keys")) {
					return jsonResponse([]);
				}
				return jsonResponse({ error: "unexpected" }, 500);
			}),
		);

		renderKeys();
		expect(
			await screen.findByText(/No downstream tokens yet/i),
		).toBeInTheDocument();
		fireEvent.click(firstCreateKeyButton());
		expect(
			await screen.findByRole("heading", { name: "Create downstream key" }),
		).toBeInTheDocument();
	});

	it("auto-opens create dialog once for ?create=1 and strips create param", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn(async (input: RequestInfo | URL) => {
				if (String(input).endsWith("/admin/downstream-keys")) {
					return jsonResponse([]);
				}
				return jsonResponse({ error: "unexpected" }, 500);
			}),
		);

		renderKeys("/keys?create=1&return=%2Fsites%2F9");
		expect(
			await screen.findByRole("heading", { name: "Create downstream key" }),
		).toBeInTheDocument();
		await waitFor(() => {
			expect(screen.getByTestId("location")).toHaveTextContent(
				"/keys?return=%2Fsites%2F9",
			);
		});
		expect(screen.getByTestId("location").textContent).not.toContain(
			"create=1",
		);
	});

	it("invalidates keys after create and shows one-time secret", async () => {
		const fetchMock = vi.fn(
			async (input: RequestInfo | URL, init?: RequestInit) => {
				const path = String(input);
				const method = (init?.method ?? "GET").toUpperCase();
				if (path.endsWith("/admin/downstream-keys") && method === "GET") {
					return jsonResponse([]);
				}
				if (path.endsWith("/admin/downstream-keys") && method === "POST") {
					return jsonResponse({
						id: 7,
						name: "ops",
						enabled: true,
						scopes: "relay",
						created_at: "2026-07-17T00:00:00Z",
						token: "mg-secret-once",
					});
				}
				return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
			},
		);
		vi.stubGlobal("fetch", fetchMock);
		const { queryClient } = renderKeys();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");

		await screen.findByText(/No downstream tokens yet/i);
		fireEvent.click(firstCreateKeyButton());
		fireEvent.change(screen.getByLabelText("Name"), {
			target: { value: "ops" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Create" }));

		expect(await screen.findByText("mg-secret-once")).toBeInTheDocument();
		await waitFor(() => {
			expect(invalidate).toHaveBeenCalledWith({ queryKey: ["keys"] });
		});
		expect(
			screen.queryByRole("cell", { name: /mg-secret-once/ }),
		).not.toBeInTheDocument();
	});

	it("can create with an operator-chosen secret", async () => {
		const fetchMock = vi.fn(
			async (input: RequestInfo | URL, init?: RequestInit) => {
				const path = String(input);
				const method = (init?.method ?? "GET").toUpperCase();
				if (path.endsWith("/admin/downstream-keys") && method === "GET") {
					return jsonResponse([]);
				}
				if (path.endsWith("/admin/downstream-keys") && method === "POST") {
					const body = JSON.parse(String(init?.body ?? "{}")) as {
						name?: string;
						token?: string;
					};
					expect(body.token).toBe("my-custom-secret-16");
					return jsonResponse({
						id: 9,
						name: body.name ?? "custom",
						enabled: true,
						scopes: "relay",
						created_at: "2026-07-27T00:00:00Z",
						token: body.token,
					});
				}
				return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
			},
		);
		vi.stubGlobal("fetch", fetchMock);
		renderKeys();

		await screen.findByText(/No downstream tokens yet/i);
		fireEvent.click(firstCreateKeyButton());
		fireEvent.change(screen.getByLabelText("Name"), {
			target: { value: "custom-app" },
		});
		fireEvent.click(screen.getByLabelText(/Set my own secret/i));
		fireEvent.change(screen.getByLabelText("Secret"), {
			target: { value: "my-custom-secret-16" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Create" }));

		expect(await screen.findByText("my-custom-secret-16")).toBeInTheDocument();
		const postCall = fetchMock.mock.calls.find(
			([, init]) => (init?.method ?? "GET").toUpperCase() === "POST",
		);
		expect(postCall).toBeTruthy();
	});

	it("shows one-time token without site-return after slim key flow", async () => {
		const fetchMock = vi.fn(
			async (input: RequestInfo | URL, init?: RequestInit) => {
				const path = String(input);
				const method = (init?.method ?? "GET").toUpperCase();
				if (path.endsWith("/admin/downstream-keys") && method === "GET") {
					return jsonResponse([]);
				}
				if (path.endsWith("/admin/downstream-keys") && method === "POST") {
					return jsonResponse({
						id: 7,
						name: "ops",
						enabled: true,
						scopes: "relay",
						created_at: "2026-07-17T00:00:00Z",
						token: "mg-secret-once",
					});
				}
				return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
			},
		);
		vi.stubGlobal("fetch", fetchMock);
		renderKeys("/keys?create=1");

		await screen.findByRole("heading", { name: "Create downstream key" });
		fireEvent.change(screen.getByLabelText("Name"), {
			target: { value: "ops" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Create" }));

		expect(await screen.findByText("mg-secret-once")).toBeInTheDocument();
		expect(
			screen.queryByRole("button", { name: "Back to site" }),
		).not.toBeInTheDocument();
		expect(
			screen.getByRole("button", { name: "I have stored it" }),
		).toBeInTheDocument();
	});

	it("re-views a stored plaintext token and rotates it", async () => {
		const fetchMock = vi.fn(
			async (input: RequestInfo | URL, init?: RequestInit) => {
				const path = String(input);
				const method = (init?.method ?? "GET").toUpperCase();
				if (path.endsWith("/admin/downstream-keys") && method === "GET") {
					return jsonResponse([
						{
							id: 5,
							name: "stored",
							enabled: true,
							scopes: "relay",
							quota_used_tokens: 0,
							quota_total_tokens: 0,
							estimated_cost: 0,
							has_token: true,
							created_at: "2026-07-17T00:00:00Z",
						},
					]);
				}
				if (path.endsWith("/admin/downstream-keys/5/reveal")) {
					return jsonResponse({ token: "mg-stored-plaintext" });
				}
				if (path.endsWith("/admin/downstream-keys/5/rotate")) {
					return jsonResponse({ id: 5, token: "mg-rotated-new" });
				}
				return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
			},
		);
		vi.stubGlobal("fetch", fetchMock);
		renderKeys();

		// Row shows the stored key; reveal returns the plaintext.
		expect(await screen.findByText("stored")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "View token" }));
		expect(await screen.findByText("mg-stored-plaintext")).toBeInTheDocument();
		const viewDialog = screen.getByRole("dialog", { name: /Token · stored/ });
		fireEvent.click(
			within(viewDialog).getAllByRole("button", { name: "Close" })[0]!,
		);

		// Rotate confirms, then shows the fresh token.
		fireEvent.click(screen.getAllByRole("button", { name: "Rotate token" })[0]!);
		expect(
			await screen.findByText(/A new token will be generated/i),
		).toBeInTheDocument();
		const rotateDialog = screen.getByRole("dialog", { name: "Rotate token" });
		fireEvent.click(
			within(rotateDialog).getByRole("button", { name: "Rotate token" }),
		);
		expect(await screen.findByText("mg-rotated-new")).toBeInTheDocument();
	});
});
