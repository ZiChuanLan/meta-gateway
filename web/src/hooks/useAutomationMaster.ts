import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import type { RuntimeEditableSettings } from "../api/types";
import { useAdminMutation } from "./useAdminMutation";
import { useSession } from "../session";

/**
 * The master switch behind the console's scheduled automations.
 *
 * Check-in and keepalive each have one: a per-account switch makes an account a
 * participant, and this one decides whether the scheduler runs at all. Both ship
 * off, and both live in 运行设置 — which is far from the page an operator is on
 * when they turn one account on. An account that is "on" while the master is off
 * is indistinguishable from a broken feature: the row says it is scheduled and
 * nothing ever happens.
 *
 * So the places that turn an account on turn the master on too, through here,
 * and the page shows the switch rather than hiding the state two pages away.
 */
/** What the master-switch hook hands back, for the pages that take it as a prop. */
export type AutomationMaster = ReturnType<typeof useAutomationMaster>;

export function useAutomationMaster(field: "checkin_enabled" | "keepalive_enabled") {
  const { client } = useSession();
  const queryClient = useQueryClient();
  const service = api(client!);
  const queryKey = ["runtime-settings"] as const;
  const settings = useQuery({
    queryKey,
    queryFn: ({ signal }) => service.runtimeSettings(signal),
  });
  const current = settings.data?.editable;
  const save = useAdminMutation({
    mutationFn: (next: RuntimeEditableSettings) => service.updateRuntimeSettings(next),
    invalidateKeys: [queryKey],
  });

  /** The editable block, fetched on demand: the caller's page may never have
   *  loaded it, and guessing "off" would silently skip the switch. */
  const editable = async (): Promise<RuntimeEditableSettings | null> => {
    if (current) return current;
    const fresh = await queryClient.fetchQuery({
      queryKey,
      queryFn: ({ signal }) => service.runtimeSettings(signal),
    });
    return fresh.editable ?? null;
  };

  /** What a switch needs when it is turned on: the check-in scheduler also needs
   *  a schedule, or "on" would mean a scheduler idling on an empty cron. */
  const turningOn = (base: RuntimeEditableSettings): RuntimeEditableSettings => {
    const next = { ...base, [field]: true };
    if (field === "checkin_enabled" && !next.checkin_cron) next.checkin_cron = "0 8 * * *";
    return next;
  };

  return {
    enabled: Boolean(current?.[field]),
    ready: current != null,
    saving: save.isPending,
    setEnabled: async (value: boolean) => {
      const base = await editable();
      if (!base) return;
      await save.mutateAsync(value ? turningOn(base) : { ...base, [field]: false });
    },
    /** Turn the master on if it is off. True when this call changed it. */
    enable: async () => {
      const base = await editable();
      if (!base || base[field]) return false;
      await save.mutateAsync(turningOn(base));
      return true;
    },
  };
}
