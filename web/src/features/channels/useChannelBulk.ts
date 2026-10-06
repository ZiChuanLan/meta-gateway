import { useState } from "react";
import type { api } from "../../api/client";
import type { ChannelOverview } from "../../api/types";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { runBatch } from "../../lib/batch";

/** One failed row of a bulk run, kept so the bar can report exactly which. */
export type BulkFailure = { item: number; error: unknown };

type Text = (key: string, vars?: Record<string, string | number>) => string;
type Toast = { push: (message: { tone: "success" | "error"; message: string }) => void };

/**
 * The two bulk operations on the connections board, and what is left of the
 * selection afterwards.
 *
 * Both share one rule, which is why they belong together: after a bulk run the
 * selection becomes **exactly the rows that failed**. A fully successful run
 * therefore clears it, and a partly successful one leaves the operator standing on
 * the failures — the list they have to act on next. Both also refresh the same
 * keys, so `invalidateKeys` is stated once here rather than in each page's copy.
 */
export function useChannelBulk({
  service,
  overviews,
  invalidateKeys,
  toast,
  t,
  setSelected,
}: {
  service: ReturnType<typeof api>;
  overviews: ChannelOverview[];
  invalidateKeys: readonly (readonly string[])[];
  toast: Toast;
  t: Text;
  setSelected: (next: Set<number>) => void;
}) {
  const [failures, setFailures] = useState<BulkFailure[]>([]);

  const report = (key: string, ok: number, total: number) =>
    toast.push({
      tone: ok === total ? "success" : "error",
      message: t(key, { ok, total }),
    });

  const sync = useAdminMutation({
    mutationFn: async (ids: number[]) => runBatch(ids, (id) => service.refreshChannel(id)),
    invalidateKeys: [...invalidateKeys],
    onSuccess: ({ ok, total, failures: failed }) => {
      report("channels.bulkSyncDone", ok, total);
      setSelected(new Set(failed.map((failure) => failure.item)));
      setFailures(failed);
    },
  });

  const status = useAdminMutation({
    mutationFn: async (input: { ids: number[]; status: "enabled" | "disabled" }) =>
      runBatch(input.ids, (id) => {
        const overview = overviews.find((o) => o.channel.id === id);
        // A selected row whose overview is gone (deleted between the selection and
        // this click) was NOT updated. Reject so it counts as a failure instead of
        // silently inflating the success tally.
        if (!overview) {
          return Promise.reject(new Error(`channel ${id} is no longer available`));
        }
        return service.updateChannel(id, { ...overview.channel, status: input.status });
      }),
    invalidateKeys: [...invalidateKeys],
    onSuccess: ({ ok, total, failures: failed }) => {
      report("channels.bulkStatusDone", ok, total);
      setSelected(new Set(failed.map((failure) => failure.item)));
      setFailures(failed);
    },
  });

  return {
    failures,
    sync,
    status,
    busy: sync.isPending || status.isPending,
  };
}
