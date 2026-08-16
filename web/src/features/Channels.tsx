import { ExternalLink, KeyRound, Pencil, Play, Plus, Power, RefreshCw, Copy, Search, CalendarCheck, Trash2, UserCheck } from "lucide-react"
import { useQuery } from "@tanstack/react-query"
import { useEffect, useMemo, useRef, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { api } from "../api/client"
import type { Channel, ChannelOverview, Site } from "../api/types"
import { ChannelModelsPanel } from "./ChannelModels"
import { ChannelKeysDrawer } from "./ChannelKeys"
import { ActionMenu, type ActionMenuItem } from "../components/ActionMenu"
import { Drawer } from "../components/Drawer"
import { EmptyHero } from "../components/EmptyHero"
import { ListShell } from "../components/ListShell"
import { PaginationBar } from "../components/PaginationBar"
import { EntityState } from "../components/EntityState"
import { ResultStrip } from "../components/ResultStrip"
import { StatGrid } from "../components/StatGrid"
import { Button, ConfirmDialog, DataTable, Page, Panel } from "../components/ui"
import { useAdminMutation } from "../hooks/useAdminMutation"
import { useClientPagination } from "../hooks/useClientPagination"
import { useI18n } from "../i18n"
import { useToast } from "../toast"
import { formatErrorMessage } from "../formatError"
import { useSession } from "../session"
import { useModules } from "../hooks/useModules"
import { channelNeedsAttention, isChannelReady } from "./channelHealth"
import { AddChannelDialog } from "./channels/AddChannelDialog"
import { CreateKeyDialog } from "./channels/CreateKeyDialog"
import { EditChannelDialog } from "./channels/EditChannelDialog"
import { ChannelDetail } from "./channels/ChannelDetail"
import { ChannelStatusBadges } from "./channels/badges"
import { capabilityFlags, hostLabel, isMissingAPIKey, needsVerify, normalizeBase, SECRET_MASK, type ConnectionHealthFilter, type CreateConnectionInput, type CreateConnectionResult } from "./channels/helpers"
import { positiveId } from "../lib/positiveId"
export { channelReadiness } from "./channelHealth"

const INVALIDATE = [
  ["channel-overviews"],
  ["channels"],
  ["sites"],
  ["models"],
  ["routes"],
  ["route-overviews"],
  ["discovered-models"],
] as const;

export function Channels() {
  const { client } = useSession();
  const { checkinEnabled } = useModules();
  const { t } = useI18n();
  const toast = useToast();
  const service = api(client!);
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const overviews = useQuery({
    queryKey: ["channel-overviews"],
    queryFn: ({ signal }) => service.channelOverviews(signal),
  });
  const sites = useQuery({
    queryKey: ["sites"],
    queryFn: ({ signal }) => service.sites(signal),
  });
  const routeOverviewsQuery = useQuery({
    queryKey: ["route-overviews"],
    queryFn: ({ signal }) => service.routeOverviews(signal),
  });
  const [addOpen, setAddOpen] = useState(false);
  const [remove, setRemove] = useState<Channel | null>(null);
const [edit, setEdit] = useState<Channel | null>(null);
const [modelsChannel, setModelsChannel] = useState<Channel | null>(null);
const [keysChannel, setKeysChannel] = useState<Channel | null>(null);
  const [createKeyChannel, setCreateKeyChannel] = useState<Channel | null>(
    null,
  );
  // Synchronous lock for the create-key dialog (see onCreate re-entry guard).
  const createKeyLocked = useRef(false);
  const [contextMenu, setContextMenu] = useState<{
    channelId: number;
    top: number;
    left: number;
  } | null>(null);
  const [query, setQuery] = useState("");
  const [healthFilter, setHealthFilter] =
    useState<ConnectionHealthFilter>("all");
  const [stageMessage, setStageMessage] = useState<{
    kind: "created" | "created_and_verified" | "verify_failed";
    name: string;
    channelId: number;
    models?: number;
  } | null>(null);
  const selectedId = positiveId(params.get("id"));
  // Load site credentials for the channel being edited, selected, or opened via ⋯/context menu.
  const credentialSiteId =
    edit?.site_id ??
    (selectedId != null
      ? (overviews.data ?? []).find((row) => row.channel.id === selectedId)
          ?.channel.site_id
      : undefined) ??
    (contextMenu != null
      ? (overviews.data ?? []).find(
          (row) => row.channel.id === contextMenu.channelId,
        )?.channel.site_id
      : undefined);
  const credentials = useQuery({
    queryKey: ["credentials", credentialSiteId],
    queryFn: ({ signal }) =>
      service.credentials(credentialSiteId as number, signal),
    enabled: typeof credentialSiteId === "number" && credentialSiteId > 0,
  });
  const verifyAfterCreate = useRef(false);
  const runVerifyRef = useRef<(channelId: number, name: string) => void>(
    () => undefined,
  );

  const refresh = useAdminMutation({
    mutationFn: (id: number) => service.refreshChannel(id),
    invalidateKeys: [...INVALIDATE],
    pendingIdOf: (id) => id,
  });

  runVerifyRef.current = (channelId: number, name: string) => {
    refresh.reset();
    refresh.mutate(channelId, {
      onSuccess: (refreshResult) => {
        setStageMessage({
          kind: "created_and_verified",
          name,
          channelId,
          models: refreshResult.models.length,
        });
      },
      onError: () => {
        setStageMessage({
          kind: "verify_failed",
          name,
          channelId,
        });
      },
    });
  };

  // Health probing is driven by the backend health sweep (default on) and by
  // the explicit per-channel actions below; entering this page does not fire
  // network pings for every enabled connection anymore.
  const createConnection = useAdminMutation({
    mutationFn: async (
      input: CreateConnectionInput,
    ): Promise<CreateConnectionResult> => {
      const base = normalizeBase(input.base_url);
      const name = input.name.trim() || hostLabel(base);
      const existing = (sites.data ?? []).find(
        (site) => normalizeBase(site.base_url) === base,
      );
      let site: Site;
      let reusedSite = false;
      if (existing) {
        site = existing;
        reusedSite = true;
      } else {
        site = await service.createSite({
          name,
          base_url: base,
          platform: input.type_hint || "openai-compatible",
          status: "enabled",
        });
      }
      const credential = await service.createCredential(site.id, {
        kind: "api_key",
        secret: input.secret,
        status: "enabled",
      });
      const channel = await service.createChannel({
        name,
        site_id: site.id,
        credential_id: credential.id,
        base_url: "",
        group_name: "default",
        priority: 0,
        weight: 100,
        status: "enabled",
        type_hint: input.type_hint || "openai-compatible",
      });
      return {
        channel,
        reusedSite,
        looksLikeAccessToken: !/^sk-/i.test(input.secret.trim()),
      };
    },
    invalidateKeys: [...INVALIDATE],
    toastOnError: false,
    onSuccess: (result) => {
      setAddOpen(false);
      setParams({ id: String(result.channel.id) }, { replace: true });
      const shouldVerify = verifyAfterCreate.current;
      verifyAfterCreate.current = false;
      setStageMessage({
        kind: "created",
        name: result.channel.name,
        channelId: result.channel.id,
      });
      if (shouldVerify) {
        runVerifyRef.current(result.channel.id, result.channel.name);
      }
      // Auto-check the access token right after creation so the UI can
      // tell a good credential from a dead/blocked one immediately.
      if (result.looksLikeAccessToken) {
        accountProbe.reset();
        accountProbe.mutate(result.channel.id);
      }
    },
    onError: () => {
      verifyAfterCreate.current = false;
    },
  });
  const refreshAll = useAdminMutation({
    mutationFn: () => service.refreshAll(),
    invalidateKeys: [...INVALIDATE],
  });

const probe = useAdminMutation({
	mutationFn: (id: number) => service.probeChannel(id),
	invalidateKeys: [...INVALIDATE],
	pendingIdOf: (id) => id,
});

const ping = useAdminMutation({
	mutationFn: (id: number) => service.pingChannel(id),
	invalidateKeys: [...INVALIDATE],
	pendingIdOf: (id) => id,
});
  const accountProbe = useAdminMutation({
    mutationFn: (id: number) => service.probeAccount(id),
    pendingIdOf: (id) => id,
    invalidateKeys: [...INVALIDATE],
    // Errors stay silent by default: this hook also fires as a side effect of
    // "fetch models" / connection creation, where a second error toast on top
    // of the primary action's toast reads as a duplicate prompt.
    toastOnError: false,
  });
  const checkAllTokens = useAdminMutation({
    mutationFn: () => service.probeAllAccounts(),
    invalidateKeys: [...INVALIDATE],
  });
  const syncKeys = useAdminMutation({
    mutationFn: (id: number) => service.syncKeys(id),
    invalidateKeys: [...INVALIDATE, ["credentials"]],
    pendingIdOf: (id) => id,
  });
  const duplicate = useAdminMutation({
    mutationFn: (id: number) => service.duplicateChannel(id),
    invalidateKeys: [...INVALIDATE] as const,
    pendingIdOf: (id) => id,
  });
  const createUpstreamKey = useAdminMutation({
    mutationFn: (input: { id: number; name?: string; group?: string }) =>
      service.createUpstreamKey(input.id, {
        name: input.name,
        group: input.group,
        unlimited_quota: true,
      }),
    invalidateKeys: [...INVALIDATE, ["credentials"]],
    pendingIdOf: (input) => input.id,
    toastOnError: false,
  });
  const toggle = useAdminMutation({
    mutationFn: async (overview: ChannelOverview) => {
      const next =
        overview.channel.status === "enabled" ? "disabled" : "enabled";
      return service.updateChannel(overview.channel.id, {
        ...overview.channel,
        status: next,
      });
    },
    invalidateKeys: [...INVALIDATE],
    pendingIdOf: (overview) => overview.channel.id,
  });
  const saveEdit = useAdminMutation({
    mutationFn: async (input: {
      channel: Channel;
      site?: Site;
      userCredential?: {
        id: number;
        kind: string;
        has_secret: boolean;
        checkin_enabled: boolean;
      };
      relayCredential?: {
        id: number;
        kind: string;
        has_secret: boolean;
        checkin_enabled: boolean;
      };
      name: string;
      base_url: string;
      type_hint: string;
      max_reasoning_effort?: string;
  payload_rules?: string;
  proxy_url?: string;
  max_concurrent?: number;
      priority: number;
      weight: number;
      header_override?: string;
      system_prompt?: string;
      retry_config?: string;
      stable_first?: boolean;
      userToken: string;
      apiKey: string;
    }) => {
      const name = input.name.trim() || input.channel.name;
      const base = normalizeBase(input.base_url);
      const typeHint = input.type_hint || "openai-compatible";
      if (input.site && !input.channel.base_url.trim()) {
        await service.updateSite(input.site.id, {
          ...input.site,
          name,
          base_url: base,
          platform: typeHint,
        });
      }
      // Keep user token and API key as separate credentials.
      // Never overwrite access_token with sk- on the same row.
      let relayCredentialId = input.channel.credential_id;
      const userTokenRaw = input.userToken.trim();
      // The mask means "keep the stored value"; an empty field means "remove the credential".
      const userTokenKept = userTokenRaw === SECRET_MASK;
      const userToken = userTokenKept ? "" : userTokenRaw;
      const apiKey = input.apiKey.trim();
      const userCred = input.userCredential;
      const relayCred = input.relayCredential;
      if (userToken && input.site) {
        if (userCred?.id) {
          const kind =
            userCred.kind === "session" || userCred.kind === "access_token"
              ? userCred.kind
              : "access_token";
          await service.updateCredential(userCred.id, {
            kind,
            secret: userToken,
            status: "enabled",
          });
        } else {
          await service.createCredential(input.site.id, {
            kind: "access_token",
            secret: userToken,
            status: "enabled",
          });
        }
      } else if (userCred?.id && input.site && !userTokenKept) {
        // Field was explicitly cleared → remove the access-token credential.
        await service.deleteCredential(userCred.id);
      }
      if (apiKey && input.site) {
        if (relayCred?.id && relayCred.kind === "api_key") {
          await service.updateCredential(relayCred.id, {
            kind: "api_key",
            secret: apiKey,
            status: "enabled",
          });
          relayCredentialId = relayCred.id;
        } else {
          const created = await service.createCredential(input.site.id, {
            kind: "api_key",
            secret: apiKey,
            status: "enabled",
          });
          relayCredentialId = created.id;
        }
      } else if (
        relayCred?.id &&
        relayCred.kind === "api_key" &&
        relayCred.has_secret
      ) {
        // Keep existing relay key bound when operator only edits other fields.
        relayCredentialId = relayCred.id;
      }
      const channelBase = input.channel.base_url.trim()
        ? base
        : input.channel.base_url;
      const siteId = input.channel.site_id ?? input.site?.id;
      return service.updateChannel(input.channel.id, {
        ...input.channel,
        name,
        base_url: channelBase,
        type_hint: typeHint,
        priority: input.priority,
        weight: input.weight,
        max_reasoning_effort: input.max_reasoning_effort ?? "",
        payload_rules: input.payload_rules ?? "",
        proxy_url: input.proxy_url ?? "",
        max_concurrent: input.max_concurrent ?? 0,
        header_override: input.header_override ?? "",
        system_prompt: input.system_prompt ?? "",
        retry_config: input.retry_config ?? "",
        stable_first: input.stable_first ?? false,
        site_id: siteId,
        credential_id: relayCredentialId,
      });
    },
    invalidateKeys: [...INVALIDATE, ["credentials"]],
    toastOnError: false,
    onSuccess: () => setEdit(null),
  });

	const setCredentialStatus = useAdminMutation({
		mutationFn: async (input: {
			id: number;
			status: "enabled" | "disabled";
		}) => {
			const list = credentials.data ?? [];
			const current = list.find((item) => item.id === input.id);
			return service.updateCredential(input.id, {
				kind: current?.kind || "api_key",
				status: input.status,
			});
		},
		invalidateKeys: [...INVALIDATE, ["credentials"]],
	});

	const updateKeyModels = useAdminMutation({
		mutationFn: async (input: { id: number; modelsCsv: string }) => {
			const list = credentials.data ?? [];
			const current = list.find((item) => item.id === input.id);
			return service.updateCredential(input.id, {
				kind: current?.kind || "api_key",
				models_csv: input.modelsCsv,
			});
		},
		invalidateKeys: [...INVALIDATE, ["credentials"]],
	});

  const addApiKeyCredential = useAdminMutation({
    mutationFn: async (input: { siteId: number; secret: string }) => {
      const secret = input.secret.trim();
      if (!secret) {
        throw new Error("api key is required");
      }
      // Site-level key pool: all enabled api_keys aggregate for relay/discovery failover.
      return service.createCredential(input.siteId, {
        kind: "api_key",
        secret,
        status: "enabled",
        meta_json: JSON.stringify({ name: "manual", group: "default" }),
      });
    },
    invalidateKeys: [...INVALIDATE, ["credentials"]],
  });

  const deleteApiKeyCredential = useAdminMutation({
    mutationFn: (credentialId: number) =>
      service.deleteCredential(credentialId),
    invalidateKeys: [...INVALIDATE, ["credentials"]],
  });

  const del = useAdminMutation({
    mutationFn: (id: number) => service.deleteChannel(id),
    invalidateKeys: [...INVALIDATE],
    pendingIdOf: (id) => id,
    toastOnError: false,
    onSuccess: (_data, id) => {
      setRemove(null);
      if (selectedId === id) {
        const next = new URLSearchParams(params);
        next.delete("id");
        setParams(next, { replace: true });
      }
      if (stageMessage?.channelId === id) setStageMessage(null);
    },
  });

  const setCheckin = useAdminMutation({
    mutationFn: (input: { credentialId: number; enabled: boolean }) =>
      service.setCheckin(input.credentialId, input.enabled),
    invalidateKeys: [["credentials"], ["channel-overviews"]],
    pendingIdOf: (input) => input.credentialId,
  });
  const runCheckin = useAdminMutation({
    mutationFn: (credentialId: number) => service.runCredential(credentialId),
    invalidateKeys: [["checkin-logs"], ["credentials"], ["channel-overviews"]],
    pendingIdOf: (credentialId) => credentialId,
  });

  const siteById = useMemo(() => {
    const map = new Map<number, Site>();
    for (const site of sites.data ?? []) map.set(site.id, site);
    return map;
  }, [sites.data]);

  const rows = useMemo(() => {
    const list = overviews.data ?? [];
    const term = query.trim().toLowerCase();
    return list.filter((overview) => {
      if (healthFilter === "ready" && !isChannelReady(overview)) {
        return false;
      }
      if (healthFilter === "missing_key" && !isMissingAPIKey(overview)) {
        return false;
      }
      if (healthFilter === "attention" && !channelNeedsAttention(overview)) {
        return false;
      }
      if (!term) return true;
      const ch = overview.channel;
      const site = ch.site_id != null ? siteById.get(ch.site_id) : undefined;
      const base = (ch.base_url || site?.base_url || "").toLowerCase();
      return (
        ch.name.toLowerCase().includes(term) ||
        base.includes(term) ||
        String(ch.id).includes(term)
      );
    });
  }, [overviews.data, query, siteById, healthFilter]);

  const pagination = useClientPagination(rows);
  const pageRows = pagination.pageItems;

  useEffect(() => {
    if (!rows.length) return;
    if (selectedId && rows.some((r) => r.channel.id === selectedId)) return;
    if (selectedId) return;
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("id", String(rows[0]!.channel.id));
        return next;
      },
      { replace: true },
    );
  }, [rows, selectedId, setParams]);

  const selected =
    rows.find((r) => r.channel.id === selectedId) ??
    (overviews.data ?? []).find((r) => r.channel.id === selectedId) ??
    null;

  const readyCount = (overviews.data ?? []).filter(
    isChannelReady,
  ).length;
  const missingKeyCount = (overviews.data ?? []).filter((o) =>
    isMissingAPIKey(o),
  ).length;
  const attentionCount = (overviews.data ?? []).filter((o) => {
    return channelNeedsAttention(o);
  }).length;

  const toggleHealthFilter = (next: ConnectionHealthFilter) => {
    setHealthFilter((current) => (current === next ? "all" : next));
  };

  const selectRow = (id: number) => {
    const next = new URLSearchParams(params);
    next.set("id", String(id));
    setParams(next, { replace: true });
  };

  /** User token for check-in on a site. Prefer the scheduled credential when overview says on. */
  const userCredentialFor = (overview?: ChannelOverview) => {
    const list = credentials.data ?? [];
    const siteId = overview?.channel.site_id;
    const onSite = list.filter((item) => {
      if (siteId != null && item.site_id !== siteId) return false;
      return (
        (item.kind === "access_token" || item.kind === "session") &&
        item.status === "enabled"
      );
    });
    if (!onSite.length) return undefined;
    // Match backend pickUserCredential: prefer the credential this channel is bound to,
    // so editing/deleting the token operates on the same credential that checks use.
    const boundId = overview?.channel.credential_id;
    if (boundId) {
      const bound = onSite.find((item) => item.id === boundId);
      if (bound) return bound;
    }
    // Match badge: when schedule is on, operate on a credential that is actually scheduled.
    if (overview?.checkin_enabled) {
      const scheduled = onSite.find((item) => item.checkin_enabled);
      if (scheduled) return scheduled;
    }
    // When schedule is off, prefer a token that is not scheduled yet (first off, else any).
    const notScheduled = onSite.find((item) => !item.checkin_enabled);
    return notScheduled ?? onSite[0];
  };
  const relayCredentialFor = (overview: ChannelOverview) => {
    const list = credentials.data ?? [];
    const id = overview.channel.credential_id;
    if (id) {
      const hit = list.find((item) => item.id === id);
      if (hit && hit.kind === "api_key") return hit;
    }
    return list.find(
      (item) => item.kind === "api_key" && item.status === "enabled",
    );
  };

  const connectionActions = (
    overview: ChannelOverview,
    options?: { closeContext?: boolean },
  ): ActionMenuItem[] => {
    const ch = overview.channel;
    const caps = capabilityFlags(overview);
    const busy =
      refresh.pendingId === ch.id ||
      probe.pendingId === ch.id ||
      accountProbe.pendingId === ch.id ||
      syncKeys.pendingId === ch.id ||
      toggle.pendingId === ch.id ||
      del.pendingId === ch.id;
    const close = () => {
      if (options?.closeContext) setContextMenu(null);
    };
    const items: ActionMenuItem[] = [
      {
        key: "edit",
        label: t("common.edit"),
        icon: <Pencil size={14} />,
        disabled: busy,
        onSelect: () => {
          close();
          saveEdit.reset();
          setEdit(ch);
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
          selectRow(ch.id);
          accountProbe.reset();
          accountProbe.mutate(ch.id, {
            onError: (err) => toast.pushError(err),
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
          selectRow(ch.id);
          syncKeys.reset();
          syncKeys.mutate(ch.id);
        },
      });
    }
    // Only offer key creation when the account token is known-good (last
    // probe succeeded for this channel) and the site has no key.
    // A dead/blocked token should never show a create button that can only fail.
    const canCreateKey =
      caps.accountSupported &&
      caps.hasUser &&
      caps.needsKeyForRelay &&
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
          selectRow(ch.id);
          createUpstreamKey.reset();
          setCreateKeyChannel(ch);
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
          selectRow(ch.id);
          probe.reset();
          probe.mutate(ch.id);
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
        duplicate.reset();
        duplicate.mutate(ch.id);
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
          selectRow(ch.id);
          refresh.reset();
          refresh.mutate(ch.id);
          // Model import doubles as an availability check; also refresh the
          // access-token status while we are at it.
          if (caps.accountSupported && caps.hasUser) {
            accountProbe.reset();
            accountProbe.mutate(ch.id);
          }
        },
      },
      {
        key: "toggle",
        label:
          ch.status === "enabled"
            ? t("common.disableAction")
            : t("common.enableAction"),
        icon: <Power size={14} />,
        disabled: busy,
        onSelect: () => {
          close();
          toggle.mutate(overview);
        },
      },
      {
        key: "models",
        label: t("channels.openModels"),
        icon: <ExternalLink size={14} />,
        onSelect: () => {
          close();
          navigate(`/models?channel_id=${ch.id}`);
        },
      },
      {
        key: "logs",
        label: t("channels.openLogs"),
        icon: <ExternalLink size={14} />,
        onSelect: () => {
          close();
          navigate(`/logs?channel_id=${ch.id}`);
        },
      },
      ...(() => {
        if (!checkinEnabled) return [];
        // Label must follow overview badge (site-level schedule), not an arbitrary first token.
        const scheduleOn = Boolean(overview.checkin_enabled);
        const checkinCred = userCredentialFor(overview);
        if (!checkinCred && !caps.hasUser) return [];
        // Has user token on overview but credentials list not loaded yet: still show correct label.
        const canToggle = Boolean(checkinCred);
        return [
          {
            key: "checkin-toggle",
            label: scheduleOn
              ? t("channels.checkinDisable")
              : t("channels.checkinEnable"),
            icon: <CalendarCheck size={14} />,
            disabled:
              busy ||
              !canToggle ||
              (checkinCred != null && setCheckin.pendingId === checkinCred.id),
            onSelect: () => {
              close();
              if (!checkinCred) return;
              setCheckin.mutate({
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
              (checkinCred != null && runCheckin.pendingId === checkinCred.id),
            onSelect: () => {
              close();
              if (!checkinCred) return;
              runCheckin.mutate(checkinCred.id);
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
          setRemove(ch);
        },
      },
    );
    return items;
  };

  const openAdd = () => {
    createConnection.reset();
    verifyAfterCreate.current = false;
    setAddOpen(true);
  };

  const submitCreate = (
    value: CreateConnectionInput,
    options: { verify: boolean },
  ) => {
    verifyAfterCreate.current = options.verify;
    createConnection.mutate(value);
  };

  const retryVerify = (channelId: number) => {
    const name = stageMessage?.name ?? `#${channelId}`;
    runVerifyRef.current(channelId, name);
  };

  return (
    <Page
      kicker={t("channels.kicker")}
      title={t("channels.title")}
      description={t("channels.description")}
      actions={
        <>
          <label className="directory-search">
            <Search size={14} aria-hidden="true" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("channels.searchPlaceholder")}
              aria-label={t("channels.searchPlaceholder")}
            />
          </label>
          <Button
            variant="secondary"
            icon={
              <RefreshCw
                size={16}
                className={refreshAll.isPending ? "spin" : ""}
              />
            }
            disabled={refreshAll.isPending || !rows.length}
            onClick={() => {
              refreshAll.reset();
              refreshAll.mutate();
            }}
          >
            {t("channels.refreshAll")}
          </Button>
          <Button
            variant="secondary"
            icon={
              <UserCheck
                size={16}
                className={checkAllTokens.isPending ? "spin" : ""}
              />
            }
            disabled={checkAllTokens.isPending || !rows.length}
            onClick={() => {
              checkAllTokens.reset();
              checkAllTokens.mutate();
            }}
          >
            {t("channels.checkAllTokens")}
          </Button>
          <Button icon={<Plus size={16} />} onClick={openAdd}>
            {t("channels.add")}
          </Button>
        </>
      }
    >
      <div className="ops-canvas">
        <StatGrid
          columns={4}
          items={[
            {
              label: t("channels.stat.total"),
              value: overviews.data?.length ?? "—",
              onClick: () => setHealthFilter("all"),
              active: healthFilter === "all",
              hint: t("channels.stat.totalHint"),
            },
            {
              label: t("channels.stat.ready"),
              value: overviews.isPending ? "—" : readyCount,
              onClick: () => toggleHealthFilter("ready"),
              active: healthFilter === "ready",
              hint: t("channels.stat.readyHint"),
            },
            {
              label: t("channels.stat.missingKey"),
              value: overviews.isPending ? "—" : missingKeyCount,
              onClick: () => toggleHealthFilter("missing_key"),
              active: healthFilter === "missing_key",
              hint: t("channels.stat.missingKeyHint"),
            },
            {
              label: t("channels.stat.attention"),
              value: overviews.isPending ? "—" : attentionCount,
              onClick: () => toggleHealthFilter("attention"),
              active: healthFilter === "attention",
              hint: t("channels.stat.attentionHint"),
            },
          ]}
        />

        {stageMessage?.kind === "created" ? (
          <ResultStrip status="info">
            {t("channels.createdOnly", { name: stageMessage.name })}
          </ResultStrip>
        ) : null}
        {stageMessage?.kind === "created_and_verified" ? (
          <ResultStrip status="success">
            {t("channels.createdAndSynced", {
              name: stageMessage.name,
              models: stageMessage.models ?? 0,
            })}
          </ResultStrip>
        ) : null}
        {stageMessage?.kind === "verify_failed" ? (
          <ResultStrip status="error">
            <span>
              {t("channels.verifyFailed", { name: stageMessage.name })}
            </span>
            <Button
              variant="secondary"
              disabled={refresh.isPending}
              onClick={() => retryVerify(stageMessage.channelId)}
            >
              {t("channels.retryVerify")}
            </Button>
          </ResultStrip>
        ) : null}
        {refreshAll.data ? (
          <ResultStrip
            status={refreshAll.data.failure_count > 0 ? "error" : "success"}
          >
            {t("ops.refreshSummary", {
              success: refreshAll.data.success_count,
              failure: refreshAll.data.failure_count,
            })}
          </ResultStrip>
        ) : null}
        {checkAllTokens.data ? (
          <ResultStrip
            status={
              checkAllTokens.data.items.some((item) => !item.ok)
                ? "error"
                : "success"
            }
          >
            {t("channels.checkAllTokensSummary", {
              success: checkAllTokens.data.items.filter((item) => item.ok)
                .length,
              failure: checkAllTokens.data.items.filter((item) => !item.ok)
                .length,
            })}
          </ResultStrip>
        ) : null}
        {refresh.data && stageMessage?.kind !== "created_and_verified" ? (
          <ResultStrip status="success">
            {t("channels.refreshResult", {
              id: refresh.data.channel_id,
              models: refresh.data.models.length,
            })}
          </ResultStrip>
        ) : null}
        {probe.data ? (
          <ResultStrip status="success">
            {t("channels.probeResult", {
              id: probe.data.channel_id,
              models: probe.data.models.length,
              latency: probe.data.latency_ms,
            })}
          </ResultStrip>
        ) : null}
        {accountProbe.data ? (
          <ResultStrip status="success">
            {t("channels.accountProbeResult", {
              user: accountProbe.data.username,
              latency: accountProbe.data.latency_ms,
            })}
          </ResultStrip>
        ) : null}
        {syncKeys.data ? (
          <ResultStrip
            status={
              syncKeys.data.created_credentials +
                syncKeys.data.reused_credentials >
              0
                ? "success"
                : syncKeys.data.skipped_masked > 0 || syncKeys.data.empty_list
                  ? "error"
                  : "info"
            }
          >
            <span>
              {t("channels.syncKeysResult", {
                created: syncKeys.data.created_credentials,
                reused: syncKeys.data.reused_credentials,
                masked: syncKeys.data.skipped_masked,
                deleted: syncKeys.data.deleted_credentials ?? 0,
              })}
              {(syncKeys.data.created_channels ||
                syncKeys.data.updated_channels) &&
                ` ${t("channels.syncKeysGroups", {
                  created: syncKeys.data.created_channels ?? 0,
                  updated: syncKeys.data.updated_channels ?? 0,
                })}`}
              {syncKeys.data.message
                ? ` — ${formatErrorMessage(syncKeys.data.message, t)}`
                : syncKeys.data.empty_list
                  ? ` — ${t("channels.syncKeysEmpty")}`
                  : syncKeys.data.skipped_masked > 0 &&
                      syncKeys.data.created_credentials +
                        syncKeys.data.reused_credentials ===
                        0
                    ? ` — ${t("channels.syncKeysMasked")}`
                    : ""}
            </span>
          </ResultStrip>
        ) : null}

        <div className="split">
          <Panel className="ops-list-panel">
            <EntityState
              isLoading={overviews.isPending}
              isError={overviews.isError}
              error={overviews.error}
              isEmpty={!rows.length}
              empty={
                <EmptyHero
                  kicker={
                    healthFilter === "missing_key"
                      ? t("channels.filter.missingKeyKicker")
                      : t("channels.emptyKicker")
                  }
                  title={
                    healthFilter === "missing_key"
                      ? t("channels.filter.missingKeyTitle")
                      : healthFilter === "attention"
                        ? t("channels.filter.attentionTitle")
                        : healthFilter === "ready"
                          ? t("channels.filter.readyTitle")
                          : t("channels.emptyTitle")
                  }
                  body={
                    healthFilter === "all"
                      ? t("channels.empty")
                      : t("channels.filter.clearHint")
                  }
                  actions={
                    healthFilter === "all" ? (
                      <Button icon={<Plus size={16} />} onClick={openAdd}>
                        {t("channels.add")}
                      </Button>
                    ) : (
                      <Button
                        variant="secondary"
                        onClick={() => setHealthFilter("all")}
                      >
                        {t("common.clearFilters")}
                      </Button>
                    )
                  }
                />
              }
              retry={() => overviews.refetch()}
            >
              <ListShell
                footer={
                  <PaginationBar
                    page={pagination.page}
                    totalPages={pagination.totalPages}
                    total={pagination.total}
                    pageSize={pagination.pageSize}
                    rangeStart={pagination.rangeStart}
                    rangeEnd={pagination.rangeEnd}
                    hasPrev={pagination.hasPrev}
                    hasNext={pagination.hasNext}
                    onPageChange={pagination.setPage}
                    onPageSizeChange={pagination.setPageSize}
                  />
                }
              >
                <DataTable
                  headers={[
                    t("common.name"),
                    t("common.status"),
                    t("common.models"),
                    t("common.latency"),
                    t("common.actions"),
                  ]}
                >
                  {pageRows.map((overview) => {
                    const ch = overview.channel;
                    const site =
                      ch.site_id != null ? siteById.get(ch.site_id) : undefined;
                    const displayBase = ch.base_url || site?.base_url || "";
                    const caps = capabilityFlags(overview);
                    const active = selected?.channel.id === ch.id;
                    const rowBusy =
                      refresh.pendingId === ch.id ||
                      probe.pendingId === ch.id ||
                      accountProbe.pendingId === ch.id ||
                      syncKeys.pendingId === ch.id ||
                      toggle.pendingId === ch.id ||
                      del.pendingId === ch.id;
                    return (
                      <tr
                        key={ch.id}
                        className={`is-clickable${active ? " is-selected" : ""}`}
                        onClick={() => selectRow(ch.id)}
                        onContextMenu={(event) => {
                          event.preventDefault();
                          selectRow(ch.id);
                          setContextMenu({
                            channelId: ch.id,
                            top: event.clientY,
                            left: event.clientX,
                          });
                        }}
                      >
                        <td>
                          <strong>{ch.name}</strong>
                          {displayBase ? (
                            <a
                              className="mono truncate base-url-link"
                              href={displayBase}
                              target="_blank"
                              rel="noopener noreferrer"
                              title={displayBase}
                              onClick={(event) => event.stopPropagation()}
                            >
                              {displayBase}
                            </a>
                          ) : (
                            <small className="mono truncate">
                              {t("channels.inheritsSite")}
                            </small>
                          )}
                        </td>
                        <td className="status-col">
                          <div className="capability-stack is-compact">
                            <ChannelStatusBadges overview={overview} />
                            {caps.tokenProblem ? (
                              <span className="capability-chip is-warn">
                                {t("channels.badge.tokenProblem")}
                              </span>
                            ) : null}
                            {caps.checkinScheduled ? (
                              <span className="capability-chip is-checkin">
                                {t("channels.badge.checkinOn")}
                              </span>
                            ) : caps.checkinNeedsUserID ? (
                              <span className="capability-chip is-warn">
                                {t("channels.badge.needsUserId")}
                              </span>
                            ) : null}
                            {caps.modelsReady ? (
                              <span className="capability-chip is-models">
                                {t("channels.badge.models")}
                              </span>
                            ) : null}
                          </div>
                        </td>
                        <td>
                          <strong>{overview.model_count}</strong>
                        </td>
                        <td>
                          {overview.last_checked_at
                            ? t("common.ms", { n: overview.last_latency_ms })
                            : "—"}
                        </td>
                        <td
                          className="actions row-actions"
                          onClick={(event) => event.stopPropagation()}
                        >
                          <ActionMenu
                            compact
                            label={t("common.moreActions")}
                            disabled={rowBusy}
                            onOpenChange={(open) => {
                              // Ensure credentials for this row's site are loaded so check-in
                              // toggle label matches the overview badge.
                              if (open) selectRow(ch.id);
                            }}
                            items={connectionActions(overview)}
                          />
                        </td>
                      </tr>
                    );
                  })}
                </DataTable>
              </ListShell>
              {contextMenu
                ? (() => {
                    const overview =
                      rows.find(
                        (row) => row.channel.id === contextMenu.channelId,
                      ) ??
                      (overviews.data ?? []).find(
                        (row) => row.channel.id === contextMenu.channelId,
                      );
                    if (!overview) return null;
                    return (
                      <ActionMenu
                        label={t("common.moreActions")}
                        open
                        onOpenChange={(open) => {
                          if (!open) setContextMenu(null);
                        }}
                        position={{
                          top: contextMenu.top,
                          left: contextMenu.left,
                        }}
                        items={connectionActions(overview, {
                          closeContext: true,
                        })}
                      />
                    );
                  })()
                : null}
            </EntityState>
          </Panel>

          <div className="detail-card ops-detail-card is-compact">
            {!selected ? (
              <div className="detail-empty">{t("channels.selectHint")}</div>
            ) : (
              <ChannelDetail
                overview={selected}
                site={
                  selected.channel.site_id != null
                    ? siteById.get(selected.channel.site_id)
                    : undefined
                }
                accountData={
                  accountProbe.data?.channel_id === selected.channel.id
                    ? accountProbe.data
                    : null
                }
                busy={
                  refresh.pendingId === selected.channel.id ||
                  probe.pendingId === selected.channel.id ||
                  accountProbe.pendingId === selected.channel.id ||
                  syncKeys.pendingId === selected.channel.id ||
                  toggle.pendingId === selected.channel.id ||
                  del.pendingId === selected.channel.id ||
                  ping.pendingId === selected.channel.id
                }
                onCheckAccount={() => {
                  accountProbe.reset();
                  accountProbe.mutate(selected.channel.id);
                }}
                onPing={() => {
                  ping.reset();
                  ping.mutate(selected.channel.id);
                }}
                pingPending={ping.pendingId === selected.channel.id}
                pingResult={
                  ping.data?.channel_id === selected.channel.id
                    ? ping.data
                    : null
                }
                onRefresh={() => {
                  refresh.reset();
                  refresh.mutate(selected.channel.id);
                }}
                onEdit={() => {
                  saveEdit.reset();
                  setEdit(selected.channel);
                }}
              />
            )}
          </div>
        </div>
      </div>

      {addOpen ? (
        <AddChannelDialog
          pending={createConnection.isPending}
          error={createConnection.error}
          onClose={() => {
            if (createConnection.isPending) return;
            setAddOpen(false);
          }}
          onSave={(value, options) => submitCreate(value, options)}
        />
      ) : null}
      {edit ? (
        <EditChannelDialog
          value={edit}
          routeOverviews={routeOverviewsQuery.data}
          site={edit.site_id != null ? siteById.get(edit.site_id) : undefined}
          credentials={credentials.data ?? []}
          credential={(() => {
            const overview =
              (overviews.data ?? []).find(
                (row) => row.channel.id === edit.id,
              ) ?? null;
            return overview ? relayCredentialFor(overview) : undefined;
          })()}
          userCredential={(() => {
            const overview =
              (overviews.data ?? []).find(
                (row) => row.channel.id === edit.id,
              ) ?? null;
            return overview ? userCredentialFor(overview) : undefined;
          })()}
          pending={
            saveEdit.isPending ||
            setCredentialStatus.isPending ||
            addApiKeyCredential.isPending ||
            deleteApiKeyCredential.isPending
          }
          error={
            saveEdit.error ??
            setCredentialStatus.error ??
            addApiKeyCredential.error ??
            deleteApiKeyCredential.error
          }
          onClose={() => {
            setEdit(null);
            setModelsChannel(null);
            setKeysChannel(null);
          }}
          onSave={(value) => saveEdit.mutate(value)}
          onManageModels={() => {
            setKeysChannel(null);
            setModelsChannel(edit);
          }}
          onManageKeys={() => {
            setModelsChannel(null);
            setKeysChannel(edit);
          }}
        />
      ) : null}
      {createKeyChannel ? (
        <CreateKeyDialog
          channelName={createKeyChannel.name}
          channelId={createKeyChannel.id}
          pending={createUpstreamKey.isPending}
          error={createUpstreamKey.error}
          onClose={() => {
            if (createUpstreamKey.isPending) return;
            createUpstreamKey.reset();
            setCreateKeyChannel(null);
          }}
          onCreate={(group) => {
            // Synchronous re-entry guard: the disabled={pending} button only
            // takes effect after re-render, so rapid double-clicks could
            // otherwise create several upstream tokens.
            if (createKeyLocked.current) return;
            createKeyLocked.current = true;
            const input = {
              id: createKeyChannel.id,
              name: `gateway-${group || "default"}`,
              group,
            };
            createUpstreamKey.mutate(input, {
              onSuccess: () => {
                // Close immediately so the operator cannot click again;
                // the toast is the success signal.
                createUpstreamKey.reset();
                setCreateKeyChannel(null);
                toast.push({
                  tone: "success",
                  message: t("channels.createKeySuccess", {
                    name: createKeyChannel.name,
                  }),
                });
              },
              onSettled: () => {
                createKeyLocked.current = false;
              },
            });
          }}
          // If the upstream masks the fresh key, offer a one-click sync
          // import inside the dialog instead of forcing a manual paste.
          syncPending={syncKeys.isPending}
          onSync={() => {
            createUpstreamKey.reset();
            syncKeys.reset();
            syncKeys.mutate(createKeyChannel.id);
          }}
        />
      ) : null}
      {modelsChannel ? (
        <Drawer
          title={t("channels.modelsSection")}
          width={780}
          onClose={() => setModelsChannel(null)}
          footer={
            <Button variant="secondary" onClick={() => setModelsChannel(null)}>
              {t("common.close")}
            </Button>
          }
        >
          <ChannelModelsPanel
            channelId={modelsChannel.id}
            header={
              <div className="channel-models-panel-head">
                <div>
                  <p className="page-kicker">{modelsChannel.name}</p>
                  <p className="detail-section-empty is-quiet">
                    {t("channels.modelsManageHint")}
                  </p>
                </div>
              </div>
            }
          />
        </Drawer>
      ) : null}
      {keysChannel ? (
        <ChannelKeysDrawer
          channel={keysChannel}
          apiKeys={(credentials.data ?? []).filter(
            (item) => item.kind === "api_key",
          )}
          pending={
            setCredentialStatus.isPending ||
            deleteApiKeyCredential.isPending
          }
          addApiKeyPending={addApiKeyCredential.isPending}
          syncKeysPending={syncKeys.isPending}
          onToggleKey={(id, enabled) =>
            setCredentialStatus.mutate({
              id,
              status: enabled ? "enabled" : "disabled",
            })
          }
          onUpdateKeyModels={(id, modelsCsv) =>
            updateKeyModels.mutate({ id, modelsCsv })
          }
          onDeleteKey={(id) => deleteApiKeyCredential.mutate(id)}
          onAddApiKey={(secret) => {
            const siteId = keysChannel.site_id;
            if (!siteId) return;
            addApiKeyCredential.mutate({ siteId, secret });
          }}
          onSyncKeys={() => {
            syncKeys.reset();
            syncKeys.mutate(keysChannel.id);
          }}
          onClose={() => setKeysChannel(null)}
        />
      ) : null}
      {remove ? (
        <ConfirmDialog
          title={t("channels.deleteTitle")}
          message={t("channels.deleteMsg", { name: remove.name })}
          pending={del.isPending}
          error={del.error}
          onClose={() => setRemove(null)}
          onConfirm={() => del.mutate(remove.id)}
        />
      ) : null}
    </Page>
  );
}
export { capabilityFlags } from "./channels/helpers"
