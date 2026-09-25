/**
 * Durable console state — the few things that must outlive a page load.
 *
 * The workbench keeps full-resolution base64 images, which do not fit in
 * localStorage's ~5MB budget, so IndexedDB is the primary store. A browser that
 * blocks it (some embedded webviews, private-mode variants) falls back to
 * localStorage, and a last-resort in-memory map keeps the surface working
 * rather than throwing on every render. Callers own their own size caps: the
 * fallbacks are smaller, and a quota error has to reach them so they can trim.
 *
 * Reads and writes are promise-based and never reject for a missing key — a
 * `null` result means "nothing stored yet".
 */
const DB_NAME = "meta-gateway-console";
const STORE_NAME = "state";
const DB_VERSION = 1;
const LOCAL_PREFIX = "meta-gateway.state.";

type Record = { key: string; value: unknown };

interface Backend {
	read(key: string): Promise<unknown>;
	write(key: string, value: unknown): Promise<void>;
}

export async function readConsoleState<T>(key: string): Promise<T | null> {
	const value = await (await backend()).read(key);
	return (value ?? null) as T | null;
}

/**
 * Stores `value` under `key`; `null` deletes the entry. A storage failure
 * (quota, blocked write) rejects so the caller can drop old entries and retry —
 * silently losing the history would look like the panel never saved it.
 */
export async function writeConsoleState(key: string, value: unknown): Promise<void> {
	await (await backend()).write(key, value);
}

let backendPromise: Promise<Backend> | null = null;

function backend(): Promise<Backend> {
	backendPromise ??= createBackend();
	return backendPromise;
}

/** Test seam: forget the chosen backend (IndexedDB can appear mid-session). */
export function resetConsoleStore(): void {
	backendPromise = null;
}

async function createBackend(): Promise<Backend> {
	const db = await openDatabase();
	if (db) return indexedBackend(db);
	if (hasLocalStorage()) return localBackend();
	return memoryBackend();
}

function openDatabase(): Promise<IDBDatabase | null> {
	return new Promise((resolve) => {
		if (typeof indexedDB === "undefined") {
			resolve(null);
			return;
		}
		let request: IDBOpenDBRequest;
		try {
			request = indexedDB.open(DB_NAME, DB_VERSION);
		} catch {
			resolve(null);
			return;
		}
		request.onupgradeneeded = () => {
			const db = request.result;
			if (!db.objectStoreNames.contains(STORE_NAME)) {
				db.createObjectStore(STORE_NAME, { keyPath: "key" });
			}
		};
		request.onsuccess = () => resolve(request.result);
		// A blocked or failed open is not fatal: fall back instead of hanging.
		request.onerror = () => resolve(null);
		request.onblocked = () => resolve(null);
	});
}

function settle<T>(source: IDBRequest<T>): Promise<T> {
	return new Promise((resolve, reject) => {
		source.onsuccess = () => resolve(source.result);
		source.onerror = () => reject(source.error ?? new Error("indexedDB request failed"));
	});
}

function indexedBackend(db: IDBDatabase): Backend {
	return {
		read: async (key) => {
			const store = db.transaction(STORE_NAME, "readonly").objectStore(STORE_NAME);
			const record = await settle<Record | undefined>(store.get(key) as IDBRequest<Record | undefined>);
			return record?.value ?? null;
		},
		write: async (key, value) => {
			const tx = db.transaction(STORE_NAME, "readwrite");
			const store = tx.objectStore(STORE_NAME);
			if (value === null) store.delete(key);
			else store.put({ key, value } satisfies Record);
			await new Promise<void>((resolve, reject) => {
				tx.oncomplete = () => resolve();
				tx.onerror = () => reject(tx.error ?? new Error("indexedDB write failed"));
				tx.onabort = () => reject(tx.error ?? new Error("indexedDB write aborted"));
			});
		},
	};
}

function hasLocalStorage(): boolean {
	try {
		const probe = `${LOCAL_PREFIX}probe`;
		localStorage.setItem(probe, "1");
		localStorage.removeItem(probe);
		return true;
	} catch {
		return false;
	}
}

function localBackend(): Backend {
	return {
		read: async (key) => {
			const raw = localStorage.getItem(LOCAL_PREFIX + key);
			return raw === null ? null : JSON.parse(raw);
		},
		write: async (key, value) => {
			if (value === null) localStorage.removeItem(LOCAL_PREFIX + key);
			else localStorage.setItem(LOCAL_PREFIX + key, JSON.stringify(value));
		},
	};
}

const memory = new Map<string, unknown>();

function memoryBackend(): Backend {
	return {
		read: async (key) => memory.get(key) ?? null,
		write: async (key, value) => {
			if (value === null) memory.delete(key);
			else memory.set(key, value);
		},
	};
}
