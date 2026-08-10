import {
  Activity,
  Cable,
  ChevronDown,
  ExternalLink,
  KeyRound,
  Pencil,
  Play,
  Plus,
  Power,
  RefreshCw,
  Search,
  CalendarCheck,
  Trash2,
  UserCheck,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
  AccountProbeResult,
  Channel,
  ChannelPingResult,
  ChannelOverview,
  RouteOverview,
  Site,
} from "../api/types";
import { ChannelModelsPanel } from "./ChannelModels";
import { ActionMenu, type ActionMenuItem } from "../components/ActionMenu";
import { Drawer } from "../components/Drawer";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import {
  SearchableSelect,
  type SelectOption,
} from "../components/SearchableSelect";
import { PaginationBar } from "../components/PaginationBar";
import { EntityState } from "../components/EntityState";
import { ResultStrip } from "../components/ResultStrip";
import { StatGrid } from "../components/StatGrid";
import {
  Button,
  ConfirmDialog,
  DataTable,
  Dialog,
  ErrorState,
  Field,
  Page,
  Panel,
  InfoTip,
  StatusBadge,
  formatDate,
} from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useToast } from "../toast";
import { formatErrorMessage } from "../formatError";
import {
  CONNECTION_TYPE_OPTIONS,
  PROVIDER_BASE_URLS,
} from "../connectionTypes";
import { useSession } from "../session";
import { useModules } from "../hooks/useModules";
import {
  channelConnectivityState,
  channelHealthState,
  channelNeedsAttention,
  channelReadiness,
  isChannelReady,
} from "./channelHealth";

export {
  channelHealthState,
  channelConnectivityState,
  channelNeedsAttention,
  channelReadiness,
  isChannelReady,
} from "./channelHealth";

const INVALIDATE = [
  ["channel-overviews"],
  ["channels"],
  ["sites"],
  ["models"],
  ["routes"],
  ["route-overviews"],
  ["discovered-models"],
] as const;

const TYPE_OPTIONS: SelectOption[] = [
  ...CONNECTION_TYPE_OPTIONS.map((option) => ({
    value: option.value,
    label: option.label,
    group: option.group,
  })),
  { value: "__custom__", label: "Custom…", group: "other" },
];

const TYPE_GROUPS = ["core", "relay", "other"];

/** Value shown in secret inputs when a credential is stored; keeping it means "don't change". */
const SECRET_MASK = "••••••••••";

type ConnectionHealthFilter = "all" | "ready" | "attention" | "missing_key";

function isMissingAPIKey(overview: ChannelOverview) {
  return !overview.has_api_key;
}

type CreateConnectionInput = {
  name: string;
  base_url: string;
  secret: string;
  type_hint: string;
};

type CreateConnectionResult = {
  channel: Channel;
  reusedSite: boolean;
  /** True when the supplied secret looks like a New API access token (not sk-). */
  looksLikeAccessToken: boolean;
};

function hostLabel(url: string) {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}

function normalizeBase(url: string) {
  return url.trim().replace(/\/+$/, "");
}

function positiveId(value: string | null) {
  if (!value) return undefined;
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
}

function parseCredentialMeta(metaJSON?: string): {
  name?: string;
  group?: string;
  upstream_token_id?: number;
} {
  if (!metaJSON?.trim()) return {};
  try {
    const parsed = JSON.parse(metaJSON) as Record<string, unknown>;
    const name = typeof parsed.name === "string" ? parsed.name : undefined;
    const group =
      typeof parsed.group === "string"
        ? parsed.group
        : typeof parsed.Group === "string"
          ? parsed.Group
          : undefined;
    const upstream =
      typeof parsed.upstream_token_id === "number"
        ? parsed.upstream_token_id
        : undefined;
    return { name, group, upstream_token_id: upstream };
  } catch {
    return {};
  }
}

function needsVerify(overview: ChannelOverview) {
  return channelReadiness(overview) === "unverified" || overview.model_count === 0;
}

export function capabilityFlags(overview: ChannelOverview) {
  const hasUser = Boolean(overview.has_user_credential);
  const hasPlatformUserID = Boolean(overview.has_platform_user_id);
  const hasAPIKey = Boolean(overview.has_api_key);
  const modelsReady = overview.model_count > 0;
  const checkinScheduled = Boolean(overview.checkin_enabled);
  // Site-family capabilities (AAH-derived profile): sub2api/aihubmix/
  // sharedchat do not support check-in; unsupported families have no
  // account APIs at all.
  const checkinSupported = Boolean(overview.checkin_supported);
  const accountSupported = Boolean(overview.account_supported);
  // New-API family check-in needs user token + numeric user id (may be filled on first run).
  const checkinReady = hasUser && hasPlatformUserID && checkinSupported;
  const checkinNeedsUserID = hasUser && !hasPlatformUserID && checkinSupported;
  return {
    hasUser,
    hasPlatformUserID,
    hasAPIKey,
    checkinSupported,
    accountSupported,
    /** Token + user id present; manual check-in is fully prepared. */
    checkinCapable: checkinReady,
    checkinReady,
    checkinNeedsUserID,
    /** Scheduled check-in is enabled on a user credential. */
    checkinScheduled: checkinScheduled && checkinReady,
    /** User token exists but schedule is off. */
    checkinOff: checkinReady && !checkinScheduled,
    /** No user token at all — cannot check in. */
    noUserToken: !hasUser,
    missingAPIKey: !hasAPIKey,
    /** An access token is stored, was checked, and that check failed — the token itself is the problem. */
    tokenProblem:
      hasUser &&
      Boolean(overview.last_probe_at) &&
      overview.last_probe_ok === false,
    modelsReady,
    needsKeyForRelay: !hasAPIKey,
  };
}

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

  // Entering the Connections page records a network-layer Ping for every
  // enabled connection. This deliberately does not run the authenticated
  // model probe; model/auth verification remains an explicit action.
  const autoPinged = useRef(false);
  const pingAll = useAdminMutation({
    mutationFn: async (ids: number[]) => {
      // Small serial batches: 8 probes per wave, all failures swallowed so a
      // flaky site does not toast on page entry.
      for (let index = 0; index < ids.length; index += 8) {
        await Promise.allSettled(
          ids.slice(index, index + 8).map((id) => service.pingChannel(id)),
        );
      }
    },
    invalidateKeys: [...INVALIDATE],
  });
  useEffect(() => {
    if (autoPinged.current) return;
    const ids = (overviews.data ?? [])
      .filter((row) => row.channel.status === "enabled")
      .map((row) => row.channel.id);
    if (!ids.length) return;
    autoPinged.current = true;
    pingAll.mutate(ids);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [overviews.data]);

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
  const createUpstreamKey = useAdminMutation({
    mutationFn: (input: { id: number; name?: string; group?: string }) =>
      service.createUpstreamKey(input.id, {
        name: input.name,
        group: input.group,
        unlimited_quota: true,
      }),
    invalidateKeys: [...INVALIDATE, ["credentials"]],
    pendingIdOf: (input) => input.id,
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
        header_override: input.header_override ?? "",
        system_prompt: input.system_prompt ?? "",
  retry_config: input.retry_config ?? "",
        stable_first: input.stable_first ?? false,
        site_id: siteId,
        credential_id: relayCredentialId,
      });
    },
    invalidateKeys: [...INVALIDATE, ["credentials"]],
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
          accountProbe.mutate(ch.id);
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
          }}
          onSave={(value) => saveEdit.mutate(value)}
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
            const siteId = edit.site_id;
            if (!siteId) return;
            addApiKeyCredential.mutate({ siteId, secret });
          }}
          addApiKeyPending={addApiKeyCredential.isPending}
          onSyncKeys={() => {
            syncKeys.reset();
            syncKeys.mutate(edit.id);
          }}
          syncKeysPending={syncKeys.isPending}
          onManageModels={() => {
            setModelsChannel(edit);
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
          rightOffset={520}
          plain
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

function channelHealthReasonLabel(
  overview: ChannelOverview,
  t: (key: string, vars?: Record<string, string | number>) => string,
) {
  switch (overview.health_reason) {
    case "manual_disabled":
      return t("channels.healthReason.manualDisabled");
    case "auto_disabled":
      return t("channels.healthReason.autoDisabled");
    case "not_checked":
      return t("channels.healthReason.notChecked");
    case "authentication_failed":
      return t("channels.healthReason.authenticationFailed");
    case "credential_scope":
      return t("channels.healthReason.credentialScope");
    case "credential_unavailable":
      return t("channels.healthReason.credentialUnavailable");
    case "invalid_base_url":
      return t("channels.healthReason.invalidBaseUrl");
    case "probe_failed":
      return t("channels.healthReason.probeFailed");
    case "probe_slow":
      return t("channels.healthReason.probeSlow");
    case "route_cooling":
      return t("channels.healthReason.routeCooling", {
        count: overview.cooling_member_count,
      });
    case "route_failures":
      return t("channels.healthReason.routeFailures", {
        count: overview.failure_count,
      });
    case "probe_ok":
      return t("channels.healthReason.probeOk");
    default:
      return t("channels.healthReason.unknown");
  }
}

function ChannelHealthBadge({ overview }: { overview: ChannelOverview }) {
  const { t } = useI18n();
  const state = channelHealthState(overview);
  const reason = channelHealthReasonLabel(overview, t);
  return (
    <span
      className={`badge badge-${state}`}
      title={reason}
      data-testid="channel-health-badge"
    >
      {t(`channels.healthState.${state}`)}
    </span>
  );
}

/**
 * Health + readiness as one badge. When a connection merely lacks an API key
 * the two stacked badges read as noise on every row, so they merge into a
 * single "state · needs key" badge; the tooltip keeps the health reason.
 */
function ChannelStatusBadges({ overview }: { overview: ChannelOverview }) {
  const { t } = useI18n();
  const readiness = channelReadiness(overview);
  if (readiness === "missing_key") {
    const health = channelHealthState(overview);
    return (
      <span
        className="badge badge-missing-key"
        title={channelHealthReasonLabel(overview, t)}
      >
        {t(`channels.healthState.${health}`)} · {t("channels.badge.missingKey")}
      </span>
    );
  }
  return (
    <>
      <ChannelHealthBadge overview={overview} />
      <ChannelReadinessBadge overview={overview} />
    </>
  );
}

function ChannelReadinessBadge({ overview }: { overview: ChannelOverview }) {
  const readiness = channelReadiness(overview);
  if (
    readiness !== "auto_disabled" &&
    readiness !== "blocked" &&
    readiness !== "missing_key" &&
    readiness !== "token_invalid"
  ) {
    return null;
  }
  return <StatusBadge value={readiness} />;
}

function ChannelConnectivityBadge({
  overview,
  live,
}: {
  overview: ChannelOverview;
  live?: ChannelPingResult | null;
}) {
  const { t, status } = useI18n();
  const state = channelConnectivityState(overview, live);
  const latency = live?.latency_ms ?? overview.last_ping_ms;
  const error = live ? live.error : overview.last_ping_error;
  const detail =
    state === "reachable"
      ? latency != null && latency > 0
        ? t("channels.pingOkShort", { ms: latency })
        : ""
      : state === "unreachable" && error
        ? status(error)
        : "";
  const checkedAt = live?.checked_at ?? overview.last_ping_at;
  const stateLabel =
    state === "reachable"
      ? t("channels.reachable")
      : state === "unreachable"
        ? t("channels.unreachable")
        : t("channels.connectivityUnknown");
  // The error label often repeats the state label ("不可达 · 不可达");
  // only append detail when it adds information.
  const suffix = detail && detail !== stateLabel ? ` · ${detail}` : "";
  return (
    <span
      className={`health-chip is-${state}`}
      title={checkedAt ? formatDate(checkedAt) : undefined}
      data-testid="channel-connectivity-badge"
    >
      {stateLabel}
      {suffix}
    </span>
  );
}

function ChannelDetail({
  overview,
  site,
  busy,
  accountData,
  onCheckAccount,
  onPing,
  pingPending,
  pingResult,
  onRefresh,
  onEdit,
}: {
  overview: ChannelOverview;
  site?: Site;
  busy: boolean;
  accountData: AccountProbeResult | null;
  onCheckAccount: () => void;
  onPing: () => void;
  pingPending: boolean;
  pingResult: ChannelPingResult | null;
  onRefresh: () => void;
  onEdit: () => void;
}) {
  const { t, status } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const ch = overview.channel;
  const displayBase = ch.base_url || site?.base_url || "";
  const caps = capabilityFlags(overview);
  const probeError =
    overview.last_probe_error === "probe_slow"
      ? ""
      : overview.last_probe_error;
  const finance = useQuery({
    queryKey: ["finance"],
    queryFn: ({ signal }) => service.finance(signal),
    retry: false,
    staleTime: 120_000,
  });
  const financeItem = (finance.data?.items ?? []).find(
    (item) => item.channel_id === ch.id,
  );
  const quotaPerUnit =
    financeItem?.quota_per_unit && financeItem.quota_per_unit > 0
      ? financeItem.quota_per_unit
      : 500000;
  const balance =
    accountData?.quota != null && accountData.used_quota != null
      ? accountData.quota - accountData.used_quota
      : null;
  const formatCurrency = (value: number) =>
    (value / quotaPerUnit).toLocaleString("en-US", {
      style: "currency",
      currency: "USD",
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    });

  return (
    <>
      <div className="detail-head">
        <div className="detail-title-block">
          <p className="detail-kicker">{t("channels.detailKicker")}</p>
          <h2>{ch.name}</h2>
          <p className="detail-subtitle mono" title={displayBase}>
            <span>#{ch.id}</span>
            {displayBase ? <span className="detail-dot">·</span> : null}
            {displayBase ? (
              <a
                className="truncate base-url-link"
                href={displayBase}
                target="_blank"
                rel="noopener noreferrer"
              >
                {displayBase}
              </a>
            ) : null}
          </p>
        </div>
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
          ) : caps.hasUser ? (
            <span className="capability-chip is-muted">
              {t("channels.badge.checkinOff")}
            </span>
          ) : null}
          {caps.hasAPIKey ? (
            <span className="capability-chip is-key">
              {t("channels.badge.hasKey")}
            </span>
          ) : null}
          {caps.modelsReady ? (
            <span className="capability-chip is-models">
              {t("channels.badge.models")}
            </span>
          ) : null}
        </div>
      </div>

      <div className="detail-meta is-compact">
        <div>
          <span className="label">{t("common.type")}</span>
          <span>{ch.type_hint || site?.platform || "—"}</span>
        </div>
        <div>
          <span className="label">{t("channels.defaultRouting")}</span>
          <span title={t("channels.priorityHint")}>
            {t("channels.defaultPriorityWeight", {
              priority: ch.priority,
              weight: ch.weight,
            })}
          </span>
        </div>
        <div>
          <span className="label">{t("common.models")}</span>
          <span>{overview.model_count}</span>
        </div>
        {balance != null ? (
          <div>
            <span className="label">{t("channels.balance")}</span>
            <span title={t("channels.balanceHint")}>
              {formatCurrency(balance)}
            </span>
          </div>
        ) : null}
        <div>
          <span className="label">{t("common.checked")}</span>
          <span>
            {overview.last_checked_at
              ? formatDate(overview.last_checked_at)
              : t("channels.neverChecked")}
          </span>
        </div>
        <div>
          <span className="label">{t("channels.healthState")}</span>
          <span className="detail-health-value">
            <ChannelHealthBadge overview={overview} />
            <small>{channelHealthReasonLabel(overview, t)}</small>
          </span>
        </div>
        <div>
          <span className="label">{t("channels.reachability")}</span>
          <ChannelConnectivityBadge overview={overview} live={pingResult} />
        </div>
      {probeError || overview.last_error ? (
        <div className="detail-meta-error">
          <span className="label">{t("common.error")}</span>
          <span
            className="truncate"
            title={probeError || overview.last_error}
          >
            {status(probeError ?? overview.last_error ?? "")}
          </span>
        </div>
      ) : null}
      </div>

      {(() => {
        const models = (ch.models_csv || "")
          .split(",")
          .map((name) => name.trim())
          .filter(Boolean);
        if (!models.length) return null;
        const shown = models.slice(0, 50);
        return (
          <div className="detail-pricing">
            <span className="label">{t("channels.modelsTitle")}</span>
            <div className="detail-pricing-list">
              {shown.map((name) => (
                <div key={name} className="detail-pricing-row">
                  <code>{name}</code>
                </div>
              ))}
            </div>
            {models.length > 50 ? (
              <div className="is-quiet detail-pricing-more">
                {t("channels.modelsMore", {
                  count: models.length - 50,
                })}
              </div>
            ) : null}
          </div>
        );
      })()}

      <div className="detail-primary-bar is-compact">
        <Button
          icon={
            <Activity
              size={14}
              className={pingPending ? "spin" : ""}
            />
          }
          disabled={busy}
          onClick={onPing}
        >
          {pingPending
            ? t("channels.pinging")
            : pingResult
              ? pingResult.reachable
                ? t("channels.pingOk", {
                    ms: pingResult.latency_ms ?? "",
                  })
                : t("channels.pingFail", { error: pingResult.error ?? "" })
              : t("channels.ping")}
        </Button>
        {caps.hasUser ? (
          <Button
            variant="secondary"
            icon={<UserCheck size={14} />}
            disabled={busy}
            onClick={onCheckAccount}
          >
            {t("channels.checkAccount")}
          </Button>
        ) : null}
        <Button
          variant="secondary"
          icon={<RefreshCw size={14} className={busy ? "spin" : ""} />}
          disabled={busy}
          onClick={onRefresh}
        >
          {t("channels.fetchModels")}
        </Button>
        <Button
          variant="secondary"
          disabled={busy}
          onClick={onEdit}
          icon={<Pencil size={14} />}
        >
          {t("common.edit")}
        </Button>
        <p className="detail-actions-hint is-quiet">{t("channels.pathHint")}</p>
      </div>
    </>
  );
}

function CreateKeyDialog({
  channelName,
  channelId,
  pending,
  error,
  syncPending,
  onClose,
  onCreate,
  onSync,
}: {
  channelName: string;
  channelId: number;
  pending: boolean;
  error: unknown;
  syncPending: boolean;
  onClose: () => void;
  onCreate: (group: string) => void;
  onSync: () => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const groupsQuery = useQuery({
    queryKey: ["token-groups", channelId],
    queryFn: ({ signal }) => service.tokenGroups(channelId, signal),
    retry: false,
  });
  const [group, setGroup] = useState("");
  useEffect(() => {
    if (group) return;
    const groups = groupsQuery.data?.groups ?? [];
    if (groups.length > 0) setGroup(groups[0] ?? "");
  }, [groupsQuery.data, group]);
  const groupOptions: SelectOption[] = (groupsQuery.data?.groups ?? []).map(
    (value) => ({ value, label: value, group: "token-groups" }),
  );
  const groupsLoadFailed = Boolean(groupsQuery.isError);
  const canSubmit = !groupsLoadFailed && Boolean(group.trim());

  return (
    <Dialog
      title={t("channels.createKeyTitle")}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={pending}>
            {t("common.cancel")}
          </Button>
          <Button
            // After a failure the token already exists upstream; further
            // clicks would create duplicate keys, so lock creation until the
            // dialog is dismissed (operator should sync instead).
            disabled={pending || !canSubmit || Boolean(error)}
            onClick={() => onCreate(group.trim())}
          >
            {t("channels.createKeyConfirm")}
          </Button>
        </>
      }
    >
      <p className="dialog-hint">
        {t("channels.createKeyHint")} <strong>{channelName}</strong>
      </p>
      <Field
        label={t("channels.createKeyGroup")}
        hint={t("channels.createKeyGroupHint")}
      >
        <SearchableSelect
          options={groupOptions}
          groups={["token-groups"]}
          value={group}
          onChange={(value) => setGroup(value ?? "")}
          disabled={pending}
          allowCustom
          placeholder={t("channels.createKeyGroupPlaceholder")}
        />
      </Field>
      {groupsLoadFailed ? (
        <div className="dialog-form-error">
          {t("channels.createKeyGroupsUnavailable")}
        </div>
      ) : null}
      {error ? (
        <div className="dialog-form-error">
          <ErrorState error={error} />
          <div className="create-key-sync-row">
            <Button
              variant="secondary"
              disabled={syncPending}
              onClick={onSync}
            >
              {t("channels.syncKeys")}
            </Button>
          </div>
        </div>
      ) : null}
    </Dialog>
  );
}

function AddChannelDialog({
  pending,
  error,
  onClose,
  onSave,
}: {
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (value: CreateConnectionInput, options: { verify: boolean }) => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [secret, setSecret] = useState("");
  const [typeHint, setTypeHint] = useState("openai-compatible");
  const [showAdvanced, setShowAdvanced] = useState(false);
  const canSubmit = Boolean(baseUrl.trim() && secret.trim());

  return (
    <Dialog
      title={t("channels.add")}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={pending}>
            {t("common.cancel")}
          </Button>
          <Button
            icon={<Cable size={16} />}
            disabled={pending || !canSubmit}
            onClick={() =>
              onSave(
                {
                  name,
                  base_url: baseUrl,
                  secret,
                  type_hint: typeHint,
                },
                { verify: true },
              )
            }
          >
            {pending ? t("common.working") : t("channels.saveAndVerify")}
          </Button>
        </>
      }
    >
			<div className="ops-panel-context">
				<span>{t("channels.addHint")}</span>
			</div>
      <div className="form-grid form-grid-single">
        <Field label={t("common.type")}>
          <SearchableSelect
            options={TYPE_OPTIONS}
            groups={TYPE_GROUPS}
            value={typeHint}
            onChange={(next) => {
              const provider = next ?? "openai-compatible";
              // Auto-fill the provider default base URL when the field is
              // empty or still holds the previous provider's default.
              setBaseUrl((current) => {
                const currentTrimmed = current.trim().replace(/\/+$/, "");
                const previousDefault = PROVIDER_BASE_URLS[typeHint] ?? "";
                if (
                  currentTrimmed === "" ||
                  (previousDefault &&
                    currentTrimmed === previousDefault.replace(/\/+$/, ""))
                ) {
                  return PROVIDER_BASE_URLS[provider] ?? "";
                }
                return current;
              });
              setTypeHint(provider);
            }}
            disabled={pending}
            allowCustom
            placeholder={t("common.type")}
          />
        </Field>
        <Field label={t("common.name")}>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("channels.namePlaceholder")}
            disabled={pending}
          />
        </Field>
        <Field label={t("common.baseUrl")} hint={t("channels.baseUrlHint")}>
          <input
            type="url"
            required
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            onBlur={() => {
              // AAH-style auto-detection: sniff the platform from the URL so
              // the operator does not have to pick the type manually.
              const url = baseUrl.trim();
              if (!url) return;
              service
                .detectSiteType(url)
                .then((detected) => {
                  if (detected.family && TYPE_OPTIONS.some((o) => o.value === detected.family)) {
                    setTypeHint(detected.family);
                  }
                })
                .catch(() => undefined);
            }}
            placeholder="https://api.example.com"
            disabled={pending}
          />
        </Field>
        <Field label={t("common.secret")}>
          <input
            type="password"
            autoComplete="new-password"
            required
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            disabled={pending}
          />
        </Field>
      </div>
      <button
        type="button"
        className={`advanced-toggle${showAdvanced ? " is-open" : ""}`}
        onClick={() => setShowAdvanced((value) => !value)}
      >
        <ChevronDown size={13} />
        {showAdvanced ? t("channels.hideAdvanced") : t("channels.showAdvanced")}
      </button>
      {showAdvanced ? (
        <div style={{ marginTop: 4 }}>
          <Button
            variant="secondary"
            disabled={pending || !canSubmit}
            onClick={() =>
              onSave(
                {
                  name,
                  base_url: baseUrl,
                  secret,
                  type_hint: typeHint,
                },
                { verify: false },
              )
            }
          >
            {t("channels.saveOnly")}
          </Button>
									<InfoTip label={t("channels.saveOnlyHint")} />
        </div>
      ) : null}

      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}

function EditChannelDialog({
  value,
  routeOverviews,
  site,
  credentials,
  credential,
  userCredential,
  pending,
  error,
	onClose,
	onSave,
	onToggleKey,
	onUpdateKeyModels,
	onDeleteKey,
	onAddApiKey,
	addApiKeyPending,
	onSyncKeys,
	syncKeysPending,
	onManageModels,
}: {
  value: Channel;
  routeOverviews?: RouteOverview[];
  site?: Site;
	credentials: Array<{
		id: number;
		kind: string;
		has_secret: boolean;
		status: string;
		checkin_enabled: boolean;
		meta_json?: string;
		models_csv?: string;
	}>;
  credential?: {
    id: number;
    kind: string;
    has_secret: boolean;
    checkin_enabled: boolean;
  };
  userCredential?: {
    id: number;
    kind: string;
    has_secret: boolean;
    checkin_enabled: boolean;
  };
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (value: {
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
    priority: number;
    weight: number;
    header_override?: string;
    system_prompt?: string;
    retry_config?: string;
    stable_first?: boolean;
    userToken: string;
    apiKey: string;
  }) => void;
	onToggleKey: (id: number, enabled: boolean) => void;
	onUpdateKeyModels: (id: number, modelsCsv: string) => void;
	onDeleteKey: (id: number) => void;
  onAddApiKey: (secret: string) => void;
  addApiKeyPending?: boolean;
  onSyncKeys: () => void;
  syncKeysPending?: boolean;
  onManageModels?: () => void;
}) {
	const { t } = useI18n();
	const inheritedBase = !value.base_url.trim();
	const initialBase = value.base_url || site?.base_url || "";
	const [name, setName] = useState(value.name);
	const [baseUrl, setBaseUrl] = useState(initialBase);
	// Per-key model allowlist drafts: id → input value while editing.
	const [keyModelsDraft, setKeyModelsDraft] = useState<Record<number, string>>({});
  const [typeHint, setTypeHint] = useState(
    value.type_hint || site?.platform || "openai-compatible",
  );
  const [priority, setPriority] = useState(value.priority);
  const [weight, setWeight] = useState(value.weight);
  const [headerOverride, setHeaderOverride] = useState(
    value.header_override ?? "",
  );
  const [systemPrompt, setSystemPrompt] = useState(value.system_prompt ?? "");
  const [retryConfig, setRetryConfig] = useState(value.retry_config ?? "");
  const [stableFirst, setStableFirst] = useState(value.stable_first ?? false);
  const [userToken, setUserToken] = useState(
    userCredential?.has_secret ? SECRET_MASK : "",
  );
  const [apiKey, setApiKey] = useState("");
  const [showAdvanced, setShowAdvanced] = useState(false);
  const canSubmit = Boolean(name.trim() && baseUrl.trim());
  const apiKeys = credentials.filter((item) => item.kind === "api_key");
  const service = api(useSession().client!);
  const discovered = useQuery({
    queryKey: ["discovered-models", value.id],
    queryFn: ({ signal }) => service.discoveredModels(value.id, signal),
  });
  const editModels = discovered.data ?? [];
  const aliasOf = (realModel: string) =>
    routeOverviews?.find((overview) => {
      if (!overview.route.mapping_json) return false;
      try {
        const parsed = JSON.parse(overview.route.mapping_json) as {
          real?: string;
        };
        return (
          parsed.real === realModel &&
          (overview.members ?? []).some(
            (member) => member.member.channel_id === value.id,
          )
        );
      } catch {
        return false;
      }
    });

  return (
    <Drawer
      title={t("channels.edit")}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={pending}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={pending || !canSubmit}
            onClick={() =>
              onSave({
                channel: value,
                site,
                userCredential,
                relayCredential: credential,
                name,
                base_url: baseUrl,
                type_hint: typeHint,
                priority,
                weight,
    header_override: headerOverride,
    system_prompt: systemPrompt,
    retry_config: retryConfig,
    stable_first: stableFirst,
                userToken,
                apiKey,
              })
            }
          >
            {pending ? t("common.working") : t("common.save")}
          </Button>
        </>
      }
    >
      <>
			<div className="ops-panel-context">
				<span>{t("channels.editHintDual")}</span>
			</div>
        <div className="form-grid form-grid-single">
          <Field label={t("common.type")}>
            <SearchableSelect
              options={TYPE_OPTIONS}
              groups={TYPE_GROUPS}
              value={typeHint}
              onChange={setTypeHint}
              disabled={pending}
              allowCustom
              placeholder={t("common.type")}
            />
          </Field>
          <Field label={t("common.name")}>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={pending}
            />
          </Field>
          <Field
            label={t("common.baseUrl")}
            hint={
              inheritedBase
                ? t("channels.editBaseUrlInherited")
                : t("channels.baseUrlHint")
            }
          >
            <input
              type="url"
              required
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="https://api.example.com"
              disabled={pending}
            />
          </Field>
          <Field
            label={t("channels.userToken")}
            hint={
              userCredential?.has_secret
                ? t("channels.userTokenPresentHint")
                : t("channels.userTokenHint")
            }
          >
            <input
              type="password"
              autoComplete="new-password"
              value={userToken}
              onChange={(e) => setUserToken(e.target.value)}
              placeholder={
                userCredential?.has_secret
                  ? t("channels.editSecretPlaceholder")
                  : t("channels.userTokenEmptyPlaceholder")
              }
              disabled={pending}
            />
          </Field>
        </div>

        <section
          className="credential-key-panel"
          aria-label={t("channels.apiKeysTitle")}
        >
          <div className="credential-key-panel-head">
            <div>
              <strong>{t("channels.apiKeysTitle")}</strong>
              <p>{t("channels.apiKeysHint")}</p>
            </div>
            <Button
              variant="secondary"
              disabled={pending || Boolean(syncKeysPending)}
              onClick={onSyncKeys}
            >
              {syncKeysPending ? t("common.loading") : t("channels.syncKeys")}
            </Button>
          </div>

          {apiKeys.length === 0 ? (
            <p className="exchange-panel-note">{t("channels.apiKeysEmpty")}</p>
          ) : (
            <ul className="credential-key-list">
              {apiKeys.map((item) => {
                const meta = parseCredentialMeta(item.meta_json);
                const enabled = item.status === "enabled";
                const usedByThisConnection = value.credential_id === item.id;
                const label =
                  meta.name?.trim() ||
                  t("channels.apiKeyUnnamed", { id: item.id });
                const groupLabel =
                  meta.group?.trim() || t("channels.apiKeyGroupDefault");
                return (
                  <li
                    key={item.id}
                    className={[
                      "credential-key-row",
                      usedByThisConnection ? "is-bound" : "",
                      !enabled ? "is-disabled" : "",
                    ]
                      .filter(Boolean)
                      .join(" ")}
                  >
						<div className="credential-key-main">
							<strong>{label}</strong>
							<small>
								{`${groupLabel} · #${item.id}`}
								{usedByThisConnection
									? ` · ${t("channels.apiKeyUsedByConnection")}`
									: ""}
								{!item.has_secret
									? ` · ${t("channels.apiKeyNoSecret")}`
									: ""}
							</small>
							<div style={{ display: "flex", gap: 6, alignItems: "center", marginTop: 6 }}>
								<input
									className="mono"
									style={{ flex: 1, minWidth: 0, fontSize: 12 }}
									placeholder={t("channels.keyModelsPlaceholder")}
									title={t("channels.keyModelsHint")}
									value={keyModelsDraft[item.id] ?? item.models_csv ?? ""}
									disabled={pending}
									onChange={(event) =>
										setKeyModelsDraft((prev) => ({
												...prev,
												[item.id]: event.target.value,
											}))
									}
									onBlur={() => {
										const draft = keyModelsDraft[item.id];
										if (draft === undefined) return;
										const next = draft.trim();
										if (next !== (item.models_csv ?? "")) {
											onUpdateKeyModels(item.id, next);
										}
										setKeyModelsDraft((prev) => {
												const copy = { ...prev };
												delete copy[item.id];
												return copy;
											});
									}}
									onKeyDown={(event) => {
										if (event.key === "Enter") {
											(event.target as HTMLInputElement).blur();
										}
									}}
								/>
							</div>
						</div>
                    <div className="credential-key-actions">
                      <label className="check credential-key-enable">
                        <input
                          type="checkbox"
                          checked={enabled}
                          disabled={pending}
                          onChange={(event) =>
                            onToggleKey(item.id, event.target.checked)
                          }
                        />
                        <span>
                          {enabled ? t("common.enabled") : t("common.disabled")}
                        </span>
                      </label>
                      <button
                        type="button"
                        className="icon-button"
                        aria-label={t("channels.apiKeyDelete")}
                        title={t("channels.apiKeyDelete")}
                        disabled={pending}
                        onClick={() => {
                          if (
                            window.confirm(
                              t("channels.apiKeyDeleteConfirm", {
                                name: label,
                              }),
                            )
                          ) {
                            onDeleteKey(item.id);
                          }
                        }}
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}

          <Field
            label={t("channels.apiKeyAdd")}
            hint={t("channels.apiKeyAddHint")}
          >
            <div className="credential-key-add-row">
              <input
                type="password"
                autoComplete="new-password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={t("channels.apiKeyPlaceholder")}
                disabled={pending || Boolean(addApiKeyPending)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    const secret = apiKey.trim();
                    if (!secret || pending || addApiKeyPending) return;
                    onAddApiKey(secret);
                    setApiKey("");
                  }
                }}
              />
              <Button
                variant="secondary"
                disabled={
                  pending || Boolean(addApiKeyPending) || !apiKey.trim()
                }
                onClick={() => {
                  const secret = apiKey.trim();
                  if (!secret) return;
                  // First key becomes the relay key; later keys just join the pool.
                  onAddApiKey(secret);
                  setApiKey("");
                }}
              >
                {addApiKeyPending
                  ? t("common.loading")
                  : t("channels.apiKeyAddSave")}
              </Button>
            </div>
          </Field>
        </section>

        <section
          className="detail-section"
          aria-label={t("channels.modelsSection")}
        >
          <div className="detail-section-head">
            <h3>{t("channels.modelsSection")}</h3>
            <span className="detail-section-count">{editModels.length}</span>
            <button
              type="button"
              className="detail-section-expand"
              onClick={onManageModels}
            >
              <ExternalLink size={12} />
              {t("channels.modelsManage")}
            </button>
          </div>
          {discovered.isLoading ? (
            <p className="detail-section-empty is-quiet">
              {t("common.loading")}…
            </p>
          ) : editModels.length === 0 ? (
            <p className="detail-section-empty is-quiet">
              {t("channels.modelsEmpty")}
            </p>
          ) : (
            <ul className="channel-model-list is-compact">
              {editModels.map((model) => {
                const existingAlias = aliasOf(model.model_name);
                const alias = existingAlias?.route.model_pattern ?? "";
                return (
                  <li key={model.id} className="channel-model-row">
                    <span className="mono truncate" title={model.model_name}>
                      {model.model_name}
                    </span>
                    {alias ? (
                      <span className="capability-chip is-key">{alias}</span>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          )}
        </section>

        <button
          type="button"
          className={`advanced-toggle${showAdvanced ? " is-open" : ""}`}
          onClick={() => setShowAdvanced((v) => !v)}
        >
          <ChevronDown size={13} />
          {showAdvanced
            ? t("channels.hideAdvanced")
            : t("channels.showAdvanced")}
        </button>
        {showAdvanced ? (
          <div className="form-grid">
            <Field
              label={t("common.priority")}
              hint={t("channels.priorityHint")}
            >
              <input
                type="number"
                value={priority}
                onChange={(e) => setPriority(Number(e.target.value) || 0)}
                disabled={pending}
              />
            </Field>
            <Field label={t("common.weight")} hint={t("channels.weightHint")}>
              <input
                type="number"
                value={weight}
                onChange={(e) => setWeight(Number(e.target.value) || 0)}
                disabled={pending}
              />
            </Field>
          </div>
        ) : null}
        <section className="detail-section">
          <div className="detail-section-head">
            <h3>{t("channels.overrides")}</h3>
          </div>
          <Field
            label={t("channels.headerOverride")}
            hint={t("channels.headerOverrideHint")}
          >
            <textarea
              className="mono"
              value={headerOverride}
              onChange={(e) => setHeaderOverride(e.target.value)}
              disabled={pending}
              placeholder='{"User-Agent": "…", "X-Custom": "value"}'
              style={{ minHeight: 64 }}
            />
          </Field>
          <Field
            label={t("channels.systemPrompt")}
            hint={t("channels.systemPromptHint")}
          >
            <textarea
              value={systemPrompt}
              onChange={(e) => setSystemPrompt(e.target.value)}
              disabled={pending}
              placeholder={t("channels.systemPromptPlaceholder")}
              style={{ minHeight: 72 }}
            />
          </Field>
          <Field
            label={t("channels.retryConfig")}
            hint={t("channels.retryConfigHint")}
          >
            <textarea
              value={retryConfig}
              onChange={(e) => setRetryConfig(e.target.value)}
              disabled={pending}
              placeholder={t("channels.retryConfigPlaceholder")}
              style={{ minHeight: 72, fontFamily: "var(--font-mono)" }}
            />
          </Field>
          <label className="check" style={{ marginTop: "0.75rem" }}>
            <input
              type="checkbox"
              checked={stableFirst}
              onChange={(e) => setStableFirst(e.target.checked)}
              disabled={pending}
            />
            <span>{t("channels.stableFirst")}</span>
          </label>
        </section>

        {error ? <ErrorState error={error} /> : null}
      </>
    </Drawer>
  );
}
