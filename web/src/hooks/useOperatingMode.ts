import { useQuery } from "@tanstack/react-query";
import { isStaff, useSession } from "../session";
import type { ModeInfo } from "../team/types";

/**
 * The gateway's operating mode, for the pages that care about it.
 *
 * Staff only: a member is not an operator, so "is this a team gateway" is not a
 * question their console asks — and skipping it is also what keeps the console
 * from logging a 403 every fifteen seconds.
 */
export function useOperatingMode() {
  const { client, role } = useSession();
  return useQuery({
    queryKey: ["team", "mode"],
    queryFn: ({ signal }) => client!.get<ModeInfo>("/admin/mode", signal),
    enabled: Boolean(client) && isStaff(role),
    retry: false,
    staleTime: 5000,
    refetchInterval: 15000,
    refetchOnWindowFocus: true,
  });
}
