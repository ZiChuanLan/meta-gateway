import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useSession } from "../session";

export function useAvailability(options: {
  routeIds?: number[];
  channelId?: number;
  group?: string;
  hours?: number;
}) {
  const { client } = useSession();
  const routeIds = [...(options.routeIds ?? [])].sort((a, b) => a - b);
  return useQuery({
    queryKey: [
      "availability",
      routeIds.join(","),
      options.channelId ?? 0,
      options.group ?? "",
      options.hours ?? 24,
    ],
    queryFn: ({ signal }) => api(client!).availability({ ...options, routeIds }, signal),
    enabled: Boolean(client) && (routeIds.length > 0 || Boolean(options.channelId)),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
}
