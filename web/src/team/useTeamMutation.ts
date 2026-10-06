import { useCallback, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { TeamText } from "./text";
import type { TeamRequest } from "./types";

/**
 * The one place a team board writes through.
 *
 * Every board needs the same four things around a mutation — a busy flag, an
 * error slot, a notice slot, and invalidating the `team` query family so the
 * other boards reload — and each board used to carry its own copy of that
 * dance. It lives here instead, so a board file is about its own fields and
 * layout.
 *
 * `run` resolves to the payload or to `undefined` when the request failed; the
 * error is already in `error` by then, so callers never branch on a rejected
 * promise.
 */
export function useTeamMutation(request: TeamRequest, t: TeamText) {
  const qc = useQueryClient();
  const running = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");

  const run = useCallback(
    async <T>(
      path: string,
      method: string,
      body?: unknown,
      onSuccess?: (data: T) => void,
    ): Promise<T | undefined> => {
      if (running.current) return undefined;
      running.current = true;
      setBusy(true);
      setError(null);
      setNotice("");
      try {
        const data = await request<T>(path, {
          method,
          ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        });
        onSuccess?.(data);
        await qc.invalidateQueries({ queryKey: ["team"] });
        if (path.startsWith("/admin/ratios/"))
          await qc.invalidateQueries({ queryKey: ["model-pricing"] });
        if (!onSuccess) setNotice(t("success"));
        return data;
      } catch (failure) {
        setError(failure);
        return undefined;
      } finally {
        running.current = false;
        setBusy(false);
      }
    },
    [request, qc, t],
  );

  return { busy, error, notice, setNotice, setError, run };
}
