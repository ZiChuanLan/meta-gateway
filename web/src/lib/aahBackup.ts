/**
 * Shared inspection of import documents (AAH backups and Meta Gateway exchange
 * packages). Kept framework-free so the console pages and the setup wizard
 * agree on what a file contains before either of them sends it to the backend.
 */

/** Envelope marker AAH writes when its WebDAV backup is encrypted. */
export const ENCRYPTED_BACKUP_TYPE = "all-api-hub-webdav-backup-encrypted";

export type BackupKind = "canonical" | "compatibility" | "encrypted";

export interface BackupPreview {
	kind: BackupKind;
	format: string;
	version: string;
	/** Rows that actually carry a usable credential. */
	items: number;
	/** null = needs an unlock password before we can tell. */
	importable: boolean | null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Reports whether a parsed document is an AAH-encrypted backup envelope. */
export function isEncryptedBackup(doc: unknown): boolean {
	if (!isRecord(doc)) return false;
	return (
		doc.type === ENCRYPTED_BACKUP_TYPE &&
		doc.v === 1 &&
		typeof doc.kdf === "string" &&
		typeof doc.cipher === "string" &&
		typeof doc.ct === "string" &&
		typeof doc.salt === "string" &&
		typeof doc.iv === "string"
	);
}

/** Reads a credential-bearing token out of one AAH account row. */
function accountToken(entry: unknown): string {
	if (!isRecord(entry)) return "";
	const info = isRecord(entry.account_info) ? entry.account_info : null;
	const candidates = [
		info?.access_token,
		info?.apiKey,
		info?.api_key,
		info?.token,
		entry.apiKey,
		entry.api_key,
		entry.key,
		entry.access_token,
	];
	for (const value of candidates) {
		if (typeof value === "string" && value.trim()) return value;
	}
	return "";
}

/** Counts the AAH rows that can become relay channels. */
function countAahEntries(doc: Record<string, unknown>): number {
	const profilesRoot = isRecord(doc.apiCredentialProfiles)
		? doc.apiCredentialProfiles
		: null;
	const profiles = profilesRoot && Array.isArray(profilesRoot.profiles)
		? profilesRoot.profiles
		: [];
	const usableProfiles = profiles.filter(
		(profile) => isRecord(profile) && typeof profile.apiKey === "string" && profile.apiKey.trim().length > 0,
	).length;

	const accountsRoot = doc.accounts;
	const accountList = Array.isArray(accountsRoot)
		? accountsRoot
		: isRecord(accountsRoot) && Array.isArray(accountsRoot.accounts)
			? accountsRoot.accounts
			: [];
	const usableAccounts = accountList.filter(
		(entry) => !(isRecord(entry) && entry.disabled === true) && accountToken(entry).length > 0,
	).length;

	return usableProfiles + usableAccounts;
}

/** Finds AAH sections either at the root or inside a legacy `data` container. */
function aahSections(doc: Record<string, unknown>): Record<string, unknown> | null {
	if (isRecord(doc.data)) {
		const nested = doc.data;
		const hasNested =
			isRecord(nested.apiCredentialProfiles) ||
			Array.isArray(nested.accounts) ||
			isRecord(nested.accounts);
		if (hasNested) {
			return { ...doc, ...nested };
		}
	}
	if (isRecord(doc.apiCredentialProfiles) || Array.isArray(doc.accounts) || isRecord(doc.accounts)) {
		return doc;
	}
	return null;
}

/** Sections that only ever appear in an All API Hub backup. */
const AAH_ONLY_SECTIONS = ["preferences", "tagStore", "channelConfigs", "featureGuidance"];

/**
 * Reports whether a document is an AAH backup. A selective AAH backup may hold
 * no credential section at all — it is still an AAH backup, just an empty one,
 * which is a different message from "we do not recognize this file".
 */
function isAahDocument(doc: Record<string, unknown>, sections: Record<string, unknown> | null): boolean {
	if (typeof doc.version !== "string") return false;
	if (sections) return true;
	const containers = [doc, isRecord(doc.data) ? doc.data : null];
	return containers.some(
		(container) => container !== null && AAH_ONLY_SECTIONS.some((key) => key in container),
	);
}

/**
 * Summarizes a parsed document for the preview panel. An encrypted envelope
 * reports kind "encrypted" and leaves importability unknown until the password
 * is supplied.
 */
export function previewDocument(doc: unknown): BackupPreview {
	if (isEncryptedBackup(doc)) {
		return {
			kind: "encrypted",
			format: ENCRYPTED_BACKUP_TYPE,
			version: String((doc as Record<string, unknown>).v ?? "-"),
			items: 0,
			importable: null,
		};
	}
	if (Array.isArray(doc)) {
		return {
			kind: "compatibility",
			format: "new-api-array",
			version: "-",
			items: doc.length,
			importable: doc.length > 0,
		};
	}
	if (!isRecord(doc)) {
		return { kind: "compatibility", format: "unknown", version: "-", items: 0, importable: null };
	}
	if (typeof doc.format === "string") {
		const items = Array.isArray(doc.items) ? doc.items.length : 0;
		return {
			kind: "canonical",
			format: doc.format,
			version: doc.version == null ? "-" : String(doc.version),
			items,
			importable: doc.importable === true,
		};
	}
	if (Array.isArray(doc.channels)) {
		return {
			kind: "compatibility",
			format: "new-api.channels",
			version: "-",
			items: doc.channels.length,
			importable: doc.channels.length > 0,
		};
	}
	// All API Hub: any string version plus an AAH section. Older releases wrote
	// "2.0" while current ones write "4.0", so the version must not be gated.
	const sections = aahSections(doc);
	if (isAahDocument(doc, sections)) {
		const items = sections ? countAahEntries(sections) : 0;
		return {
			kind: "compatibility",
			format: "all-api-hub",
			version: String(doc.version),
			items,
			importable: items > 0,
		};
	}
	if (Array.isArray(doc.data)) {
		return {
			kind: "compatibility",
			format: "new-api.data",
			version: "-",
			items: doc.data.length,
			importable: doc.data.length > 0,
		};
	}
	return { kind: "compatibility", format: "unknown", version: "-", items: 0, importable: null };
}

/** Maps a backend skip reason to its i18n key suffix. */
export function skipReasonKey(reason: string): string {
	switch (reason) {
		case "missing_credential":
		case "missing_field":
		case "invalid_base_url":
		case "duplicate_identity":
		case "invalid_item":
			return reason;
		default:
			return "invalid_item";
	}
}
