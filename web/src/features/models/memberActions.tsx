import { Pencil, Power, Target, Trash2 } from "lucide-react";
import type { ActionMenuItem } from "../../components/ActionMenu";
import type { Route } from "../../api/types";

/**
 * The per-member action menu on a route's routing list.
 *
 * The fourth extraction of this shape (channels, routes, and now members): a menu
 * built inline inside a 600-line member row. Generic over the member record so the
 * caller's own type flows through — `toggleMember`, `setMember` and
 * `setRemoveMember` all take the full entry, and a narrower structural type here
 * would force casts at the call site.
 */
type Text = (key: string, vars?: Record<string, string | number>) => string;

export type MemberActionTarget = { id: number; enabled: boolean };

export type MemberActionDeps<M extends MemberActionTarget> = {
  t: Text;
  /** The route these members belong to; null while it is still loading. */
  route: Route | null;
  /** How many members the route has — solo is meaningless with one. */
  memberCount: number;
  /** Whether the gateway considers this member's health resettable. */
  canResetHealth: boolean;
  /** Whether the reset clears a cooldown or recovers a failure — it is one action. */
  resetIsCooldown: boolean;
  mutations: {
    toggleMember: { mutate: (entry: M) => unknown };
    pinMember: {
      isPending?: boolean;
      mutate: (input: { route: Route; memberId: number | null }) => unknown;
    };
    clearHealth: { isPending?: boolean; mutate: (id: number) => unknown };
    saveMember: { reset: () => void };
  };
  setMember: (entry: M) => void;
  setRemoveMember: (entry: M) => void;
};

export function memberActions<M extends MemberActionTarget>(
  entry: M,
  deps: MemberActionDeps<M>,
): ActionMenuItem[] {
  const { t, route, mutations } = deps;
  const pinned = route?.routing_mode === "single" && route.single_member_id === entry.id;
  return [
    {
      key: "toggle",
      icon: <Power size={14} />,
      label: entry.enabled ? t("common.disableAction") : t("common.enableAction"),
      onSelect: () => mutations.toggleMember.mutate(entry),
    },
    ...(deps.memberCount > 1
      ? [
          {
            key: "solo",
            icon: <Target size={14} />,
            label: pinned ? t("routing.unsoloMember") : t("routing.soloMember"),
            disabled: mutations.pinMember.isPending,
            onSelect: () => {
              if (!route) return;
              mutations.pinMember.mutate({
                route,
                memberId: pinned ? null : entry.id,
              });
            },
          },
        ]
      : []),
    ...(deps.canResetHealth
      ? [
          {
            key: "clear",
            label: t(deps.resetIsCooldown ? "routing.clearHealth" : "routing.recoverMember"),
            onSelect: () => mutations.clearHealth.mutate(entry.id),
          },
        ]
      : []),
    {
      key: "edit",
      label: t("common.edit"),
      icon: <Pencil size={14} />,
      onSelect: () => {
        mutations.saveMember.reset();
        deps.setMember(entry);
      },
    },
    {
      key: "delete",
      label: t("common.delete"),
      icon: <Trash2 size={14} />,
      danger: true,
      onSelect: () => deps.setRemoveMember(entry),
    },
  ];
}
