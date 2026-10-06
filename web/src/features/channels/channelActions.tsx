import {
  CalendarCheck,
  Copy,
  ExternalLink,
  KeyRound,
  ListChecks,
  PanelRightOpen,
  Pencil,
  Play,
  Plus,
  Power,
  RefreshCw,
  Trash2,
  UserCheck,
} from "lucide-react";
import type { ActionMenuItem } from "../../components/ActionMenu";
import type { Channel, ChannelOverview } from "../../api/types";
import { capabilityFlags, needsVerify } from "./helpers";

/**
 * The per-channel action menu, as a function of the data and the board's verbs.
 *
 * It used to be a 256-line closure inside the channels page: it read eleven
 * mutations, seven setters, the router and the toast out of the enclosing scope,
 * so nothing about it — which actions exist, when each is enabled, how they are
 * ranked into sections — could be read or tested without the whole two-thousand
 * line component on screen. Every one of those closures is now an explicit
 * parameter, which is what makes the answer to "what can I do to a channel?"
 * visible in one file.
 *
 * Pure mapping only: it decides and returns, the caller performs.
 */
type Text = (key: string, vars?: Record<string, string | number>) => string;

/** The slice of a mutation hook this menu touches. */
type PendingMutation = {
  /** `useAdminMutation` reports null/undefined (or a string) while idle. */
  pendingId: number | string | null | undefined;
  reset: () => void;
  /**
   * The menu passes whatever the hook expects and never reads the result, so the
   * seam stays untyped on purpose: `unknown[]` would reject every real call (the
   * hook's parameters are narrower than `unknown`), and naming all eleven hook
   * signatures here would make this file about react-query instead of about what
   * an operator can do to a channel.
   */
  /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
  mutate: (...args: any[]) => unknown;
};

export type ChannelActionDeps = {
  t: Text;
  navigate: (to: string) => void;
  pushError: (error: unknown) => void;
  /** The channel's user credential, used to label the check-in actions. */
  userCredentialFor: (overview: ChannelOverview) => { id: number } | undefined;
  selectRow: (id: number) => void;
  setContextMenu: (value: null) => void;
  setBulkMode: (value: boolean) => void;
  setBulkSelected: (update: (current: Set<number>) => Set<number>) => void;
  setEdit: (channel: Channel) => void;
  setCreateKeyChannel: (channel: Channel) => void;
  setRemove: (channel: Channel) => void;
  setInspectorOpen: (open: boolean) => void;
  mutations: {
    refresh: PendingMutation;
    probe: PendingMutation;
    accountProbe: PendingMutation;
    syncKeys: PendingMutation;
    toggle: PendingMutation;
    del: PendingMutation;
    duplicate: PendingMutation;
    /** Only reset here: the dialog that opens does the creating. */
    createUpstreamKey: Pick<PendingMutation, "pendingId" | "reset">;
    setCheckin: PendingMutation;
    runCheckin: PendingMutation;
    saveEdit: PendingMutation;
  };
};

/** How each action is grouped, in the order the sections appear. */
const RANKS: Record<string, number> = {
  details: 0,
  models: 0,
  logs: 0,
  edit: 1,
  duplicate: 1,
  toggle: 1,
  bulk: 3,
  delete: 4,
};

const SECTIONS = [
  "actions.view",
  "actions.manage",
  "actions.maintenance",
  "actions.selection",
  "actions.danger",
] as const;

export function channelActions(
  overview: ChannelOverview,
  deps: ChannelActionDeps,
  options?: { closeContext?: boolean },
): ActionMenuItem[] {
  const { t, mutations } = deps;
  const ch = overview.channel;
  const caps = capabilityFlags(overview);
  const busy =
    mutations.refresh.pendingId === ch.id ||
    mutations.probe.pendingId === ch.id ||
    mutations.accountProbe.pendingId === ch.id ||
    mutations.syncKeys.pendingId === ch.id ||
    mutations.toggle.pendingId === ch.id ||
    mutations.del.pendingId === ch.id;
  const close = () => {
    if (options?.closeContext) deps.setContextMenu(null);
  };
  const items: ActionMenuItem[] = [
    {
      key: "bulk",
      label: t("channels.bulkMode"),
      icon: <ListChecks size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        deps.setBulkMode(true);
        deps.setBulkSelected((current) => new Set(current).add(ch.id));
      },
    },
    {
      key: "edit",
      label: t("common.edit"),
      icon: <Pencil size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        mutations.saveEdit.reset();
        deps.setEdit(ch);
      },
    },
  ];
  if (caps.accountSupported && caps.hasUser) {
    items.push({
      key: "check-account",
      label: t("channels.checkAccount"),
      icon: <UserCheck size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        deps.selectRow(ch.id);
        mutations.accountProbe.reset();
        mutations.accountProbe.mutate(ch.id, {
          onError: (err: unknown) => deps.pushError(err),
        });
      },
    });
  }
  if (caps.accountSupported && caps.hasUser && caps.needsKeyForRelay) {
    items.push({
      key: "sync-keys",
      label: t("channels.syncKeys"),
      icon: <KeyRound size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        deps.selectRow(ch.id);
        mutations.syncKeys.reset();
        mutations.syncKeys.mutate(ch.id);
      },
    });
  }
  // Only offer key creation when the account token is known-good (last probe
  // succeeded for this channel). A dead/blocked token should never show a create
  // button that can only fail. Keys can be created even when the site already has
  // keys — group-scoped upstreams (New API groups) typically need one per group.
  const canCreateKey =
    caps.accountSupported &&
    caps.hasUser &&
    Boolean(overview.last_probe_at) &&
    overview.last_probe_ok === true;
  if (canCreateKey) {
    items.push({
      key: "create-key",
      label: t("channels.createKey"),
      icon: <Plus size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        deps.selectRow(ch.id);
        mutations.createUpstreamKey.reset();
        deps.setCreateKeyChannel(ch);
      },
    });
  }
  if (caps.hasAPIKey && !needsVerify(overview)) {
    items.push({
      key: "test",
      label: t("channels.test"),
      icon: <Play size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        deps.selectRow(ch.id);
        mutations.probe.reset();
        mutations.probe.mutate(ch.id);
      },
    });
  }
  items.push({
    key: "duplicate",
    label: t("channels.duplicate"),
    icon: <Copy size={14} />,
    disabled: busy,
    onSelect: () => {
      close();
      mutations.duplicate.reset();
      mutations.duplicate.mutate(ch.id);
    },
  });
  items.push(
    {
      key: "sync",
      label: t("channels.fetchModels"),
      icon: <RefreshCw size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        deps.selectRow(ch.id);
        mutations.refresh.reset();
        mutations.refresh.mutate(ch.id);
        // Model import doubles as an availability check; also refresh the
        // access-token status while we are at it.
        if (caps.accountSupported && caps.hasUser) {
          mutations.accountProbe.reset();
          mutations.accountProbe.mutate(ch.id);
        }
      },
    },
    {
      key: "toggle",
      label: ch.status === "enabled" ? t("common.disableAction") : t("common.enableAction"),
      icon: <Power size={14} />,
      disabled: busy,
      onSelect: () => {
        close();
        mutations.toggle.mutate(overview);
      },
    },
    {
      key: "models",
      label: t("channels.openModels"),
      icon: <ExternalLink size={14} />,
      onSelect: () => {
        close();
        deps.navigate(`/models?channel_id=${ch.id}`);
      },
    },
    {
      key: "logs",
      label: t("channels.openLogs"),
      icon: <ExternalLink size={14} />,
      onSelect: () => {
        close();
        deps.navigate(`/logs?channel_id=${ch.id}`);
      },
    },
    ...(() => {
      // Label must follow overview badge (site-level schedule), not an arbitrary
      // first token.
      const scheduleOn = Boolean(overview.checkin_enabled);
      const checkinCred = deps.userCredentialFor(overview);
      if (!checkinCred && !caps.hasUser) return [];
      // Has user token on overview but credentials list not loaded yet: still show
      // the correct label.
      const canToggle = Boolean(checkinCred);
      return [
        {
          key: "checkin-toggle",
          label: scheduleOn ? t("channels.checkinDisable") : t("channels.checkinEnable"),
          icon: <CalendarCheck size={14} />,
          disabled:
            busy ||
            !canToggle ||
            (checkinCred != null && mutations.setCheckin.pendingId === checkinCred.id),
          onSelect: () => {
            close();
            if (!checkinCred) return;
            mutations.setCheckin.mutate({
              credentialId: checkinCred.id,
              enabled: !scheduleOn,
            });
          },
        },
        {
          key: "checkin-run",
          label: t("channels.checkinRun"),
          icon: <CalendarCheck size={14} />,
          disabled:
            busy ||
            !canToggle ||
            (checkinCred != null && mutations.runCheckin.pendingId === checkinCred.id),
          onSelect: () => {
            close();
            if (!checkinCred) return;
            mutations.runCheckin.mutate(checkinCred.id);
          },
        },
      ];
    })(),
    {
      key: "delete",
      label: t("common.delete"),
      icon: <Trash2 size={14} />,
      danger: true,
      disabled: busy,
      onSelect: () => {
        close();
        deps.setRemove(ch);
      },
    },
  );
  items.unshift({
    key: "details",
    label: t("channels.details"),
    icon: <PanelRightOpen size={14} />,
    onSelect: () => {
      close();
      deps.selectRow(ch.id);
      deps.setInspectorOpen(true);
    },
  });
  return items
    .sort((a, b) => (RANKS[a.key] ?? 2) - (RANKS[b.key] ?? 2))
    .map((item) => ({
      ...item,
      group: t(SECTIONS[RANKS[item.key] ?? 2]!),
      disabledReason: item.disabled
        ? t(busy ? "common.working" : "actions.accountUnavailable")
        : undefined,
    }));
}
