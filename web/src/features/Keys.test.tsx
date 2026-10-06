import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	act,
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
import { Keys, KeysView } from "./Keys";
import { MEMBER_KEY_CAPS, type KeysSource } from "./keys/KeysSource";
import { setCurrency } from "../lib/format";

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

function keyEditFetch() {
  const key = {
    id: 9, name: "audit-key", enabled: true, scopes: "relay",
    quota_total_tokens: 100, quota_used_tokens: 2, quota_total_cost: 25,
    group_name: "standard", cost: 1, has_token: true, created_at: "2026-10-06",
  };
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).split("?")[0];
    if (path === "/admin/downstream-keys/9" && init?.method === "PUT") {
      Object.assign(key, JSON.parse(String(init.body)));
      return jsonResponse(key);
    }
    if (path === "/admin/downstream-keys") return jsonResponse([key]);
    if (path === "/admin/mode") return jsonResponse({ mode: "team", has_owner: true, role: "owner" });
    if (path === "/admin/groups") return jsonResponse([{ name: "standard" }, { name: "vip" }]);
    if (path === "/admin/route-groups") return jsonResponse({ groups: ["default"] });
    if (path === "/admin/model-metadata") return jsonResponse({ items: [] });
    if (path === "/admin/usage/summary") return jsonResponse({ request_count: 1, total_tokens: 2, cost: 1 });
    return jsonResponse([]);
  });
}

describe("Keys page", () => {
	beforeEach(() => {
		setCurrency({ symbol: "$", rate: 1 });
		localStorage.clear();
		sessionStorage.clear();
		localStorage.setItem("meta-gateway.locale", "en");
		localStorage.setItem("meta-gateway.admin-token", "test-token");
	});

	afterEach(() => {
		cleanup();
		setCurrency({ symbol: "$", rate: 1 });
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
		// Custom token lives in the Advanced fold (progressive disclosure).
		fireEvent.click(screen.getByRole("button", { name: /^Advanced/ }));
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

	it("offers routed model names, so a renamed model is pickable", async () => {
		const fetchMock = vi.fn(
			async (input: RequestInfo | URL, init?: RequestInit) => {
				const path = String(input);
				const method = (init?.method ?? "GET").toUpperCase();
				if (path.endsWith("/admin/downstream-keys") && method === "GET") {
					return jsonResponse([]);
				}
				if (path.includes("/admin/discovery/models")) {
					return jsonResponse([
						{
							id: 1,
							channel_id: 1,
							model_name: "deepseek-v4-flash",
							available: true,
							source: "sync",
							latency_ms: 12,
							checked_at: "2026-07-17T00:00:00Z",
						},
						{
							id: 2,
							channel_id: 1,
							model_name: "never-routed-upstream",
							available: true,
							source: "sync",
							latency_ms: 12,
							checked_at: "2026-07-17T00:00:00Z",
						},
					]);
				}
				if (path.endsWith("/admin/routes/overview")) {
					return jsonResponse([
						{
							route: {
								id: 4,
								model_pattern: "deepseek-flash",
								enabled: true,
								routing_mode: "priority",
								created_at: "2026-07-17T00:00:00Z",
								updated_at: "2026-07-17T00:00:00Z",
							},
							members: [
								{
									member: {
										id: 9,
										route_id: 4,
										channel_id: 1,
										priority: 0,
										weight: 100,
										enabled: true,
										auto: true,
										manual_override: true,
										mapping_json: JSON.stringify({ real: "deepseek-v4-flash" }),
										fail_count: 0,
										created_at: "2026-07-17T00:00:00Z",
										updated_at: "2026-07-17T00:00:00Z",
									},
									channel: { id: 1, name: "Site One" },
									credential_usable: true,
								},
							],
						},
					]);
				}
				if (path.endsWith("/admin/model-metadata")) {
					return jsonResponse({ items: [] });
				}
				if (path.endsWith("/admin/route-groups")) {
					return jsonResponse({ groups: [] });
				}
				if (path.includes("/admin/usage/summary")) {
					return jsonResponse({
						request_count: 0,
						prompt_tokens: 0,
						completion_tokens: 0,
						total_tokens: 0,
					});
				}
				return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
			},
		);
		vi.stubGlobal("fetch", fetchMock);
		renderKeys();

		await screen.findByText(/No downstream tokens yet/i);
		fireEvent.click(firstCreateKeyButton());
		fireEvent.click(screen.getByRole("button", { name: /^Model access/ }));

		const names = () =>
			Array.from(document.querySelectorAll(".model-picker-name")).map(
				(node) => node.textContent?.trim() ?? "",
			);
		await waitFor(() => expect(names()).toContain("deepseek-flash"));
		// The upstream spelling is not callable — the route answers under the
		// alias — and a discovered model with no route is not offered at all.
		expect(names()).not.toContain("deepseek-v4-flash");
		expect(names()).not.toContain("never-routed-upstream");
		const row = Array.from(
			document.querySelectorAll(".model-picker-item"),
		).find(
			(item) =>
				item.querySelector(".model-picker-name")?.textContent ===
				"deepseek-flash",
		);
		expect(row?.textContent).toContain("Site One");
		expect(
			row
				?.querySelector(".model-picker-badge.is-alias")
				?.getAttribute("title"),
		).toContain("deepseek-v4-flash");
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
							cost: 0,
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
		fireEvent.click(screen.getByRole("button", { name: "More actions" }));
		// A key is a relay credential, not a user account. The retired
		// key-bound portal must not leave a sign-in management action here.
		expect(
			screen.queryByRole("menuitem", { name: /Sign-in methods|keys\.portal/ }),
		).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("menuitem", { name: "Rotate token" }));
		expect(
			await screen.findByText(/A new token will be generated/i),
		).toBeInTheDocument();
		const rotateDialog = screen.getByRole("dialog", { name: "Rotate token" });
		fireEvent.click(
			within(rotateDialog).getByRole("button", { name: "Rotate token" }),
		);
		expect(await screen.findByText("mg-rotated-new")).toBeInTheDocument();
	});

	it("sends the route group and spend limit when editing a key", async () => {
		// Both controls collected a value the update never sent, and an omitted
		// field means "keep what is stored": picking a route group or a spend
		// limit looked saved and was not. Assert on the request body, because
		// that is the layer where the value was being lost.
		const fetchMock = vi.fn(
			async (input: RequestInfo | URL, init?: RequestInit) => {
				const path = String(input);
				const method = (init?.method ?? "GET").toUpperCase();
				if (path.endsWith("/admin/downstream-keys") && method === "GET") {
					return jsonResponse([
						{
							id: 9,
							name: "tiered",
							enabled: true,
							scopes: "relay",
							quota_used_tokens: 0,
							quota_total_tokens: 0,
							quota_total_cost: 0,
							cost: 0,
							has_token: true,
							created_at: "2026-07-17T00:00:00Z",
						},
					]);
				}
				if (path.endsWith("/admin/route-groups")) {
					return jsonResponse({ groups: ["default", "vip"] });
				}
				if (path.endsWith("/admin/downstream-keys/9") && method === "PUT") {
					return jsonResponse({ id: 9, name: "tiered" });
				}
				if (
					path.includes("/admin/discovery/models") ||
					path.endsWith("/admin/routes/overview")
				) {
					return jsonResponse([]);
				}
				return jsonResponse({ error: `unexpected ${method} ${path}` }, 500);
			},
		);
		vi.stubGlobal("fetch", fetchMock);
		renderKeys();

		expect(await screen.findByText("tiered")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "More actions" }));
		fireEvent.click(screen.getByRole("menuitem", { name: "Edit quota" }));

		const dialog = await screen.findByRole("dialog", {
			name: /Edit token quota/,
		});
		fireEvent.click(within(dialog).getByText("Quota"));
		fireEvent.change(within(dialog).getByLabelText("Spend limit"), {
			target: { value: "25" },
		});
		fireEvent.click(within(dialog).getByText("Advanced"));
		fireEvent.change(within(dialog).getByLabelText("Route group"), {
			target: { value: "vip" },
		});
		fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

		await waitFor(() => {
			const put = fetchMock.mock.calls.find(
				([input, init]) =>
					String(input).endsWith("/admin/downstream-keys/9") &&
					(init?.method ?? "GET").toUpperCase() === "PUT",
			);
			expect(put).toBeTruthy();
			const body = JSON.parse(String(put?.[1]?.body));
			expect(body.route_group_name).toBe("vip");
			expect(body.quota_total_cost).toBe(25);
		});
	});

	it("renders for a member with no console session and no team vocabulary", async () => {
		// The member app mounts this same renderer with its own source and
		// WITHOUT a SessionProvider. An operator-only hook inside it (the
		// operating-mode lookup) took the whole user page down, so this renders it
		// the way the member app does — no SessionProvider anywhere in the tree.
		const source: KeysSource = {
			keys: async () =>
				[
					{
						id: 1,
						name: "my-token",
						enabled: true,
						quota_used_tokens: 10,
						quota_total_tokens: 0,
						cost: 0,
						created_at: "2026-10-01",
						has_token: true,
						group_name: "vip",
					} as never,
				],
			discoveredModels: async () => [],
			usageSummary: async () =>
				({
					request_count: 0,
					prompt_tokens: 0,
					completion_tokens: 0,
					total_tokens: 0,
					total_cost: 0,
				}) as never,
			routeOverviews: async () => [],
			routeGroups: async () => ({ groups: [] }),
			keyGroups: async () => ({ groups: ["vip"] }),
			modelMetadata: async () => ({ items: [] }),
		};
		render(
			<QueryClientProvider
				client={
					new QueryClient({ defaultOptions: { queries: { retry: false } } })
				}
			>
				<I18nProvider>
					<ToastProvider>
						<MemoryRouter>
							<KeysView source={source} caps={MEMBER_KEY_CAPS} />
						</MemoryRouter>
					</ToastProvider>
				</I18nProvider>
			</QueryClientProvider>,
		);
		expect(await screen.findByText("my-token")).toBeInTheDocument();
		// The tenant group is the multi-user module's vocabulary: a member is
		// never shown which group their token is bound to.
		expect(screen.queryByText("Tenant group")).not.toBeInTheDocument();
	});

  it.each(["vip", ""])("saves and reloads the tenant group including explicit clearing: %s", async (group) => {
    const fetcher = keyEditFetch();
    vi.stubGlobal("fetch", fetcher);
    renderKeys();
    await screen.findByText("audit-key");
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit quota" }));
    const field = await screen.findByLabelText("Tenant group");
    await waitFor(() => expect(within(field).getByRole("option", { name: "vip" })).toBeInTheDocument());
    fireEvent.change(field, { target: { value: group } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const put = fetcher.mock.calls.find(([, init]) => init?.method === "PUT");
    expect(JSON.parse(String(put?.[1]?.body)).group_name).toBe(group);
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit quota" }));
    // An explicitly cleared group no longer automatically opens Advanced.
    if (!screen.queryByLabelText("Tenant group")) fireEvent.click(screen.getByText("Advanced"));
    expect(await screen.findByLabelText("Tenant group")).toHaveValue(group);
  });

  it.each([["Token quota", "-1"], ["Token quota", "1.5"], ["Spend limit", "-1"]])(
    "rejects invalid %s=%s rather than saving an unlimited quota", async (label, value) => {
      const fetcher = keyEditFetch();
      vi.stubGlobal("fetch", fetcher);
      renderKeys();
      await screen.findByText("audit-key");
      fireEvent.click(screen.getByRole("button", { name: "More actions" }));
      fireEvent.click(screen.getByRole("menuitem", { name: "Edit quota" }));
      fireEvent.change(await screen.findByLabelText(label), { target: { value } });
      expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
      expect(screen.getByRole("alert")).toHaveTextContent(/non-negative/);
      expect(fetcher.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(false);
      fireEvent.change(screen.getByLabelText(label), { target: { value: "0" } });
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    },
  );

  it("converts recorded fees for display without converting USD quota inputs", async () => {
    const fetcher = keyEditFetch();
    vi.stubGlobal("fetch", fetcher);
    renderKeys();
    await screen.findByText("audit-key");
    act(() => setCurrency({ symbol: "¥", rate: 7 }));
    expect(await screen.findByText("¥7.00")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit quota" }));
    expect(await screen.findByLabelText("Spend limit")).toHaveValue(25);
    expect(fetcher.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(false);
  });
});
