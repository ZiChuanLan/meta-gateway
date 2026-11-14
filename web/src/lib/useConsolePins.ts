import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import { api } from "../api/client";
import { useSession } from "../session";

/**
 * Shared console pins: the operator's "keep these at the top" list, stored
 * server-side so every admin sees the same order (unlike the localStorage
 * appearance prefs, pins are workspace state, not device state).
 *
 * `kind` is the board ("channel", "model"); `refOf` turns a row into the ref
 * the board pins by (its id as a string, or another stable key).
 */
export function useConsolePins(kind: string) {
  const { client } = useSession();
  const service = api(client!);
  const queryClient = useQueryClient();

  const pins = useQuery({
    queryKey: ["console-pins"],
    queryFn: ({ signal }) => service.consolePins(signal),
  });

  const toggle = useMutation({
    mutationFn: (input: { ref: string; pin: boolean }) =>
      service.setConsolePin(kind, input.ref, input.pin),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["console-pins"] }),
  });

  const pinnedRefs = useMemo(() => new Set(pins.data?.[kind] ?? []), [pins.data, kind]);

  /** Stable sort: pinned rows first (in pin order), the rest keep their order. */
  const sortByPin = useCallback(
    <T>(rows: T[], refOf: (row: T) => string): T[] => {
      const order = pins.data?.[kind] ?? [];
      const rank = (ref: string) => {
        const index = order.indexOf(ref);
        return index === -1 ? Number.MAX_SAFE_INTEGER : index;
      };
      return [...rows].sort((left, right) => rank(refOf(left)) - rank(refOf(right)));
    },
    [pins.data, kind],
  );

  return { pinnedRefs, sortByPin, toggle, isLoading: pins.isLoading };
}
