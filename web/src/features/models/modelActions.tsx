import { ExternalLink, ListChecks, Pencil, Power, Shield, Sparkles, Trash2 } from "lucide-react";
import type { ActionMenuItem } from "../../components/ActionMenu";
import type { ModelMetadata, Route } from "../../api/types";

/**
 * The per-route action menu, as a function of the route and the board's verbs.
 *
 * Same shape as the connections board's menu (channels/channelActions.tsx): it was
 * a 107-line closure inside a two-thousand line component, reading eleven locals
 * out of the enclosing scope, so neither the set of actions nor their grouping
 * could be read or tested without the whole catalogue on screen. Every captured
 * value is now an explicit parameter.
 *
 * Pure mapping only: it decides and returns, the caller performs.
 */
type Text = (key: string, vars?: Record<string, string | number>) => string;

/** The slice of a mutation hook this menu touches. */
type Mutation = {
  pendingId?: number | string | null;
  isPending?: boolean;
  reset: () => void;
  /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
  mutate: (...args: any[]) => unknown;
};

export type ModelActionDeps = {
  t: Text;
  navigate: (to: string) => void;
  /** Registered metadata for a model name, used to seed the editor. */
  metaByModel: Map<string, ModelMetadata>;
  selectRow: (routeId: number) => void;
  setContextMenu: (value: null) => void;
  setBulkMode: (on: boolean) => void;
  setBulkSelected: (update: (current: Set<number>) => Set<number>) => void;
  setTryOpen: (open: boolean) => void;
  setEditMeta: (value: ModelMetadata) => void;
  setEdit: (route: Route) => void;
  setRemove: (route: Route) => void;
  mutations: {
    toggleRoute: Mutation;
    del: Mutation;
    save: Mutation;
  };
};

/** How each action is ranked into sections, and the section order. */
const RANKS: Record<string, number> = {
  try: 0,
  logs: 0,
  meta: 1,
  edit: 1,
  toggle: 1,
  bulk: 2,
  delete: 3,
};

const SECTIONS = ["actions.view", "actions.manage", "actions.selection", "actions.danger"];

export function modelActions(
  route: Route,
  deps: ModelActionDeps,
  options?: { closeContext?: boolean },
): ActionMenuItem[] {
  const { t, mutations } = deps;
  const busy = mutations.toggleRoute.pendingId === route.id || mutations.del.isPending;
  const close = () => {
    if (options?.closeContext) deps.setContextMenu(null);
  };
  const items: ActionMenuItem[] = [
    {
      key: "bulk",
      label: t("modelsPage.bulkMode"),
      icon: <ListChecks size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        deps.setBulkMode(true);
        deps.setBulkSelected((current) => new Set(current).add(route.id));
      },
    },
    {
      key: "try",
      label: t("try.open"),
      icon: <Sparkles size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        deps.selectRow(route.id);
        deps.setTryOpen(true);
      },
    },
    {
      key: "toggle",
      label: route.enabled ? t("common.disableAction") : t("common.enableAction"),
      icon: <Power size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        mutations.toggleRoute.mutate(route);
      },
    },
    {
      key: "logs",
      label: t("modelsPage.openLogs"),
      icon: <ExternalLink size={14} />,
      onSelect: () => {
        close();
        deps.navigate(`/logs?model=${encodeURIComponent(route.model_pattern)}`);
      },
    },
    {
      key: "meta",
      label: t("modelsPage.editMetadata"),
      icon: <Shield size={14} />,
      onSelect: () => {
        close();
        deps.setEditMeta(
          deps.metaByModel.get(route.model_pattern) ?? {
            model_name: route.model_pattern,
            context_window: 0,
            input_modalities: "",
            output_modalities: "",
            supports_thinking: -1,
            vendor: "",
            notes: "",
          },
        );
      },
    },
    {
      key: "edit",
      label: t("common.edit"),
      icon: <Pencil size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        mutations.save.reset();
        deps.setEdit(route);
      },
    },
    {
      key: "delete",
      label: t("common.delete"),
      icon: <Trash2 size={14} />,
      danger: true,
      disabled: busy,
      onSelect: () => {
        close();
        deps.setRemove(route);
      },
    },
  ];
  return items
    .sort((a, b) => (RANKS[a.key] ?? 1) - (RANKS[b.key] ?? 1))
    .map((item) => ({
      ...item,
      group: t(SECTIONS[RANKS[item.key] ?? 1]!),
      disabledReason: item.disabled ? t("common.working") : undefined,
    }));
}
