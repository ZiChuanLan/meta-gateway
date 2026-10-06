import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { TeamRequest } from "./types";
import { teamText } from "./text";
import { useTeamMutation } from "./useTeamMutation";
it("blocks synchronous double submits and releases the guard after completion", async () => {
  let finish!: (value: unknown) => void;
  const request = vi.fn(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result, unmount } = renderHook(
    () => useTeamMutation(request as TeamRequest, teamText("en")),
    { wrapper },
  );
  let first!: Promise<unknown>;
  let duplicate!: Promise<unknown>;
  act(() => {
    first = result.current.run("/admin/team/invitations", "POST", {});
    duplicate = result.current.run("/admin/team/invitations", "POST", {});
  });
  expect(request).toHaveBeenCalledTimes(1);
  expect(await duplicate).toBeUndefined();
  await act(async () => {
    finish({ id: 1 });
    await first;
  });
  let next!: Promise<unknown>;
  act(() => {
    next = result.current.run("/admin/team/invitations", "POST", {});
  });
  expect(request).toHaveBeenCalledTimes(2);
  await act(async () => {
    finish({ id: 2 });
    await next;
  });
  unmount();
});

it("releases its submission lock after failure without repeating the failed write", async () => {
  const request = vi
    .fn()
    .mockRejectedValueOnce(new Error("failed"))
    .mockResolvedValueOnce({ id: 2 });
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result, unmount } = renderHook(
    () => useTeamMutation(request as TeamRequest, teamText("en")),
    { wrapper },
  );
  await act(async () => {
    await result.current.run("/admin/team/users", "POST", {});
  });
  expect(result.current.busy).toBe(false);
  expect(result.current.error).toBeTruthy();
  expect(request).toHaveBeenCalledTimes(1);
  await act(async () => {
    await result.current.run("/admin/team/users", "POST", {});
  });
  expect(request).toHaveBeenCalledTimes(2);
  expect(result.current.error).toBeNull();
  unmount();
});
