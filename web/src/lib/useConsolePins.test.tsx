import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { useConsolePins } from "./useConsolePins";

const pinsState: Record<string, string[]> = {};
const setConsolePin = vi.fn().mockResolvedValue({ status: "ok" });

vi.mock("../session", () => ({
  useSession: () => ({ client: {} }),
}));

vi.mock("../api/client", () => ({
  api: () => ({
    consolePins: vi.fn().mockImplementation(() => Promise.resolve(pinsState)),
    setConsolePin,
  }),
}));

function renderPins() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const shell = renderHook(() => useConsolePins("channel"), {
    wrapper: ({ children }: { children?: React.ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  });
  return shell;
}

describe("useConsolePins", () => {
  it("sorts pinned rows first in pin order and leaves the rest in place", async () => {
    pinsState.channel = ["9", "3"];
    const { result } = renderPins();
    await waitFor(() => expect(result.current.pinnedRefs.size).toBe(2));

    const sorted = result.current.sortByPin([{ id: 1 }, { id: 9 }, { id: 2 }, { id: 3 }], (row) =>
      String(row.id),
    );
    expect(sorted.map((row) => row.id)).toEqual([9, 3, 1, 2]);
  });

  it("treats unknown refs as unpinned", async () => {
    pinsState.channel = [];
    const { result } = renderPins();
    await waitFor(() => expect(result.current.pinnedRefs.size).toBe(0));

    const sorted = result.current.sortByPin([{ id: 4 }, { id: 2 }], (row) => String(row.id));
    expect(sorted.map((row) => row.id)).toEqual([4, 2]);
  });
});
