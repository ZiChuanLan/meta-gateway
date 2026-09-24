import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { api } from "../api/client";
import type { ModuleStatus } from "../api/types";
import { useSession } from "../session";

export const MODULES_QUERY_KEY = ["plugins-status"] as const;

/**
 * Loads module status for Admin UI gating.
 *
 * Everything the gateway ships with is always on: the two former store add-ons
 * (check-in, exchange) are built-in surfaces now, so the only things left here
 * are installable plugins — and they gate their own pages, not core screens.
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
	};
}
