import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { api } from "../api/client";
import type { ModuleStatus } from "../api/types";
import { useSession } from "../session";

export const MODULES_QUERY_KEY = ["plugins-status"] as const;

/** Official optional add-ons managed by the store. */
export const ADDON_EXCHANGE = "exchange";
export const ADDON_CHECKIN = "checkin";
export const ADDON_CPA = "cliproxyapi";

/**
 * Loads module status for Admin UI gating.
 * Core features are always on; only add-ons are toggleable.
 * Unknown/missing add-ons are treated as disabled (never optimistically "on").
 */
export function useModules() {
	const { client } = useSession();
	const service = client ? api(client) : null;
	const query = useQuery({
		queryKey: MODULES_QUERY_KEY,
		queryFn: ({ signal }) => service!.pluginsStatus(signal),
		enabled: Boolean(service),
		staleTime: 5_000,
		// Keep last known status while refetching after toggle to avoid UI thrash,
		// but first load must not pretend add-ons are enabled.
		placeholderData: (previous) => previous,
	});

	const byId = useMemo(() => {
		const map = new Map<string, ModuleStatus>();
		for (const item of query.data ?? []) {
			map.set(item.id, item);
		}
		return map;
	}, [query.data]);

	const isAddonEnabled = (id: string) => {
		const item = byId.get(id);
		if (!item) return false;
		return Boolean(item.enabled);
	};

	const addons = useMemo(
		() =>
			(query.data ?? []).filter(
				(item) => item.kind === "addon" && item.can_toggle,
			),
		[query.data],
	);
	const core = useMemo(
		() => (query.data ?? []).filter((item) => item.kind === "core"),
		[query.data],
	);

	return {
		...query,
		modules: query.data ?? [],
		addons,
		core,
		byId,
		isAddonEnabled,
		/** True only after we have a successful status payload at least once. */
		ready: Boolean(query.data),
		exchangeEnabled: isAddonEnabled(ADDON_EXCHANGE),
		checkinEnabled: isAddonEnabled(ADDON_CHECKIN),
		cpaEnabled: isAddonEnabled(ADDON_CPA),
	};
}
