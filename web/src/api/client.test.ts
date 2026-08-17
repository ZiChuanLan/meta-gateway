import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, ApiError } from "./client";

afterEach(() => vi.restoreAllMocks());

describe("ApiClient", () => {
	it("sends the admin token only in authorization", async () => {
		const fetchMock = vi
			.spyOn(globalThis, "fetch")
			.mockResolvedValue(new Response(JSON.stringify([]), { status: 200 }));
		await new ApiClient("admin secret").get("/admin/sites");
		const [path, init] = fetchMock.mock.calls[0]!;
		expect(path).toBe("/admin/sites");
		expect(new Headers(init?.headers).get("Authorization")).toBe(
			"Bearer admin secret",
		);
		expect(JSON.stringify(init)).not.toContain("/admin/sites?token");
	});

	it("returns a stable error without retaining response details", async () => {
		vi.spyOn(globalThis, "fetch").mockResolvedValue(
			new Response(JSON.stringify({ error: "unauthorized" }), { status: 401 }),
		);
		const error = await new ApiClient("do-not-leak")
			.get("/admin/sites")
			.catch((value: unknown) => value);
		expect(error).toBeInstanceOf(ApiError);
		expect(error).toMatchObject({ status: 401, message: "unauthorized" });
		expect(JSON.stringify(error)).not.toContain("do-not-leak");
	});

	it("normalizes network failures", async () => {
		vi.spyOn(globalThis, "fetch").mockRejectedValue(
			new Error("socket details"),
		);
		await expect(
			new ApiClient("token").get("/admin/sites"),
		).rejects.toMatchObject({
			status: 0,
			message: "Unable to reach Meta Gateway",
		});
	});

	it("normalizes null list responses to empty arrays", async () => {
		vi.spyOn(globalThis, "fetch").mockResolvedValue(
			new Response("null", { status: 200 }),
		);
		await expect(
			new ApiClient("token").getList("/admin/sites"),
		).resolves.toEqual([]);
	});

	it("invalidates the session on any unauthorized response", async () => {
		const onUnauthorized = vi.fn();
		vi.spyOn(globalThis, "fetch").mockResolvedValue(
			new Response('{"error":"unauthorized"}', { status: 401 }),
		);

		await expect(
			new ApiClient("token", onUnauthorized).get("/admin/sites"),
		).rejects.toMatchObject({ status: 401 });
		expect(onUnauthorized).toHaveBeenCalledOnce();
	});
});

describe("api.proxyLogs filters", () => {
	it("builds query string for site/channel/model/status filters", async () => {
		const fetchMock = vi
			.spyOn(globalThis, "fetch")
			.mockResolvedValue(new Response(JSON.stringify([]), { status: 200 }));
		const { api } = await import("./client");
		await api(new ApiClient("token")).proxyLogs({
			site_id: 7,
			channel_id: 42,
			model: "gpt-test",
			status: "failed",
			limit: 50,
		});
		expect(String(fetchMock.mock.calls[0]![0])).toBe(
			"/admin/proxy-logs?site_id=7&channel_id=42&model=gpt-test&status=failed&limit=50",
		);
	});

	it("omits query string when no filters are provided", async () => {
		const fetchMock = vi
			.spyOn(globalThis, "fetch")
			.mockResolvedValue(new Response(JSON.stringify([]), { status: 200 }));
		const { api } = await import("./client");
		await api(new ApiClient("token")).proxyLogs();
		expect(String(fetchMock.mock.calls[0]![0])).toBe("/admin/proxy-logs");
	});
});

describe("api connection and usage contracts", () => {
	it("creates a connection through the transactional endpoint", async () => {
		const fetchMock = vi
			.spyOn(globalThis, "fetch")
			.mockResolvedValue(new Response(JSON.stringify({}), { status: 201 }));
		const { api } = await import("./client");
		await api(new ApiClient("token")).createConnection({
			name: "demo",
			base_url: "https://api.example.com",
			secret: "sk-secret",
			models_csv: "gpt-test",
		});
		const [path, init] = fetchMock.mock.calls[0]!;
		expect(path).toBe("/admin/connections");
		expect(JSON.parse(String(init?.body))).toMatchObject({
			base_url: "https://api.example.com",
			secret: "sk-secret",
			models_csv: "gpt-test",
		});
		expect(String(init?.body)).not.toContain("header_override");
	});

	it("passes an RFC3339 lower bound to usage summaries", async () => {
		const fetchMock = vi
			.spyOn(globalThis, "fetch")
			.mockResolvedValue(new Response(JSON.stringify({}), { status: 200 }));
		const { api } = await import("./client");
		await api(new ApiClient("token")).usageSummary(
			undefined,
			undefined,
			"2026-08-16T00:00:00Z",
		);
		expect(String(fetchMock.mock.calls[0]![0])).toBe(
			"/admin/usage/summary?since=2026-08-16T00%3A00%3A00Z",
		);
	});
});
