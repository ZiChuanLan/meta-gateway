import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readConsoleState, resetConsoleStore, writeConsoleState } from "./consoleStore";

/**
 * A minimal IndexedDB double covering exactly what consoleStore uses: open
 * (with the upgrade that creates the object store), get, put, delete, and a
 * transaction that completes. jsdom ships no IndexedDB, so without this the
 * primary backend would never be exercised by any test.
 */
function installFakeIndexedDB() {
  const rows = new Map<string, unknown>();
  const stores = new Set<string>();
  const opened: string[] = [];
  const request = <T,>(value: T) => {
    const req = { onsuccess: null as null | (() => void), onerror: null as null | (() => void), result: value, error: null };
    queueMicrotask(() => req.onsuccess?.());
    return req as unknown as IDBRequest<T>;
  };
  const db = {
    objectStoreNames: { contains: (name: string) => stores.has(name) },
    createObjectStore: (name: string) => {
      if (stores.has(name)) throw new Error(`store ${name} already exists`);
      stores.add(name);
      return {};
    },
    transaction: (_name: string, _mode: string) => {
      const tx = { error: null, oncomplete: null as null | (() => void), onerror: null as null | (() => void), onabort: null as null | (() => void), objectStore: () => store };
      // A real transaction completes after its requests, so finish last.
      queueMicrotask(() => queueMicrotask(() => tx.oncomplete?.()));
      return tx;
    },
  };
  const store = {
    get: (key: string) => request(rows.has(key) ? { key, value: rows.get(key) } : undefined),
    put: (record: { key: string; value: unknown }) => {
      rows.set(record.key, record.value);
      return request(undefined);
    },
    delete: (key: string) => {
      rows.delete(key);
      return request(undefined);
    },
  };
  vi.stubGlobal("indexedDB", {
    open: (name: string) => {
      opened.push(name);
      const req = { result: db, error: null, onsuccess: null as null | (() => void), onerror: null as null | (() => void), onupgradeneeded: null as null | (() => void), onblocked: null as null | (() => void) };
      queueMicrotask(() => {
        req.onupgradeneeded?.();
        req.onsuccess?.();
      });
      return req;
    },
  });
  return { rows, opened, stores };
}

describe("console state store", () => {
  beforeEach(() => {
    localStorage.clear();
    resetConsoleStore();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    resetConsoleStore();
  });

  // jsdom, like some embedded webviews, has no IndexedDB at all: the console
  // still has to keep its state instead of throwing on every write.
  it("falls back to localStorage when IndexedDB is missing", async () => {
    expect(typeof globalThis.indexedDB).toBe("undefined");
    await writeConsoleState("probe", { value: 1 });
    expect(JSON.parse(localStorage.getItem("meta-gateway.state.probe")!)).toEqual({ value: 1 });
    expect(await readConsoleState("probe")).toEqual({ value: 1 });
    await writeConsoleState("probe", null);
    expect(await readConsoleState("probe")).toBeNull();
    expect(localStorage.getItem("meta-gateway.state.probe")).toBeNull();
  });

  it("keeps console state in IndexedDB when the browser provides it", async () => {
    const fake = installFakeIndexedDB();
    const payload = { images: ["data:image/png;base64," + "A".repeat(4000)] };
    await writeConsoleState("workbench.runs", payload);
    expect(fake.rows.get("workbench.runs")).toEqual(payload);
    expect(await readConsoleState("workbench.runs")).toEqual(payload);
    // The state lives in ONE store: nothing is duplicated into localStorage,
    // whose budget is what IndexedDB exists here to avoid.
    expect(localStorage.getItem("meta-gateway.state.workbench.runs")).toBeNull();
    // The object store is created by the upgrade, once, and the database is
    // opened once for the whole session.
    expect([...fake.stores]).toEqual(["state"]);
    expect(fake.opened).toEqual(["meta-gateway-console"]);
    await writeConsoleState("workbench.runs", null);
    expect(await readConsoleState("workbench.runs")).toBeNull();
  });

  it("reports a missing key as null rather than failing", async () => {
    installFakeIndexedDB();
    expect(await readConsoleState("never-written")).toBeNull();
  });
});
