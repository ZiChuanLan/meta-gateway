import { useMemo } from "react";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import type { api } from "../../api/client";
import type { FinanceItem, ModelMetadata, RouteOverview } from "../../api/types";
import { useModules } from "../../hooks/useModules";

/** Whatever the client's own method returns — the queries must not restate it. */
type Service = ReturnType<typeof api>;
type Resolved<T> = T extends Promise<infer U> ? U : never;

/**
 * Everything the models workspace reads: the eight queries it is built from, and
 * the three derived lookups the rest of the page treats as facts.
 *
 * Kept apart from the page for the same reason the boards' other state owners
 * exist — the difference between "what does this page fetch" and "what does this
 * page do" was invisible when both lived in one 1,200-line component, and the
 * refetch intervals in particular are a policy that deserves its own file:
 *
 *  - route overviews and the sticky pins poll at 15s, because a route entering or
 *    leaving a cooldown is live information;
 *  - metadata, missing models and plugin hooks poll at 60s;
 *  - finance is cached for two minutes (the backend caches it upstream);
 *  - `sticky` and `runtimeSettings` deliberately do not retry: they are optional
 *    context, and a failing retry loop would put two red toasts on a page whose
 *    primary function does not depend on them.
 */
export type ModelsBoard = {
  overviews: UseQueryResult<RouteOverview[]>;
  channels: UseQueryResult<Resolved<ReturnType<Service["channels"]>>>;
  sticky: UseQueryResult<Resolved<ReturnType<Service["sticky"]>>>;
  runtimeSettings: UseQueryResult<Resolved<ReturnType<Service["runtimeSettings"]>>>;
  finance: UseQueryResult<Resolved<ReturnType<Service["finance"]>>>;
  missing: UseQueryResult<Resolved<ReturnType<Service["missingModels"]>>>;
  metadata: UseQueryResult<Resolved<ReturnType<Service["modelMetadata"]>>>;
  pluginHooks: UseQueryResult<Resolved<ReturnType<Service["pluginHooks"]>>>;
  /** Models a plugin answers for, as list rows (no route, no members). */
  virtualModels: Array<{ model: string; pluginId: string; pluginName: string }>;
  /** Registered metadata by model name, for the badges and the editor. */
  metaByModel: Map<string, ModelMetadata>;
  /** Where a plugin-answered row links to (see the note further down). */
  pluginPageOf: (pluginId: string) => string;
  /** Finance rows for the members' credit/price display; empty when unknown. */
  financeItems: FinanceItem[];
};

export function useModelsBoard({ service }: { service: ReturnType<typeof api> }): ModelsBoard {
  const modules = useModules();
  const overviews = useQuery({
    queryKey: ["route-overviews"],
    queryFn: ({ signal }) => service.routeOverviews(signal),
    refetchInterval: 15_000,
  });
  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: ({ signal }) => service.channels(signal),
  });
  const sticky = useQuery({
    queryKey: ["sticky"],
    queryFn: ({ signal }) => service.sticky(signal),
    retry: false,
    refetchInterval: 15_000,
  });
  const runtimeSettings = useQuery({
    queryKey: ["runtime-settings"],
    queryFn: ({ signal }) => service.runtimeSettings(signal),
    retry: false,
  });
  // Account finances (balance + per-model price per channel), cached upstream for
  // a short TTL; used to show call price / affordable calls per member.
  const finance = useQuery({
    queryKey: ["finance"],
    queryFn: ({ signal }) => service.finance(signal),
    retry: false,
    refetchInterval: 120_000,
  });
  // Models exposed by channels but not covered by any enabled route.
  const missing = useQuery({
    queryKey: ["missing-models"],
    queryFn: ({ signal }) => service.missingModels(signal),
    refetchInterval: 60_000,
  });
  // Model metadata library (capability annotations shown as badges).
  const metadata = useQuery({
    queryKey: ["model-metadata"],
    queryFn: ({ signal }) => service.modelMetadata(signal),
    refetchInterval: 60_000,
  });
  // Models a plugin answers for. They have no route of their own — a route hook
  // rewrites the request before selection — so they never appear in the route list,
  // and without this the console gives no sign that a model the downstream
  // catalogue advertises exists at all.
  const pluginHooks = useQuery({
    queryKey: ["plugins-hooks"],
    queryFn: ({ signal }) => service.pluginHooks(signal),
    refetchInterval: 60_000,
  });

  const virtualModels = useMemo(() => {
    const hooks = pluginHooks.data?.hooks ?? [];
    const out: Array<{ model: string; pluginId: string; pluginName: string }> = [];
    const seen = new Set<string>();
    for (const hook of hooks) {
      if (hook.point !== "route") continue;
      for (const pattern of hook.match_models ?? []) {
        // A wildcard is a matcher, not a callable model name: same rule the gateway
        // applies when it builds the downstream catalogue.
        if (!pattern || /[*?]/.test(pattern) || seen.has(pattern)) continue;
        seen.add(pattern);
        out.push({
          model: pattern,
          pluginId: hook.plugin_id,
          pluginName: hook.plugin_name || hook.plugin_id,
        });
      }
    }
    return out.sort((a, b) => a.model.localeCompare(b.model));
  }, [pluginHooks.data]);

  const metaByModel = useMemo(() => {
    const map = new Map<string, ModelMetadata>();
    for (const item of metadata.data?.items ?? []) {
      map.set(item.model_name, item);
    }
    return map;
  }, [metadata.data]);

  /**
   * Where a plugin-answered model row links to. The console router mounts under
   * basename="/console", so an in-app target is route-relative —
   * "/plugins/<id>". Writing "/console/plugins/<id>" resolves to
   * /console/console/plugins/<id>, matches no route, and silently bounces back to
   * the overview through the catch-all redirect. The backend already answers with
   * the right path (`open_path`, plugins/service.go openPathFor); the literal is
   * the fallback for a plugin whose status record has not loaded.
   */
  const pluginPageOf = (pluginId: string) =>
    modules.byId.get(pluginId)?.open_path || `/plugins/${encodeURIComponent(pluginId)}`;

  const financeItems = useMemo<FinanceItem[]>(() => finance.data?.items ?? [], [finance.data]);

  return {
    overviews,
    channels,
    sticky,
    runtimeSettings,
    finance,
    missing,
    metadata,
    pluginHooks,
    virtualModels,
    metaByModel,
    pluginPageOf,
    financeItems,
  };
}
