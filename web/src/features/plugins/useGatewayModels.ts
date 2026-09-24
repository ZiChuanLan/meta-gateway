import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";

type ModelSource = Pick<ReturnType<typeof api>, "routeOverviews">;

/**
 * The model names the gateway can actually route.
 *
 * This is the only honest source for any control that picks a model: a setting
 * built around a name nothing routes can only ever fail, and the failure shows
 * up far from the field that caused it. Wildcards are matchers rather than
 * callable models, so `gpt-*` is not offered as a choice.
 *
 * The query key matches the model page's, so opening a settings panel that
 * needs models reuses what that page already fetched.
 */
export function useGatewayModels(service: ModelSource, enabled = true): string[] {
	const routes = useQuery({
		queryKey: ["route-overviews"],
		queryFn: ({ signal }) => service.routeOverviews(signal),
		staleTime: 60_000,
		enabled,
	});
	return useMemo(
		() =>
			Array.from(
				new Set(
					(routes.data ?? [])
						.map((item) => item.route.model_pattern)
						.filter((pattern) => pattern && !/[*?]/.test(pattern)),
				),
			).sort(),
		[routes.data],
	);
}
