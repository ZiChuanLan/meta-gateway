import { describe, expect, it } from "vitest";
import { isEncryptedBackup, previewDocument, skipReasonKey } from "./aahBackup";

describe("previewDocument", () => {
	it("recognizes a current AAH backup even though the version is no longer 2.0", () => {
		const preview = previewDocument({
			version: "4.0",
			timestamp: 1,
			apiCredentialProfiles: {
				version: 3,
				profiles: [{ name: "main", baseUrl: "https://a.example.com", apiKey: "sk-a" }],
			},
			accounts: {
				accounts: [
					{
						id: "a1",
						site_name: "Good",
						site_url: "https://b.example.com",
						authType: "access_token",
						account_info: { id: "1", access_token: "tok" },
					},
					{
						id: "a2",
						site_name: "Cookie",
						site_url: "https://c.example.com",
						authType: "cookie",
						account_info: { id: "2", access_token: "" },
					},
				],
			},
		});
		expect(preview.format).toBe("all-api-hub");
		expect(preview.version).toBe("4.0");
		expect(preview.items).toBe(2);
		expect(preview.importable).toBe(true);
	});

	it("marks a credential-less AAH backup as not importable", () => {
		const preview = previewDocument({
			version: "4.0",
			timestamp: 1,
			channelConfigs: { schemaVersion: 2, configs: {} },
			preferences: { themeMode: "dark" },
		});
		expect(preview.items).toBe(0);
		expect(preview.importable).toBe(false);
	});

	it("reads a legacy AAH snapshot nested under data", () => {
		const preview = previewDocument({
			version: "4.0",
			data: {
				accounts: {
					accounts: [
						{ id: "a1", site_name: "N", site_url: "https://n.example.com", account_info: { access_token: "t" } },
					],
				},
			},
		});
		expect(preview.items).toBe(1);
		expect(preview.importable).toBe(true);
	});

	it("keeps the canonical envelope semantics", () => {
		const preview = previewDocument({
			format: "meta-gateway-aah-exchange",
			version: 1,
			importable: true,
			items: [{ name: "x" }, { name: "y" }],
		});
		expect(preview.kind).toBe("canonical");
		expect(preview.items).toBe(2);
		expect(preview.importable).toBe(true);
	});

	it("flags a metadata-only canonical export as not importable", () => {
		const preview = previewDocument({
			format: "meta-gateway-aah-exchange",
			version: 1,
			importable: false,
			items: [],
		});
		expect(preview.importable).toBe(false);
	});
});

describe("isEncryptedBackup", () => {
	it("detects the AAH envelope", () => {
		expect(
			isEncryptedBackup({
				type: "all-api-hub-webdav-backup-encrypted",
				v: 1,
				kdf: "PBKDF2",
				cipher: "AES-GCM",
				iter: 250000,
				salt: "AA==",
				iv: "AA==",
				ct: "AA==",
			}),
		).toBe(true);
	});

	it("ignores plain documents", () => {
		expect(isEncryptedBackup({ version: "4.0", accounts: {} })).toBe(false);
		expect(isEncryptedBackup(null)).toBe(false);
	});
});

describe("skipReasonKey", () => {
	it("passes known reasons through and falls back for unknown ones", () => {
		expect(skipReasonKey("missing_credential")).toBe("missing_credential");
		expect(skipReasonKey("duplicate_identity")).toBe("duplicate_identity");
		expect(skipReasonKey("something_new")).toBe("invalid_item");
	});
});
