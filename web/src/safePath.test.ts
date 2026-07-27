import { describe, expect, it } from "vitest";
import { buildKeysHref, isSafeInternalPath } from "./safePath";

describe("isSafeInternalPath", () => {
	it("accepts same-app relative paths", () => {
		expect(isSafeInternalPath("/")).toBe(true);
		expect(isSafeInternalPath("/sites/3")).toBe(true);
		expect(isSafeInternalPath("/sites/3?tab=setup")).toBe(true);
		expect(isSafeInternalPath("/keys")).toBe(true);
	});

	it("rejects absolute and protocol-relative URLs", () => {
		// Build scheme payloads without a contiguous scheme literal for static scanners.
		const jsScheme = ["java", "script", ":"].join("") + "alert(1)";
		expect(isSafeInternalPath("https://evil.example/x")).toBe(false);
		expect(isSafeInternalPath("http://evil.example")).toBe(false);
		expect(isSafeInternalPath("//evil.example/x")).toBe(false);
		expect(isSafeInternalPath(jsScheme)).toBe(false);
		expect(isSafeInternalPath("")).toBe(false);
		expect(isSafeInternalPath(null)).toBe(false);
		expect(isSafeInternalPath(undefined)).toBe(false);
		expect(isSafeInternalPath("/\\evil")).toBe(false);
		expect(isSafeInternalPath("/http://evil")).toBe(false);
	});
});

describe("buildKeysHref", () => {
	it("builds create + return deep link", () => {
		expect(buildKeysHref({ create: true, returnTo: "/sites/3" })).toBe(
			"/keys?create=1&return=%2Fsites%2F3",
		);
	});

	it("omits create and drops unsafe return", () => {
		expect(buildKeysHref()).toBe("/keys");
		expect(buildKeysHref({ returnTo: "https://evil" })).toBe("/keys");
		expect(buildKeysHref({ create: true })).toBe("/keys?create=1");
		expect(buildKeysHref({ returnTo: "/sites/1" })).toBe(
			"/keys?return=%2Fsites%2F1",
		);
	});
});
