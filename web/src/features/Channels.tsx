import { HeartPulse, Plus, RefreshCw, UserCheck } from "lucide-react";
import { useEffect, useCallback, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { Channel, ChannelOverview, Site } from "../api/types";
import type { ActionMenuItem } from "../components/ActionMenu";
import { ThemeDetails } from "../themes/ThemeDetails";
import { ResultStrip } from "../components/ResultStrip";
import { TelemetryStrip } from "../components/TelemetryStrip";
import { Button, Page } from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useAutomationMaster } from "../hooks/useAutomationMaster";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useToast } from "../toast";
import { formatErrorMessage } from "../formatError";
import { useSession } from "../session";

import { channelNeedsAttention, isChannelReady } from "./channelHealth";
import { ChannelDetail } from "./channels/ChannelDetail";
import {
  isMissingAPIKey,
  normalizeBase,
  SECRET_MASK,
  type CreateConnectionInput,
} from "./channels/helpers";
import { channelActions, type ChannelActionDeps } from "./channels/channelActions";
import { readChannelTab, writeChannelTab } from "./channels/tabState";
import { useChannelFilters } from "./channels/useChannelFilters";
import {
  credentialPatch as buildCredentialPatch,
  credentialShouldBeRemoved,
} from "./channels/credentialPatch";
import { ChannelDialogs } from "./channels/ChannelDialogs";
import { KeepaliveDialog } from "./channels/KeepaliveDialog";
import { useChannelBoard } from "./channels/useChannelBoard";
import {
  relayCredentialFor as pickRelayCredential,
  userCredentialFor as pickUserCredential,
} from "./channels/channelCredentials";
import { useListSelection } from "../lib/useListSelection";
import { useChannelBulk } from "./channels/useChannelBulk";
import { ChannelDirectory } from "./channels/ChannelDirectory";
import { parseCredentialMeta } from "./credentialMeta";
export { channelReadiness } from "./channelHealth";

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
  const { t } = useI18n();
  const toast = useToast();
  const service = api(client!);
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  // The board's own state (queries, dialogs, deep links, selection) lives in
  // channels/useChannelBoard; local names are destructured to keep the rest of the
  // page reading as before.
  const board = useChannelBoard({ service, params, setParams });
  const {
    overviews,
    sites,
    routeOverviewsQuery,
    credentials,
    addOpen,
    setAddOpen,
    remove,
    setRemove,
    edit,
    setEdit,
    modelsChannel,
    setModelsChannel,
    keysChannel,
    setKeysChannel,
    createKeyChannel,
    setCreateKeyChannel,
    createKeyLocked,
    closeModelsDrawer,
    selectedId,
    inspectorOpen,
    setInspectorOpen,
    contextMenu,
    setContextMenu,
    stageMessage,
    setStageMessage,
  } = board;
  const filters = useChannelFilters(params, setParams);
  const {
    query,
    setQuery,
    healthFilter,
    setHealthFilter,
    typeFilter,
    setTypeFilter,
    groupFilter,
    setGroupFilter,
    updateFilterParam,
    toggleHealthFilter,
  } = filters;
  const verifyAfterCreate = useRef(false);
  const runVerifyRef = useRef<(channelId: number, name: string) => void>(() => undefined);

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
          models: (refreshResult.models ?? []).length,
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
  // The keepalive workspace is a dialog rather than a tab: it reads and writes
  // the same sites the rest of this page manages, and an operator opens it when
  // a window is approaching, not on every visit.
  const [keepaliveOpen, setKeepaliveOpen] = useState(false);

  const createConnection = useAdminMutation({
    // The advanced fields the dialog collects go through the same update path an
    // existing channel uses, and they go BEFORE the verify step: 调用策略 decides
    // whether this site may be probed at all, so a create-then-verify that ran the
    // probe first would knock on a door the operator just declared off limits.
    mutationFn: async (input: CreateConnectionInput) => {
      const created = await service.createConnection({
        name: input.name.trim(),
        base_url: normalizeBase(input.base_url),
        secret: input.secret.trim(),
        type_hint: input.type_hint || "openai-compatible",
        group_name: input.group_name?.trim() || "default",
        status: "enabled",
        ...(input.models_csv !== undefined ? { models_csv: input.models_csv } : {}),
        // Explicit choice from the dialog; undefined would silently inherit
        // the system default, which is exactly what used to confuse people.
        model_sync_mode: input.model_sync_mode,
      });
      if (input.advanced && Object.keys(input.advanced).length > 0) {
        await service.updateChannel(created.channel.id, input.advanced);
      }
      return created;
    },
    invalidateKeys: [...INVALIDATE],
    toastOnError: false,
    onSuccess: (result) => {
      setAddOpen(false);
      const nextParams = new URLSearchParams(params);
      nextParams.set("id", String(result.channel.id));
      setParams(nextParams, { replace: true });
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
    },
    onError: () => {
      verifyAfterCreate.current = false;
    },
  });
  const refreshAll = useAdminMutation({
    mutationFn: () => service.refreshAll(),
    invalidateKeys: [...INVALIDATE],
  });

  // Bulk selection over the connections table (current-page checkboxes).
  // Owned by lib/useListSelection (shared with the models board); the local names
  // stay so the rest of this page reads as before.
  const selection = useListSelection();
  const bulkSelected = selection.selected;
  const bulkMode = selection.mode;
  const setBulkSelected = selection.setSelected;
  const setBulkMode = selection.setMode;
  const exitBulkMode = selection.exit;
  const toggleBulkSelected = selection.toggle;
  // Bulk operations: owned by channels/useChannelBulk (both share one rule — after
  // a run the selection is exactly the failures). Local names kept.
  const bulk = useChannelBulk({
    service,
    overviews: overviews.data ?? [],
    invalidateKeys: INVALIDATE,
    toast,
    t,
    setSelected: selection.setSelected,
  });
  const bulkFailures = bulk.failures;
  const bulkSync = bulk.sync;
  const bulkStatus = bulk.status;
  const bulkBusy = bulk.busy;

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
      const next = overview.channel.status === "enabled" ? "disabled" : "enabled";
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
        auth_mode?: string;
        has_secret: boolean;
        has_cookie?: boolean;
        checkin_enabled: boolean;
        meta_json?: string;
      };
      relayCredential?: {
        id: number;
        kind: string;
        auth_mode?: string;
        has_secret: boolean;
        has_cookie?: boolean;
        checkin_enabled: boolean;
      };
      name: string;
      base_url: string;
      type_hint: string;
      group_name?: string;
      max_reasoning_effort?: string;
      payload_rules?: string;
      proxy_url?: string;
      max_concurrent?: number;
      priority: number;
      weight: number;
      header_override?: string;
      system_prompt?: string;
      retry_config?: string;
      model_sync_mode?: "auto" | "manual";
      upstream_path_override?: string;
      upstream_path_map?: string;
      upstream_request_map?: string;
      upstream_response_map?: string;
      stable_first?: boolean;
      call_policy?: "" | "allow_probe" | "real_calls_only";
      /** Cumulative spend / token ceilings (0 = none); see the drawer's
       *  「累计成本上限」. The counters and the hit marker are the store's. */
      usage_limit_cost?: number;
      usage_limit_tokens?: number;
      userToken: string;
      userCookie: string;
      /** New-API family numeric user id (`New-Api-User`). Empty = unknown. */
      userID: string;
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
      const userCookieRaw = input.userCookie.trim();
      const userCookieKept = userCookieRaw === SECRET_MASK;
      const userCookie = userCookieKept ? "" : userCookieRaw;
      const userAuthMode = userCookieKept
        ? input.userCredential?.auth_mode || (userToken ? "access_token" : "cookie")
        : userCookie
          ? userToken
            ? "auto"
            : "cookie"
          : "access_token";
      const apiKey = input.apiKey.trim();
      const userCred = input.userCredential;
      const relayCred = input.relayCredential;
      // The New-API numeric user id lives in credential meta_json
      // ({"platform_user_id":1544}). An empty field means "unknown": the
      // gateway keeps resolving it from /api/user/self at check-in time, so
      // only write when the operator typed something or cleared a stored id.
      const userIDRaw = input.userID.trim();
      const storedUserID = parseCredentialMeta(userCred?.meta_json).platform_user_id;
      const userID = /^[0-9]+$/.test(userIDRaw) ? Number(userIDRaw) : undefined;
      const metaChanged = userID !== storedUserID;
      // The mask means "keep the stored value". An empty field means "remove"
      // only when something was actually stored; otherwise every save of an
      // untouched dialog would fire a pointless credential write.
      const cookieChanged = !userCookieKept && (userCookie !== "" || Boolean(userCred?.has_cookie));
      const secretChanged = !userTokenKept && (userToken !== "" || Boolean(userCred?.has_secret));
      // Both auth materials were explicitly cleared → remove the credential.
      const clearCredential = credentialShouldBeRemoved({
        userCred,
        userToken,
        userCookie,
        secretChanged,
        cookieChanged,
      });
      /** Only the fields that actually changed are sent; see channels/credentialPatch.ts. */
      const credentialPatch = () =>
        buildCredentialPatch({
          userCred,
          userToken,
          userCookie,
          userID,
          userAuthMode,
          secretChanged,
          cookieChanged,
          metaChanged,
        });
      if (input.site) {
        if (userCred?.id) {
          if (clearCredential) {
            await service.deleteCredential(userCred.id);
          } else {
            const patch = credentialPatch();
            if (Object.keys(patch).length > 0) {
              await service.updateCredential(userCred.id, patch);
            }
          }
        } else if (userToken || userCookie) {
          await service.createCredential(input.site.id, {
            kind: "access_token",
            ...(userToken ? { secret: userToken } : {}),
            ...(userCookie ? { cookie: userCookie } : {}),
            ...(userID ? { meta_json: JSON.stringify({ platform_user_id: userID }) } : {}),
            auth_mode: userAuthMode,
            status: "enabled",
          });
        }
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
      } else if (relayCred?.id && relayCred.kind === "api_key" && relayCred.has_secret) {
        // Keep existing relay key bound when operator only edits other fields.
        relayCredentialId = relayCred.id;
      }
      const channelBase = input.channel.base_url.trim() ? base : input.channel.base_url;
      const siteId = input.channel.site_id ?? input.site?.id;
      // Sync mode is intentionally absent from the spread: the drawer may
      // have changed it since this dialog opened, and only an explicit
      // picker change (below) may touch it.
      const { model_sync_mode: _snapshotSyncMode, ...channelFields } = input.channel;
      return service.updateChannel(input.channel.id, {
        ...channelFields,
        name,
        base_url: channelBase,
        type_hint: typeHint,
        group_name: input.group_name ?? input.channel.group_name ?? "default",
        priority: input.priority,
        weight: input.weight,
        max_reasoning_effort: input.max_reasoning_effort ?? "",
        payload_rules: input.payload_rules ?? "",
        proxy_url: input.proxy_url ?? "",
        max_concurrent: input.max_concurrent ?? 0,
        header_override: input.header_override ?? "",
        system_prompt: input.system_prompt ?? "",
        retry_config: input.retry_config ?? "",
        upstream_path_override: input.upstream_path_override ?? "",
        upstream_path_map: input.upstream_path_map ?? "",
        upstream_request_map: input.upstream_request_map ?? "",
        upstream_response_map: input.upstream_response_map ?? "",
        ...(input.model_sync_mode ? { model_sync_mode: input.model_sync_mode } : {}),
        stable_first: input.stable_first ?? false,
        // "" is a value here, not a missing one: it hands the policy back to the
        // site, which is how the operator undoes a per-channel override.
        call_policy: input.call_policy ?? input.channel.call_policy ?? "",
        // The budget travels with the save; the counters and the hit marker do
        // not (the store re-derives them from the ledger when a limit changes).
        usage_limit_cost: input.usage_limit_cost ?? input.channel.usage_limit_cost ?? 0,
        usage_limit_tokens: input.usage_limit_tokens ?? input.channel.usage_limit_tokens ?? 0,
        site_id: siteId,
        credential_id: relayCredentialId,
      });
    },
    invalidateKeys: [...INVALIDATE, ["credentials"]],
    toastOnError: false,
    onSuccess: () => setEdit(null),
  });

  const setCredentialStatus = useAdminMutation({
    mutationFn: async (input: { id: number; status: "enabled" | "disabled" }) => {
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

  // Pool tier of one key. Single-field write on purpose: the credential PUT
  // preserves every key the body omits, so a tier change cannot disturb the
  // allowlist, the status, or the secret.
  const updateKeyPriority = useAdminMutation({
    mutationFn: async (input: { id: number; priority: number }) => {
      const list = credentials.data ?? [];
      const current = list.find((item) => item.id === input.id);
      return service.updateCredential(input.id, {
        kind: current?.kind || "api_key",
        priority: input.priority,
      });
    },
    invalidateKeys: [...INVALIDATE, ["credentials"]],
  });

  const addApiKeyCredential = useAdminMutation({
    mutationFn: async (input: { siteId: number; secret: string; name?: string }) => {
      const secret = input.secret.trim();
      if (!secret) {
        throw new Error("api key is required");
      }
      // Site-level key pool: all enabled api_keys aggregate for relay/discovery failover.
      return service.createCredential(input.siteId, {
        kind: "api_key",
        secret,
        status: "enabled",
        meta_json: JSON.stringify({
          name: input.name?.trim() || "manual",
          group: "default",
        }),
      });
    },
    invalidateKeys: [...INVALIDATE, ["credentials"]],
  });

  const deleteApiKeyCredential = useAdminMutation({
    mutationFn: (credentialId: number) => service.deleteCredential(credentialId),
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

  const checkinMaster = useAutomationMaster("checkin_enabled");
  const setCheckin = useAdminMutation({
    mutationFn: async (input: { credentialId: number; enabled: boolean }) => {
      const result = await service.setCheckin(input.credentialId, input.enabled);
      // An account that is "scheduled" while the console's scheduler is off would
      // never be checked in, which reads as a broken switch; turn the master on
      // and say so rather than leave the two switches disagreeing.
      if (input.enabled && (await checkinMaster.enable())) {
        toast.push({ tone: "success", message: t("channels.checkinMasterAutoOn") });
      }
      return result;
    },
    invalidateKeys: [["credentials"], ["channel-overviews"], ["runtime-settings"]],
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

  const filterOptions = useMemo(() => {
    const list = overviews.data ?? [];
    const types = [...new Set(list.map((item) => item.channel.type_hint).filter(Boolean))].sort();
    const groups = [...new Set(list.map((item) => item.channel.group_name).filter(Boolean))].sort();
    return { types, groups };
  }, [overviews.data]);
  // The filters an overview has to pass whatever the search says. Kept in one
  // place because the model group below has to obey them too.
  const passesFilters = useCallback(
    (overview: ChannelOverview) => {
      const ch = overview.channel;
      if (typeFilter !== "all" && ch.type_hint !== typeFilter) return false;
      if (groupFilter !== "all" && ch.group_name !== groupFilter) return false;
      if (healthFilter === "ready" && !isChannelReady(overview)) return false;
      if (healthFilter === "missing_key" && !isMissingAPIKey(overview)) return false;
      if (healthFilter === "attention" && !channelNeedsAttention(overview)) return false;
      return true;
    },
    [typeFilter, groupFilter, healthFilter],
  );
  const rows = useMemo(() => {
    const list = overviews.data ?? [];
    const term = query.trim().toLowerCase();
    return list.filter((overview) => {
      const ch = overview.channel;
      if (!passesFilters(overview)) return false;
      if (!term) return true;
      const site = ch.site_id != null ? siteById.get(ch.site_id) : undefined;
      const base = (ch.base_url || site?.base_url || "").toLowerCase();
      return (
        ch.name.toLowerCase().includes(term) || base.includes(term) || String(ch.id).includes(term)
      );
    });
  }, [overviews.data, query, siteById, passesFilters]);

  // The same term also searches MODELS, and that answer needs the catalog rather
  // than the loaded page: it arrives debounced and lands in its own group below
  // the name matches. A name match stays instant, because it is the one the
  // operator is usually typing towards.
  const [modelTerm, setModelTerm] = useState("");
  useEffect(() => {
    const handle = setTimeout(() => setModelTerm(query.trim()), 250);
    return () => clearTimeout(handle);
  }, [query]);
  const modelSearch = useQuery({
    queryKey: ["model-channels", modelTerm, "contains"],
    queryFn: ({ signal }) => service.modelChannels(modelTerm, "contains", signal),
    enabled: modelTerm.length >= 2,
  });
  const modelMatches = useMemo(() => {
    if (modelTerm.length < 2) return [];
    const shown = new Set(rows.map((overview) => overview.channel.id));
    const byId = new Map((overviews.data ?? []).map((overview) => [overview.channel.id, overview]));
    const out: Array<{ channelId: number; name: string; model: string; source: string }> = [];
    for (const match of modelSearch.data?.items ?? []) {
      // A channel already listed above is not listed twice.
      if (shown.has(match.channel_id)) continue;
      const overview = byId.get(match.channel_id);
      if (!overview || !passesFilters(overview)) continue;
      out.push({
        channelId: match.channel_id,
        name: overview.channel.name,
        model: match.model ?? "",
        source: match.source,
      });
    }
    return out;
  }, [modelSearch.data, modelTerm, rows, overviews.data, passesFilters]);

  const pagination = useClientPagination(rows, 20, "channels");
  const pageRows = pagination.pageItems;
  const pageAllSelected =
    pageRows.length > 0 && pageRows.every((o) => bulkSelected.has(o.channel.id));
  const selectAllPage = () => {
    setBulkSelected((prev) => {
      const next = new Set(prev);
      if (pageAllSelected) {
        pageRows.forEach((o) => next.delete(o.channel.id));
      } else {
        pageRows.forEach((o) => next.add(o.channel.id));
      }
      return next;
    });
  };
  const bulkHeader = (
    <input
      type="checkbox"
      aria-label={t("channels.bulkSelectPage")}
      checked={pageAllSelected}
      onChange={selectAllPage}
    />
  );

  // Selection for a URL that carries no usable ?id — in priority order: the
  // channel a deep-link is landing on, the selection this tab had last (a bare
  // sidebar navigation drops the query string), then the first row. This used
  // to be two effects, and the first-row one ran second: it overwrote both the
  // stored selection and the deep-link that opened the drawer.
  useEffect(() => {
    if (selectedId) return;
    // The ?channel=/?keys= effects above own the selection while their target is
    // still unread from the URL (they commit the id in this same render pass).
    if (params.get("channel") || params.get("keys")) return;
    if (!rows.length) return;
    const saved = readChannelTab<number | null>("selected", null);
    const target =
      saved != null && rows.some((r) => r.channel.id === saved) ? saved : rows[0]!.channel.id;
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("id", String(target));
        return next;
      },
      { replace: true },
    );
  }, [rows, selectedId, params, setParams]);

  const selected =
    rows.find((r) => r.channel.id === selectedId) ??
    (overviews.data ?? []).find((r) => r.channel.id === selectedId) ??
    null;

  const readyCount = (overviews.data ?? []).filter(isChannelReady).length;
  const missingKeyCount = (overviews.data ?? []).filter((o) => isMissingAPIKey(o)).length;
  const attentionCount = (overviews.data ?? []).filter((o) => {
    return channelNeedsAttention(o);
  }).length;

  const selectRow = (id: number) => {
    writeChannelTab("selected", id);
    const next = new URLSearchParams(params);
    next.set("id", String(id));
    setParams(next, { replace: true });
  };

  /** User token for check-in on a site; see channels/channelCredentials.ts. */
  const userCredentialFor = (overview?: ChannelOverview) =>
    pickUserCredential(overview, credentials.data ?? []);
  const relayCredentialFor = (overview: ChannelOverview) =>
    pickRelayCredential(overview, credentials.data ?? []);

  // Everything the action menu needs, in one explicit hand-off: the menu itself
  // lives in channels/channelActions.tsx (pure mapping, unit-testable), and this
  // is the board's half of the contract.
  const channelActionDeps: ChannelActionDeps = {
    t,
    navigate,
    pushError: (error) => toast.pushError(error),
    userCredentialFor,
    selectRow,
    setContextMenu,
    setBulkMode,
    setBulkSelected,
    setEdit,
    setCreateKeyChannel,
    setRemove,
    setInspectorOpen,
    mutations: {
      refresh,
      probe,
      accountProbe,
      syncKeys,
      toggle,
      del,
      duplicate,
      createUpstreamKey,
      setCheckin,
      runCheckin,
      saveEdit,
    },
  };

  const connectionActions = (
    overview: ChannelOverview,
    options?: { closeContext?: boolean },
  ): ActionMenuItem[] => channelActions(overview, channelActionDeps, options);

  const openAdd = () => {
    createConnection.reset();
    verifyAfterCreate.current = false;
    setAddOpen(true);
  };

  const submitCreate = (value: CreateConnectionInput, options: { verify: boolean }) => {
    verifyAfterCreate.current = options.verify;
    createConnection.mutate(value);
  };

  const retryVerify = (channelId: number) => {
    const name = stageMessage?.name ?? `#${channelId}`;
    runVerifyRef.current(channelId, name);
  };

  return (
    <Page
      title={t("channels.title")}
      description={t("channels.description")}
      actions={
        <>
          <Button
            variant="secondary"
            icon={<RefreshCw size={16} className={refreshAll.isPending ? "spin" : ""} />}
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
            icon={<UserCheck size={16} className={checkAllTokens.isPending ? "spin" : ""} />}
            disabled={checkAllTokens.isPending || !rows.length}
            onClick={() => {
              checkAllTokens.reset();
              checkAllTokens.mutate();
            }}
          >
            {t("channels.checkAllTokens")}
          </Button>
          <Button
            variant="secondary"
            icon={<HeartPulse size={16} />}
            disabled={!rows.length}
            onClick={() => setKeepaliveOpen(true)}
          >
            {t("channels.keepalive.open")}
          </Button>
          <Button icon={<Plus size={16} />} onClick={openAdd}>
            {t("channels.add")}
          </Button>
        </>
      }
    >
      <div className="ops-canvas">
        {bulkFailures.length > 0 ? (
          <div role="alert" className="inline-error">
            {bulkFailures.map((failure) => (
              <p key={failure.item}>
                #{failure.item}: {formatErrorMessage(failure.error, t)}
              </p>
            ))}
          </div>
        ) : null}
        <TelemetryStrip
          items={[
            {
              label: t("channels.stat.total"),
              value: overviews.data?.length ?? "—",
              onClick: () => setHealthFilter("all"),
              active: healthFilter === "all",
              hint: t("channels.stat.totalHint"),
              tone: "primary",
            },
            {
              label: t("channels.stat.ready"),
              value: overviews.isPending ? "—" : readyCount,
              onClick: () => toggleHealthFilter("ready"),
              active: healthFilter === "ready",
              hint: t("channels.stat.readyHint"),
              tone: "success",
            },
            {
              label: t("channels.stat.missingKey"),
              value: overviews.isPending ? "—" : missingKeyCount,
              onClick: () => toggleHealthFilter("missing_key"),
              active: healthFilter === "missing_key",
              hint: t("channels.stat.missingKeyHint"),
              tone: "warning",
            },
            {
              label: t("channels.stat.attention"),
              value: overviews.isPending ? "—" : attentionCount,
              onClick: () => toggleHealthFilter("attention"),
              active: healthFilter === "attention",
              hint: t("channels.stat.attentionHint"),
              tone: "danger",
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
            <span>{t("channels.verifyFailed", { name: stageMessage.name })}</span>
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
          <ResultStrip status={refreshAll.data.failure_count > 0 ? "error" : "success"}>
            {t("ops.refreshSummary", {
              success: refreshAll.data.success_count,
              failure: refreshAll.data.failure_count,
            })}
          </ResultStrip>
        ) : null}
        {checkAllTokens.data ? (
          <ResultStrip
            status={checkAllTokens.data.items.some((item) => !item.ok) ? "error" : "success"}
          >
            {t("channels.checkAllTokensSummary", {
              success: checkAllTokens.data.items.filter((item) => item.ok).length,
              failure: checkAllTokens.data.items.filter((item) => !item.ok).length,
            })}
          </ResultStrip>
        ) : null}
        {refresh.data && stageMessage?.kind !== "created_and_verified" ? (
          <ResultStrip status="success">
            {t("channels.refreshResult", {
              id: refresh.data.channel_id,
              models: (refresh.data.models ?? []).length,
            })}
          </ResultStrip>
        ) : null}
        {probe.data ? (
          <ResultStrip status="success">
            {t("channels.probeResult", {
              id: probe.data.channel_id,
              models: (probe.data.models ?? []).length,
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
              syncKeys.data.created_credentials + syncKeys.data.reused_credentials > 0
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
              {(syncKeys.data.created_channels || syncKeys.data.updated_channels) &&
                ` ${t("channels.syncKeysGroups", {
                  created: syncKeys.data.created_channels ?? 0,
                  updated: syncKeys.data.updated_channels ?? 0,
                })}`}
              {syncKeys.data.message
                ? ` — ${formatErrorMessage(syncKeys.data.message, t)}`
                : syncKeys.data.empty_list
                  ? ` — ${t("channels.syncKeysEmpty")}`
                  : syncKeys.data.skipped_masked > 0 &&
                      syncKeys.data.created_credentials + syncKeys.data.reused_credentials === 0
                    ? ` — ${t("channels.syncKeysMasked")}`
                    : ""}
            </span>
          </ResultStrip>
        ) : null}

        <div className="channels-workspace">
          <ChannelDirectory
            t={t}
            query={query}
            setQuery={setQuery}
            params={params}
            setParams={setParams}
            typeFilter={typeFilter}
            setTypeFilter={setTypeFilter}
            groupFilter={groupFilter}
            setGroupFilter={setGroupFilter}
            updateFilterParam={updateFilterParam}
            filterOptions={filterOptions}
            healthFilter={healthFilter}
            setHealthFilter={setHealthFilter}
            overviews={overviews}
            rows={rows}
            modelMatches={modelMatches}
            pageRows={pageRows}
            siteById={siteById}
            selected={selected}
            pagination={pagination}
            bulkMode={bulkMode}
            bulkSelected={bulkSelected}
            toggleBulkSelected={toggleBulkSelected}
            setBulkSelected={setBulkSelected}
            exitBulkMode={exitBulkMode}
            bulkBusy={bulkBusy}
            bulkSync={bulkSync}
            bulkStatus={bulkStatus}
            bulkHeader={bulkHeader}
            openAdd={openAdd}
            selectRow={selectRow}
            setInspectorOpen={setInspectorOpen}
            setContextMenu={setContextMenu}
            contextMenu={contextMenu}
            connectionActions={connectionActions}
            pending={{ refresh, probe, accountProbe, syncKeys, toggle, del }}
          />

          <ThemeDetails
            open={inspectorOpen}
            title={t("channels.details")}
            onClose={() => setInspectorOpen(false)}
          >
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
                    accountProbe.data?.channel_id === selected.channel.id ? accountProbe.data : null
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
                  pingResult={ping.data?.channel_id === selected.channel.id ? ping.data : null}
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
          </ThemeDetails>
        </div>
      </div>

      {keepaliveOpen ? <KeepaliveDialog onClose={() => setKeepaliveOpen(false)} /> : null}

      <ChannelDialogs
        t={t}
        toast={toast}
        params={params}
        setParams={setParams}
        siteById={siteById}
        overviews={overviews.data ?? []}
        routeOverviews={routeOverviewsQuery.data}
        credentials={credentials.data ?? []}
        userCredentialFor={userCredentialFor}
        relayCredentialFor={relayCredentialFor}
        addOpen={addOpen}
        setAddOpen={setAddOpen}
        edit={edit}
        setEdit={setEdit}
        modelsChannel={modelsChannel}
        setModelsChannel={setModelsChannel}
        keysChannel={keysChannel}
        setKeysChannel={setKeysChannel}
        createKeyChannel={createKeyChannel}
        setCreateKeyChannel={setCreateKeyChannel}
        remove={remove}
        setRemove={setRemove}
        createKeyLocked={createKeyLocked}
        closeModelsDrawer={closeModelsDrawer}
        submitCreate={submitCreate}
        mutations={{
          createConnection,
          saveEdit,
          setCredentialStatus,
          addApiKeyCredential,
          deleteApiKeyCredential,
          createUpstreamKey,
          syncKeys,
          updateKeyModels,
          updateKeyPriority,
          refresh,
          del,
        }}
      />
    </Page>
  );
}
export { capabilityFlags } from "./channels/helpers";
