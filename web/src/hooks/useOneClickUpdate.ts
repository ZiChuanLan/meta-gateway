import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useSession } from "../session";
import { updateLanded, updateWentBackwards } from "../lib/updateState";
import type { UpdateWatch } from "../lib/updateState";

export type { UpdateWatch };
export interface UpdateFailure {
  target: string;
  reason: string;
  /** Set when the console itself diagnosed the outcome, so the dialog can
   *  translate it; `reason` then carries the data (version and tag). */
  code?: "notNewer";
}
const STORAGE = "meta-gateway.update-watch";
const BUDGET = 16 * 60_000;
// How long an executor may take before the console stops assuming it is merely
// slow. A watchtower pull plus recreate is seconds; minutes means something is
// wrong, and the operator should be told while the task is still open.
const STALL_AFTER = 3 * 60_000;
function readWatch(): UpdateWatch | null {
  try {
    const value = JSON.parse(
      sessionStorage.getItem(STORAGE) ?? "null",
    ) as Partial<UpdateWatch> | null;
    if (!value || typeof value.target !== "string" || !Number.isFinite(value.startedAt))
      return null;
    return {
      target: value.target,
      startedAt: value.startedAt!,
      from: typeof value.from === "string" ? value.from : undefined,
      tracked: typeof value.tracked === "string" ? value.tracked : undefined,
    };
  } catch {
    return null;
  }
}
function storeWatch(value: UpdateWatch | null) {
  try {
    if (value) sessionStorage.setItem(STORAGE, JSON.stringify(value));
    else sessionStorage.removeItem(STORAGE);
  } catch {
    /* A blocked storage must not break observing a live task. */
  }
}

/** One bounded observer; navigation can resume it without submitting again. */
export function useOneClickUpdate() {
  const { client } = useSession();
  const service = useMemo(() => (client ? api(client) : null), [client]);
  const [watch, setWatch] = useState<UpdateWatch | null>(readWatch);
  const [failure, setFailure] = useState<UpdateFailure | null>(null);
  // Set when the executor has had time to do its job and the running build has
  // not moved. A silent wait is the worst outcome here: watchtower can accept
  // the request and scan nothing (a container that lost its enable label), and
  // the operator would sit in front of "updating…" for the full 16-minute budget
  // with nothing to act on. Observed in production on 2026-10-07.
  const [stalled, setStalled] = useState(false);
  const resumed = useRef(false);
  const status = useQuery({
    queryKey: ["self-update"],
    queryFn: ({ signal }) => service!.selfUpdateStatus(signal),
    enabled: Boolean(service) && !watch,
    staleTime: 0,
  });
  useEffect(() => {
    if (resumed.current || !status.data) return;
    resumed.current = true;
    if (!watch && status.data?.running && status.data.target) {
      // Started in another tab or browser: rebuild the same watch from the
      // server's record, including what it can tell about the tracked tag.
      const resumedWatch: UpdateWatch = {
        target: status.data.target,
        startedAt: status.data.started_at ?? Date.now(),
        from: status.data.from ?? undefined,
        tracked:
          status.data.mode === "watchtower" ? (status.data.tracking_tag ?? undefined) : undefined,
      };
      storeWatch(resumedWatch);
      setWatch(resumedWatch);
    }
  }, [status.data, watch]);
  const apply = useCallback(
    async (target: string, options: { from?: string; tracked?: string } = {}) => {
      if (!service) return;
      setFailure(null);
      const next: UpdateWatch = {
        target,
        startedAt: Date.now(),
        from: options.from,
        tracked: options.tracked,
      };
      storeWatch(next);
      setWatch(next);
      try {
        // The server snapshots the database before it starts the handoff, and it
        // refuses to start without one; the name comes back so the console can
        // say which snapshot this upgrade can be rolled back to.
        const started = await service.applySelfUpdate(target);
        return started?.backup;
      } catch (error) {
        // A network failure is ambiguous: keep observing rather than encouraging
        // another submission. A definite HTTP rejection did not start this task.
        if (
          error &&
          typeof error === "object" &&
          "status" in error &&
          typeof error.status === "number" &&
          error.status > 0
        ) {
          storeWatch(null);
          setWatch(null);
          throw error;
        }
      }
    },
    [service],
  );
  useEffect(() => {
    if (!watch || !service) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    let controller: AbortController | undefined;
    const finish = (reason?: string, code?: UpdateFailure["code"]) => {
      storeWatch(null);
      setWatch(null);
      if (reason !== undefined) setFailure({ target: watch.target, reason, code });
    };
    const tick = async () => {
      if (disposed) return;
      if (Date.now() - watch.startedAt >= BUDGET) {
        finish("");
        return;
      }
      if (Date.now() - watch.startedAt >= STALL_AFTER) setStalled(true);
      controller = new AbortController();
      const timeout = setTimeout(() => controller?.abort(), 5000);
      try {
        const health = await fetch("/healthz", {
          signal: controller.signal,
          cache: "no-store",
        });
        const body = (await health.json()) as { version?: string };
        const version = body.version;
        if (health.ok && updateLanded(watch, version)) {
          const ready = await fetch("/readyz", {
            signal: controller.signal,
            cache: "no-store",
          });
          if (ready.ok && !disposed) {
            finish();
            window.location.reload();
            return;
          }
        } else if (health.ok && updateWentBackwards(watch, version)) {
          // No reason to wait out the budget: the executor already reported a
          // build older than the one it replaced.
          finish(`${version} on tag ${watch.tracked}`, "notNewer");
          return;
        }
        const current = await service.selfUpdateStatus(controller.signal);
        if (
          current.phase === "failed" &&
          (!current.target || current.target === watch.target) &&
          !disposed
        ) {
          finish(current.error ?? "Update failed");
          return;
        }
      } catch {
        /* Restart or an unavailable endpoint is not a confirmed failure. */
      } finally {
        clearTimeout(timeout);
      }
      if (!disposed) timer = setTimeout(() => void tick(), 3000);
    };
    timer = setTimeout(() => void tick(), 3000);
    return () => {
      disposed = true;
      clearTimeout(timer);
      controller?.abort();
    };
  }, [service, watch]);
  return { watch, failure, apply, stalled, availability: status.data };
}
