import {
  Ban,
  Check,
  ChevronDown,
  Copy,
  Eye,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Ticket,
  Trash2,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
  CreatedDownstreamKey,
  KeyCreateInput,
  KeyUpdateInput,
  DownstreamKey,
  RouteOverview,
} from "../api/types";
import { ActionMenu, type ActionMenuItem } from "../components/ActionMenu";
import { rowContextPoint, rowKeyboardContextPoint } from "../components/contextMenu";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { ModelPicker, type ModelOption } from "../components/ModelPicker";
import { ScopePicker } from "../components/ScopePicker";
import { autoModelGroup, modelGroup, modelPatternMatches } from "./models/modelGroups";
import { PaginationBar } from "../components/PaginationBar";
import { QuotaBar, quotaPercent } from "../components/QuotaMeter";
import { SecretRevealDialog } from "../components/SecretRevealDialog";
import { EntityState } from "../components/EntityState";
import { TelemetryStrip } from "../components/TelemetryStrip";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { useOperatingMode } from "../hooks/useOperatingMode";
import { memberKeysSource } from "../member/MemberKeysSource";
import { KeyCard } from "./keys/KeyCard";
import { parseQuotaInput } from "./keys/quotaInput";
import { mappingRealName } from "../lib/alias";
import { formatCost as formatLedgerCost, useCurrency } from "../lib/format";
import {
  ADMIN_KEY_CAPS,
  MEMBER_KEY_CAPS,
  type KeysCapabilities,
  type KeysSource,
} from "./keys/KeysSource";
import {
  Button,
  ConfirmDialog,
  DataTable,
  Dialog,
  ErrorState,
  Field,
  IconButton,
  Page,
  Panel,
  StatusBadge,
  formatDate,
} from "../components/ui";

/**
 * The token's on/off switch, sitting where its state is read.
 *
 * Enablement is not the same as a quota: an operator pauses a token whose
 * client is misbehaving and turns it back on later, without rotating or
 * deleting anything. The status badge this replaces could only report the
 * state, so pausing a token meant editing the record outside the console.
 */
function EnabledSwitch({
  on,
  name,
  pending,
  onToggle,
}: {
  on: boolean;
  name: string;
  pending: boolean;
  onToggle: () => void;
}) {
  const { t, status } = useI18n();
  const action = t(on ? "common.disableAction" : "common.enableAction");
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={`${name} · ${action}`}
      title={action}
      disabled={pending}
      className={`badge badge-${on ? "enabled" : "disabled"} key-toggle`}
      onClick={onToggle}
    >
      {status(on)}
    </button>
  );
}

// Redemption code manager: mint quota vouchers, copy them, void unused ones.
function RedemptionDialog({ onClose }: { onClose: () => void }) {
  const { client } = useSession();
  const service = api(client!);
  const { t } = useI18n();
  const [count, setCount] = useState(1);
  const [quota, setQuota] = useState(1000000);
  const [minted, setMinted] = useState<Array<{ id: number; code: string }>>([]);
  const query = useQuery({
    queryKey: ["redemption-codes"],
    queryFn: ({ signal }) => service.listRedemptionCodes(signal),
  });
  const mint = useAdminMutation({
    mutationFn: () => service.createRedemptionCodes({ count, quota_tokens: quota }),
    invalidateKeys: [["redemption-codes"]],
    toastOnError: false,
    onSuccess: (result) => setMinted(result.items),
  });
  const voidCode = useAdminMutation({
    mutationFn: (id: number) => service.deleteRedemptionCode(id),
    pendingIdOf: (id) => id,
    invalidateKeys: [["redemption-codes"]],
    toastOnError: false,
  });
  const items = query.data?.items ?? [];
  return (
    <Dialog
      title={t("keys.redemptionTitle")}
      onClose={onClose}
      busy={mint.isPending || voidCode.isPending}
    >
      <div className="redemption-mint">
        <Field label={t("keys.redemptionCount")}>
          <input
            type="number"
            min={1}
            max={100}
            value={count}
            onChange={(e) => setCount(Number(e.target.value) || 1)}
          />
        </Field>
        <Field label={t("keys.redemptionQuota")}>
          <input
            type="number"
            min={1}
            value={quota}
            onChange={(e) => setQuota(Number(e.target.value) || 0)}
          />
        </Field>
        <Button
          variant="primary"
          disabled={mint.isPending || count < 1 || quota < 1}
          onClick={() => mint.mutate(undefined)}
        >
          {mint.isPending ? t("common.working") : t("keys.redemptionMint")}
        </Button>
      </div>
      {minted.length ? (
        <div className="redemption-minted">
          {minted.map((m) => (
            <div key={m.id} className="redemption-code-row">
              <code>{m.code}</code>
              <button
                type="button"
                className="redemption-copy"
                onClick={() => {
                  void navigator.clipboard.writeText(m.code);
                }}
              >
                {t("keys.redemptionCopy")}
              </button>
            </div>
          ))}
        </div>
      ) : null}
      <div className="redemption-list">
        <strong>{t("keys.redemptionList")}</strong>
        {items.length === 0 ? (
          <p className="is-quiet">{t("keys.redemptionEmpty")}</p>
        ) : (
          items.slice(0, 50).map((c) => (
            <div key={c.id} className="redemption-list-row">
              <code className={c.redeemed_by_key_id ? "is-used" : ""}>{c.code}</code>
              <span>{formatNumber(c.quota_tokens)}</span>
              {c.redeemed_by_key_id ? (
                <span className="is-quiet">
                  {t("keys.redemptionUsed")} · #{c.redeemed_by_key_id}
                </span>
              ) : (
                <button
                  type="button"
                  className="redemption-void"
                  disabled={voidCode.pendingId === c.id}
                  onClick={() => voidCode.mutate(c.id)}
                >
                  {voidCode.pendingId === c.id ? t("common.working") : t("keys.redemptionVoid")}
                </button>
              )}
            </div>
          ))
        )}
      </div>
      {mint.error ? <ErrorState error={mint.error} /> : null}
      {voidCode.error ? <ErrorState error={voidCode.error} /> : null}
    </Dialog>
  );
}

function formatNumber(value?: number) {
  if (value == null || Number.isNaN(value)) return "0";
  return new Intl.NumberFormat().format(value);
}

function formatQuota(used?: number, total?: number) {
  const usedLabel = formatNumber(used ?? 0);
  if (!total || total <= 0) return `${usedLabel} / ∞`;
  return `${usedLabel} / ${formatNumber(total)}`;
}

function formatCost(value?: number) {
  if (value == null || !Number.isFinite(value)) return "—";
  return formatLedgerCost(value);
}

/**
 * Upstream model names behind a route's {"real": …} alias mapping.
 *
 * Aliases are written on the member (one per channel) with a legacy
 * route-level form for older rows; to the picker both mean the same thing:
 * this route name is the name a client sends for that upstream model.
 */
function aliasReals(overview: RouteOverview): string[] {
  const out = new Set<string>();
  const collect = (raw?: string) => {
    const real = mappingRealName(raw);
    if (real) out.add(real);
  };
  collect(overview.route.mapping_json);
  for (const candidate of overview.members ?? []) {
    collect(candidate.member.mapping_json);
  }
  return [...out];
}

/**
 * The console's keys page: the shared renderer bound to the admin client, with
 * every capability on. The member app mounts `KeysView` directly with its own
 * source — this wrapper exists so the console's route table stays unchanged.
 */
/**
 * The token page, for whoever is signed in.
 *
 * Staff manage every token in the gateway; a member manages their own, through
 * the member source (/me/keys). Both mount the same renderer — the capability
 * flags are what differ, and they come from the role rather than from which app
 * this is, because there is now only one app.
 */
export function Keys() {
  const { client, role } = useSession();
  const member = role === "member";
  const service = useMemo(() => api(client!), [client]);
  // The tenant-group vocabulary belongs to the multi-user area, so it is shown
  // only while that area is on. Resolved here, in the console's own wrapper:
  // the shared renderer below must not reach for a session, because the member
  // app mounts it without one.
  const operatingMode = useOperatingMode();
  const team = operatingMode.data?.mode === "team";
  const source = useMemo<KeysSource>(
    () =>
      member
        ? memberKeysSource
        : {
            keys: (signal) => service.keys(signal),
            discoveredModels: (signal) => service.discoveredModels(undefined, signal),
            usageSummary: (signal) => service.usageSummary(undefined, signal),
            routeOverviews: (signal) => service.routeOverviews(signal),
            routeGroups: (signal) => service.routeGroups(signal),
            // Tenant groups live behind their own endpoint; the page only needs the
            // names for its picker.
            keyGroups: (signal) =>
              service.keyGroups(signal).then((rows) => ({ groups: rows.map((row) => row.name) })),
            modelMetadata: (signal) => service.modelMetadata(signal),
            createKey: (body) => service.createKey(body),
            updateKey: (id, body) => service.updateKey(id, body),
            deleteKey: (id) => service.deleteKey(id),
            revealKey: (id) => service.revealKey(id),
            rotateKey: (id) => service.rotateKey(id),
          },
    [member, service],
  );
  return <KeysView source={source} caps={member ? MEMBER_KEY_CAPS : ADMIN_KEY_CAPS} team={team} />;
}

/**
 * The keys list, driven by an injected data source and capabilities.
 *
 * The console mounts it with the admin client and every capability on; the
 * member app mounts the very same renderer with `/me` endpoints and the
 * operator-only capabilities off, so a member sees their own keys — same
 * table, same filters, same dialogs — without the columns and actions that
 * belong to running the site.
 */
export function KeysView({
  source,
  caps,
  team = false,
  extraRowActions,
}: {
  source: KeysSource;
  caps: KeysCapabilities;
  /**
   * Whether the gateway currently serves more than one person.
   *
   * Passed in rather than read here: the team vocabulary (tenant groups, the
   * owning member) comes from the multi-user area, and this renderer is also
   * mounted by the member app — which has no console session to ask.
   */
  team?: boolean;
  /** Member-only actions appended to each row ("connect", …). */
  extraRowActions?: (key: DownstreamKey) => ActionMenuItem[];
}) {
  const { t } = useI18n();
  useCurrency();
  // Both conditions matter: the capability says this viewer may see team
  // concepts at all, the prop says the gateway is currently in that mode.
  const showTeam = Boolean(team) && caps.team;
  const [searchParams, setSearchParams] = useSearchParams();
  const query = useQuery({
    queryKey: ["keys", caps.upstream ? "admin" : "member"],
    queryFn: ({ signal }) => source.keys(signal),
  });
  const discovered = useQuery({
    queryKey: ["discovered-models", caps.upstream ? "admin" : "member"],
    queryFn: ({ signal }) => source.discoveredModels(signal),
  });
  // Upstream names reported by discovery: used only to expand wildcard
  // routes into the concrete names a client can ask for. They are NOT the
  // candidate list — the only names a token filter can match are route
  // names (see modelOptions below).
  const discoveredNames = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const model of discovered.data ?? []) {
      const name = model.model_name.trim();
      if (!name || seen.has(name)) continue;
      seen.add(name);
      out.push(name);
    }
    return out.sort((a, b) => a.localeCompare(b));
  }, [discovered.data]);
  const usage = useQuery({
    queryKey: ["usage-summary", caps.upstream ? "admin" : "member"],
    queryFn: ({ signal }) => source.usageSummary(signal),
  });
  const modelRoutes = useQuery({
    queryKey: ["route-overviews", caps.upstream ? "admin" : "member"],
    queryFn: ({ signal }) => source.routeOverviews(signal),
  });
  const routeGroups = useQuery({
    queryKey: ["route-groups", caps.upstream ? "admin" : "member"],
    queryFn: ({ signal }) => source.routeGroups(signal),
  });
  const metadata = useQuery({
    queryKey: ["model-metadata", caps.upstream ? "admin" : "member"],
    queryFn: ({ signal }) => source.modelMetadata(signal),
  });
  const tenantGroups = useQuery({
    queryKey: ["key-groups"],
    queryFn: ({ signal }) => source.keyGroups!(signal),
    enabled: showTeam && !!source.keyGroups,
  });
  const metaByModel = useMemo(() => {
    const map = new Map<string, string>();
    for (const item of metadata.data?.items ?? []) {
      map.set(item.model_name, item.vendor);
    }
    return map;
  }, [metadata.data]);
  // The allow/deny filter compares the name a client sends, and clients can
  // only send route names — so routes (with their channel bindings) are the
  // candidate set. Discovery snapshots used to feed this list instead, which
  // hid every renamed model: an alias is a route, and never appears in an
  // upstream snapshot. Wildcard routes are expanded into the concrete names
  // they answer for; a pattern with nothing to expand stays visible as-is.
  const modelOptions = useMemo<ModelOption[]>(() => {
    const out: ModelOption[] = [];
    for (const overview of modelRoutes.data ?? []) {
      const pattern = overview.route.model_pattern.trim();
      if (!pattern) continue;
      const channels = new Map<number, string>();
      for (const candidate of overview.members ?? []) {
        channels.set(candidate.member.channel_id, candidate.channel.name);
      }
      const channelList = [...channels]
        .map(([id, name]) => ({ id, name }))
        .sort((a, b) => a.id - b.id);
      const group = modelGroup(pattern, overview.route.model_group, metaByModel.get(pattern));
      const disabled = !overview.route.enabled;
      if (/[?*]/.test(pattern)) {
        const matched = discoveredNames.filter((name) => modelPatternMatches(pattern, name));
        if (matched.length === 0) {
          out.push({ name: pattern, channels: channelList, group, disabled, pattern });
        }
        for (const name of matched) {
          out.push({ name, channels: channelList, group, disabled, pattern });
        }
        continue;
      }
      out.push({
        name: pattern,
        channels: channelList,
        group,
        disabled,
        aliasOf: aliasReals(overview),
      });
    }
    return out;
  }, [discoveredNames, metaByModel, modelRoutes.data]);
  const modelGroupOptions = useMemo(() => {
    const groups = new Set<string>();
    for (const option of modelOptions) {
      groups.add(option.group?.trim() || autoModelGroup(option.name));
    }
    return [...groups].sort((a, b) => a.localeCompare(b));
  }, [modelOptions]);
  const modelsByGroup = useMemo(() => {
    const grouped = new Map<string, Set<string>>();
    for (const option of modelOptions) {
      const group = option.group?.trim() || autoModelGroup(option.name);
      const models = grouped.get(group) ?? new Set<string>();
      models.add(option.name);
      grouped.set(group, models);
    }
    return grouped;
  }, [modelOptions]);
  const [add, setAdd] = useState(false);
  const [edit, setEdit] = useState<DownstreamKey | null>(null);
  const [redemption, setRedemption] = useState(false);
  const [created, setCreated] = useState<Pick<CreatedDownstreamKey, "id" | "token"> | null>(null);
  const [remove, setRemove] = useState<number | null>(null);
  const [contextMenu, setContextMenu] = useState<{ id: number; top: number; left: number } | null>(
    null,
  );
  // Re-view a stored plaintext token (created after plaintext storage).
  const [viewing, setViewing] = useState<{ id: number; name: string } | null>(null);
  const [viewedToken, setViewedToken] = useState<string | null>(null);
  // Rotate: replace the token, old one dies instantly.
  const [rotating, setRotating] = useState<number | null>(null);
  const [rotatedToken, setRotatedToken] = useState<{
    id: number;
    name: string;
    token: string;
  } | null>(null);
  const [rotateError, setRotateError] = useState<unknown>(null);
  const openedCreateFromQuery = useRef(false);

  useEffect(() => {
    if (openedCreateFromQuery.current) return;
    if (searchParams.get("create") !== "1") return;
    openedCreateFromQuery.current = true;
    setAdd(true);
    const next = new URLSearchParams(searchParams);
    next.delete("create");
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const create = useAdminMutation({
    mutationFn: (v: KeyCreateInput) => source.createKey!(v),
    invalidateKeys: [["keys"], ["usage-summary"]],
    toastOnError: false,
    onSuccess: (result) => {
      setCreated(result);
      setAdd(false);
    },
  });
  const update = useAdminMutation({
    mutationFn: (v: { id: number; body: KeyUpdateInput }) => source.updateKey!(v.id, v.body),
    invalidateKeys: [["keys"], ["usage-summary"]],
    toastOnError: false,
    onSuccess: () => setEdit(null),
  });
  const del = useAdminMutation({
    mutationFn: (id: number) => source.deleteKey!(id),
    invalidateKeys: [["keys"], ["usage-summary"]],
    pendingIdOf: (id) => id,
    toastOnError: false,
    onSuccess: () => setRemove(null),
  });
  const reveal = useAdminMutation({
    mutationFn: (id: number) => source.revealKey!(id),
    pendingIdOf: (id) => id,
    toastOnError: false,
    onSuccess: (result) => setViewedToken(result.token),
  });
  // Copying a token is the same read the reveal dialog performs, without making
  // the reader transcribe a secret by hand: reveal, put it on the clipboard,
  // confirm in place. If the clipboard is unavailable the dialog opens instead,
  // which is the one path that still lets them see it.
  const [copiedId, setCopiedId] = useState<number | null>(null);
  const copy = useAdminMutation({
    mutationFn: (key: { id: number; name: string }) => source.revealKey!(key.id),
    pendingIdOf: (key) => key.id,
    toastOnError: false,
    onSuccess: (result, variables) => {
      const clipboard = navigator.clipboard;
      if (!clipboard?.writeText) {
        // No clipboard in this context (an insecure origin, or a browser that
        // withheld the API): show the secret instead of a button that lies.
        setViewedToken(result.token);
        setViewing({ id: variables.id, name: variables.name });
        return;
      }
      void clipboard
        .writeText(result.token)
        .then(() => {
          setCopiedId(variables.id);
          window.setTimeout(
            () => setCopiedId((current) => (current === variables.id ? null : current)),
            1800,
          );
        })
        .catch(() => {
          setViewedToken(result.token);
          setViewing({ id: variables.id, name: variables.name });
        });
    },
  });
  const rotate = useAdminMutation({
    mutationFn: (v: { id: number; name: string }) => source.rotateKey!(v.id),
    invalidateKeys: [["keys"]],
    toastOnError: false,
    onSuccess: (result, variables) => {
      setRotatedToken({
        id: result.id,
        name: variables.name,
        token: result.token,
      });
      setRotating(null);
      setRotateError(null);
    },
    onError: (err) => setRotateError(err),
  });

  const searchTerm = searchParams.get("search")?.trim().toLowerCase() ?? "";
  const rows = useMemo(() => {
    const list = query.data ?? [];
    if (!searchTerm) return list;
    return list.filter(
      (key) => key.name.toLowerCase().includes(searchTerm) || String(key.id).includes(searchTerm),
    );
  }, [query.data, searchTerm]);
  const pagination = useClientPagination(rows, 12);
  const pageRows = pagination.pageItems;
  const enabledCount = useMemo(() => rows.filter((key) => key.enabled).length, [rows]);
  const totalUsed = useMemo(
    () => rows.reduce((sum, key) => sum + (key.quota_used_tokens ?? 0), 0),
    [rows],
  );

  const openCreate = () => {
    create.reset();
    setAdd(true);
  };

  const viewKey = (key: DownstreamKey) => {
    reveal.reset();
    setViewedToken(null);
    setViewing({ id: key.id, name: key.name });
    reveal.mutate(key.id);
  };
  const copyKey = (key: DownstreamKey) => {
    copy.reset();
    copy.mutate({ id: key.id, name: key.name });
  };
  const keyActions = (key: DownstreamKey): ActionMenuItem[] => {
    const busy =
      reveal.pendingId === key.id ||
      del.pendingId === key.id ||
      (rotate.isPending && rotate.variables?.id === key.id) ||
      (update.isPending && update.variables?.id === key.id);
    const actions: ActionMenuItem[] = [];
    // A capability that is off removes the item instead of disabling it: an
    // action a member can never perform should not appear at all.
    if (caps.reveal && (key.has_token || key.id === rotatedToken?.id))
      actions.push({
        key: "view",
        label: t("keys.view"),
        group: t("actions.view"),
        icon: <Eye size={14} />,
        disabled: busy,
        onSelect: () => viewKey(key),
      });
    if (caps.edit) {
      actions.push({
        key: "edit",
        label: t("keys.edit"),
        group: t("actions.manage"),
        icon: <Pencil size={14} />,
        disabled: busy,
        onSelect: () => {
          update.reset();
          setEdit(key);
        },
      });
    }
    // Pausing a token is not the same as revoking it: the client keeps its
    // credentials and starts working again the moment the switch goes back on.
    if (caps.edit) {
      actions.push({
        key: key.enabled ? "disable" : "enable",
        label: key.enabled ? t("common.disableAction") : t("common.enableAction"),
        group: t("actions.manage"),
        icon: key.enabled ? <Ban size={14} /> : <Check size={14} />,
        disabled: busy,
        onSelect: () => update.mutate({ id: key.id, body: { enabled: !key.enabled } }),
      });
    }
    if (caps.rotate) {
      actions.push({
        key: "rotate",
        label: t("keys.rotate"),
        group: t("actions.danger"),
        danger: true,
        icon: <RefreshCw size={14} />,
        disabled: busy,
        onSelect: () => {
          rotate.reset();
          setRotateError(null);
          setRotating(key.id);
        },
      });
    }
    if (caps.remove) {
      actions.push({
        key: "delete",
        label: t("keys.delete"),
        group: t("actions.danger"),
        danger: true,
        icon: <Trash2 size={14} />,
        disabled: busy,
        onSelect: () => setRemove(key.id),
      });
    }
    // Member-only affordances (the connect sheet) arrive through the source's
    // own slot, so the same row can offer more without a second table.
    if (extraRowActions) actions.push(...extraRowActions(key));
    return actions.map((action) => ({
      ...action,
      disabledReason: action.disabled ? t("common.working") : undefined,
    }));
  };
  const contextKey = contextMenu ? rows.find((key) => key.id === contextMenu.id) : undefined;

  return (
    <Page
      title={t("keys.title")}
      description={t("keys.description")}
      actions={
        <>
          <label className="directory-search">
            <Search size={14} aria-hidden="true" />
            <input
              value={searchParams.get("search") ?? ""}
              onChange={(event) => {
                const next = new URLSearchParams(searchParams);
                const value = event.target.value;
                if (value) next.set("search", value);
                else next.delete("search");
                setSearchParams(next, { replace: true });
              }}
              placeholder={t("keys.searchPlaceholder")}
              aria-label={t("keys.searchPlaceholder")}
            />
          </label>
          {caps.create ? (
            <Button icon={<Plus size={16} />} onClick={openCreate}>
              {t("keys.create")}
            </Button>
          ) : null}
          {/* Credit codes are an operator concept in the console; the member
              app redeems inside its own settings page instead. */}
          {caps.quotas ? (
            <Button
              variant="secondary"
              icon={<Ticket size={15} />}
              onClick={() => setRedemption(true)}
            >
              {t("keys.redemption")}
            </Button>
          ) : null}
        </>
      }
    >
      <div className="ops-canvas">
        <TelemetryStrip
          items={[
            {
              label: t("keys.stat.total"),
              value: query.isPending ? "—" : rows.length,
              tone: "primary",
            },
            {
              label: t("keys.stat.enabled"),
              value: query.isPending ? "—" : enabledCount,
              tone: "success",
            },
            {
              label: t("keys.stat.usedTokens"),
              value:
                query.isPending || usage.isPending || usage.isError
                  ? "—"
                  : formatNumber(usage.data?.total_tokens ?? totalUsed),
              tone: "info",
            },
            {
              label: t("keys.stat.requests"),
              value:
                usage.isPending || usage.isError
                  ? "—"
                  : formatNumber(usage.data?.request_count ?? 0),
              tone: "warning",
            },
          ]}
        />
        {usage.isError ? <ErrorState error={usage.error} retry={() => usage.refetch()} /> : null}

        <Panel className="ops-list-panel">
          <EntityState
            isLoading={query.isPending}
            isError={query.isError}
            error={query.error}
            isEmpty={!rows.length}
            empty={
              <EmptyHero
                kicker={t("keys.emptyKicker")}
                title={t("keys.emptyTitle")}
                body={t("keys.empty")}
                actions={
                  <>
                    {caps.create ? (
                      <Button icon={<Plus size={16} />} onClick={openCreate}>
                        {t("keys.create")}
                      </Button>
                    ) : null}
                    {caps.upstream ? (
                      <Link className="button button-secondary" to="/channels">
                        {t("keys.ctaConnections")}
                      </Link>
                    ) : null}
                  </>
                }
              />
            }
            retry={() => query.refetch()}
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
              {caps.cards ? (
                // A person's own tokens: few, and each one read on its own. The
                // same rows and the same actions feed both layouts, so the card
                // and the table can never drift apart.
                <div className="key-card-grid">
                  {pageRows.map((k) => (
                    <KeyCard
                      key={k.id}
                      name={k.name}
                      id={k.id}
                      hint={k.token_hint}
                      usedTokens={k.quota_used_tokens ?? 0}
                      cost={k.cost ?? 0}
                      lastUsedAt={k.last_used_at}
                      modelScope={k.model_allowlist}
                      expiresAt={k.expires_at}
                      allowedIPs={k.allowed_ips}
                      busy={
                        reveal.pendingId === k.id ||
                        (update.isPending && update.variables?.id === k.id)
                      }
                      copied={copiedId === k.id}
                      copyPending={copy.pendingId === k.id}
                      onCopy={caps.reveal ? () => copyKey(k) : undefined}
                      toggle={
                        caps.edit ? (
                          <EnabledSwitch
                            on={k.enabled}
                            name={k.name}
                            pending={update.isPending && update.variables?.id === k.id}
                            onToggle={() =>
                              update.mutate({ id: k.id, body: { enabled: !k.enabled } })
                            }
                          />
                        ) : (
                          <StatusBadge value={k.enabled} />
                        )
                      }
                      actions={keyActions(k)}
                    />
                  ))}
                </div>
              ) : (
                <DataTable
                  headers={[
                    t("common.name"),
                    ...(caps.scopes ? [t("keys.accessCol")] : []),
                    ...(caps.quotas ? [t("keys.quotaCol")] : []),
                    ...(caps.pricing ? [t("keys.costCol")] : []),
                    t("common.status"),
                    t("common.created"),
                    t("common.actions"),
                  ]}
                >
                  {pageRows.map((k) => (
                    <tr
                      key={k.id}
                      tabIndex={0}
                      onContextMenu={(event) => {
                        const point = rowContextPoint(event);
                        if (point) setContextMenu({ id: k.id, ...point });
                      }}
                      onKeyDown={(event) => {
                        const point = rowKeyboardContextPoint(event);
                        if (point) setContextMenu({ id: k.id, ...point });
                      }}
                    >
                      <td>
                        <strong>{k.name}</strong>
                        <small>#{k.id}</small>
                        {showTeam && k.user_id ? (
                          <small>{t("keys.userOwner", { id: k.user_id })}</small>
                        ) : null}
                      </td>
                      {caps.scopes ? <td>{k.scopes?.trim() || "relay"}</td> : null}
                      {caps.quotas ? (
                        <td>
                          <div className="quota-cell">
                            <code>{formatQuota(k.quota_used_tokens, k.quota_total_tokens)}</code>
                            {k.quota_total_tokens && k.quota_total_tokens > 0 ? (
                              <QuotaBar
                                percent={quotaPercent(
                                  k.quota_used_tokens ?? 0,
                                  k.quota_total_tokens,
                                )}
                              />
                            ) : null}
                            {/* The money budget appears only when it is set: an unlimited key
                would otherwise grow a meaningless "0 / 0" line. */}
                            {(k.quota_total_cost ?? 0) > 0 ? (
                              <small>
                                {formatCost(k.quota_used_cost ?? 0)} /{" "}
                                {formatCost(k.quota_total_cost ?? 0)}
                              </small>
                            ) : null}
                          </div>
                        </td>
                      ) : null}
                      {caps.pricing ? <td>{formatCost(k.cost)}</td> : null}
                      <td>
                        {caps.edit ? (
                          <EnabledSwitch
                            on={k.enabled}
                            name={k.name}
                            pending={update.isPending && update.variables?.id === k.id}
                            onToggle={() =>
                              update.mutate({ id: k.id, body: { enabled: !k.enabled } })
                            }
                          />
                        ) : (
                          <StatusBadge value={k.enabled} />
                        )}
                      </td>
                      <td>{formatDate(k.created_at)}</td>
                      <td className="actions key-row-actions">
                        {caps.reveal && (k.has_token || k.id === rotatedToken?.id) && (
                          <IconButton
                            className="is-bare"
                            label={t("keys.view")}
                            disabled={reveal.pendingId === k.id}
                            onClick={() => viewKey(k)}
                          >
                            <Eye size={14} />
                          </IconButton>
                        )}
                        <ActionMenu
                          compact
                          label={t("common.moreActions")}
                          title={k.name}
                          items={keyActions(k)}
                        />
                      </td>
                    </tr>
                  ))}
                </DataTable>
              )}
            </ListShell>
          </EntityState>
        </Panel>
      </div>

      {contextMenu && contextKey ? (
        <ActionMenu
          key={contextKey.id}
          label={t("common.moreActions")}
          title={contextKey.name}
          open
          position={contextMenu}
          onOpenChange={(open) => {
            if (!open) setContextMenu(null);
          }}
          items={keyActions(contextKey)}
        />
      ) : null}
      {caps.quotas && redemption && <RedemptionDialog onClose={() => setRedemption(false)} />}
      {add && (
        <KeyDialog
          mode="create"
          pending={create.isPending}
          error={create.error}
          onClose={() => setAdd(false)}
          onSave={(v) => create.mutate(v)}
          modelOptions={modelOptions}
          modelGroupOptions={modelGroupOptions}
          modelsByGroup={modelsByGroup}
          routeGroupNames={caps.upstream ? (routeGroups.data?.groups ?? []) : []}
          tenantGroupNames={showTeam ? (tenantGroups.data?.groups ?? []) : []}
        />
      )}
      {edit && (
        <KeyDialog
          mode="edit"
          initial={edit}
          pending={update.isPending}
          error={update.error}
          onClose={() => setEdit(null)}
          onSave={(v) =>
            update.mutate({
              id: edit.id,
              body: {
                // Every field the dialog can change has to travel: an omitted
                // field means "keep the stored value" (the backend reads
                // pointers), so dropping one here silently turns the input into
                // a no-op. quota_total_cost and route_group_name were both
                // collected but never sent, which is why picking a route group
                // looked like it saved and did not.
                name: v.name,
                scopes: v.scopes,
                quota_total_tokens: v.quota_total_tokens,
                quota_total_cost: v.quota_total_cost,
                model_allowlist: v.model_allowlist,
                model_denylist: v.model_denylist,
                expires_at: v.expires_at ?? "",
                allowed_ips: v.allowed_ips ?? "",
                // Always sent, empty included: "" is how a key is moved back to
                // "no route group", and `|| undefined` would omit it instead.
                route_group_name: v.route_group_name ?? "",
                group_name: v.group_name ?? "",
                reset_used: v.reset_used,
              },
            })
          }
          modelOptions={modelOptions}
          modelGroupOptions={modelGroupOptions}
          modelsByGroup={modelsByGroup}
          routeGroupNames={caps.upstream ? (routeGroups.data?.groups ?? []) : []}
          tenantGroupNames={showTeam ? (tenantGroups.data?.groups ?? []) : []}
        />
      )}
      {created && (
        <Dialog
          title={t("keys.copyTitle")}
          onClose={() => setCreated(null)}
          actions={<Button onClick={() => setCreated(null)}>{t("keys.stored")}</Button>}
        >
          <p className="warning">{t("keys.copyWarning")}</p>
          <div className="secret-output">
            <code>{created.token}</code>
            <IconButton
              label={t("keys.copyToken")}
              onClick={() => navigator.clipboard.writeText(created.token)}
            >
              <Copy size={14} />
            </IconButton>
          </div>
        </Dialog>
      )}
      {remove && (
        <ConfirmDialog
          title={t("keys.revoke")}
          message={t("keys.revokeMsg")}
          confirmLabel={t("keys.revokeConfirm")}
          pending={del.isPending}
          error={del.error}
          onClose={() => setRemove(null)}
          onConfirm={() => del.mutate(remove)}
        />
      )}
      {viewing && (
        <SecretRevealDialog
          title={t("keys.viewTitle", { name: viewing.name })}
          warning={t("keys.viewWarning")}
          secret={viewedToken}
          pending={reveal.isPending}
          error={reveal.error}
          onRetry={() => reveal.mutate(viewing.id)}
          closeLabel={t("common.close")}
          copyLabel={t("keys.copyToken")}
          onClose={() => setViewing(null)}
        />
      )}
      {rotating != null && (
        <ConfirmDialog
          title={t("keys.rotateTitle")}
          message={t("keys.rotateConfirmMsg")}
          confirmLabel={t("keys.rotateConfirm")}
          pending={rotate.isPending}
          error={rotateError}
          onClose={() => {
            if (!rotate.isPending) setRotating(null);
          }}
          onConfirm={() =>
            rotate.mutate({
              id: rotating,
              name: rows.find((k) => k.id === rotating)?.name ?? "",
            })
          }
        />
      )}
      {rotatedToken && (
        <SecretRevealDialog
          title={t("keys.rotatedTitle")}
          warning={t("keys.rotatedWarning")}
          secret={rotatedToken.token}
          closeLabel={t("keys.stored")}
          copyLabel={t("keys.copyToken")}
          onClose={() => setRotatedToken(null)}
        />
      )}
    </Page>
  );
}

type KeyFormValues = KeyCreateInput & {
  reset_used?: boolean;
};

function KeyDialog({
  mode,
  initial,
  pending,
  error,
  onClose,
  onSave,
  modelOptions,
  modelGroupOptions,
  modelsByGroup,
  routeGroupNames,
  tenantGroupNames = [],
}: {
  mode: "create" | "edit";
  initial?: DownstreamKey;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (v: KeyFormValues) => void;
  modelOptions: ModelOption[];
  modelGroupOptions: string[];
  modelsByGroup: Map<string, Set<string>>;
  routeGroupNames: string[];
  /** Tenant groups a token can be bound to; empty hides the field entirely. */
  tenantGroupNames?: string[];
}) {
  const { t } = useI18n();
  const [name, setName] = useState(initial?.name ?? "");
  const [modelGroupSelection, setModelGroupSelection] = useState("");
  const [customToken, setCustomToken] = useState("");
  const [useCustomToken, setUseCustomToken] = useState(false);
  const [scopes, setScopes] = useState<string[]>(() =>
    (initial?.scopes?.trim() || "relay")
      .split(/[,;|\s]+/)
      .map((entry) => entry.trim())
      .filter(Boolean),
  );
  const [quotaTotal, setQuotaTotal] = useState(
    String(
      initial?.quota_total_tokens && initial.quota_total_tokens > 0
        ? initial.quota_total_tokens
        : "",
    ),
  );
  // The spend budget is a SECOND allowance, in the ledger's unit: an operator
  // who sells "100 dollars of usage" caps the key in money, and the relay
  // refuses on whichever budget runs out first.
  const [quotaCost, setQuotaCost] = useState(
    String(
      initial?.quota_total_cost && initial.quota_total_cost > 0 ? initial.quota_total_cost : "",
    ),
  );
  const [tokenInputBad, setTokenInputBad] = useState(false);
  const [costInputBad, setCostInputBad] = useState(false);
  const splitModels = (raw?: string) =>
    (raw ?? "")
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
  const [allowlist, setAllowlist] = useState<string[]>(() => splitModels(initial?.model_allowlist));
  const [denylist, setDenylist] = useState<string[]>(() => splitModels(initial?.model_denylist));
  const [expiresAt, setExpiresAt] = useState(initial?.expires_at ?? "");
  const [allowedIPs, setAllowedIPs] = useState(initial?.allowed_ips ?? "");
  const [routeGroup, setRouteGroup] = useState(initial?.route_group_name ?? "");
  const [tenantGroup, setTenantGroup] = useState(initial?.group_name ?? "");
  const [resetUsed, setResetUsed] = useState(false);
  // Progressive disclosure: billing, model scoping and advanced controls are
  // folded sections so the common path (name + scopes) stays two steps.
  const [openBilling, setOpenBilling] = useState(false);
  const [openModels, setOpenModels] = useState(false);
  const [openAdvanced, setOpenAdvanced] = useState(false);
  // Pre-open a section when the stored value is non-trivial (edit mode).
  useEffect(() => {
    if (mode !== "edit") return;
    if ((initial?.quota_total_tokens ?? 0) > 0 || (initial?.quota_total_cost ?? 0) > 0) {
      setOpenBilling(true);
    }
    if ((initial?.model_allowlist ?? "").trim() || (initial?.model_denylist ?? "").trim()) {
      setOpenModels(true);
    }
    if (
      (initial?.expires_at ?? "").trim() ||
      (initial?.allowed_ips ?? "").trim() ||
      (initial?.route_group_name ?? "").trim() ||
      (initial?.group_name ?? "").trim()
    ) {
      setOpenAdvanced(true);
    }
  }, [mode, initial]);
  const addModelGroup = (group: string) => {
    setModelGroupSelection(group);
    if (!group) return;
    const additions = [...(modelsByGroup.get(group) ?? [])];
    setAllowlist((current) => [
      ...current,
      ...additions.filter((model) => !current.includes(model)),
    ]);
  };
  const trimmedCustom = customToken.trim();
  const customTooShort = useCustomToken && trimmedCustom.length > 0 && trimmedCustom.length < 16;
  const tokenQuota = parseQuotaInput(quotaTotal, true);
  const costQuota = parseQuotaInput(quotaCost);
  const quotasValid = tokenQuota !== null && costQuota !== null && !tokenInputBad && !costInputBad;
  const canSubmit =
    Boolean(name.trim()) &&
    quotasValid &&
    (mode === "edit" || !useCustomToken || (trimmedCustom.length >= 16 && !customTooShort));

  return (
    <Dialog
      title={mode === "create" ? t("keys.createDialog") : t("keys.editDialog")}
      onClose={onClose}
      busy={pending}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={pending || !canSubmit}
            icon={<KeyRound size={16} />}
            onClick={() => {
              if (!canSubmit || tokenQuota === null || costQuota === null) return;
              onSave({
                name: name.trim(),
                scopes: scopes.length > 0 ? scopes.join(",") : "relay",
                token: mode === "create" && useCustomToken ? trimmedCustom : undefined,
                quota_total_tokens: tokenQuota,
                quota_total_cost: costQuota,
                model_allowlist: allowlist.join(","),
                model_denylist: denylist.join(","),
                expires_at: expiresAt.trim() || undefined,
                allowed_ips: allowedIPs.trim() || undefined,
                route_group_name: routeGroup.trim() || undefined,
                group_name: tenantGroup.trim() || undefined,
                reset_used: mode === "edit" ? resetUsed : undefined,
              });
            }}
          >
            {mode === "create" ? t("common.create") : t("common.save")}
          </Button>
        </>
      }
    >
      <div className="ops-panel-context">
        <span>{mode === "create" ? t("keys.createHint") : t("keys.editHint")}</span>
      </div>
      <Field label={t("common.name")}>
        <input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t("keys.namePlaceholder")}
        />
      </Field>
      <Field label={t("common.scopes")} hint={t("keys.scopesHint")}>
        <ScopePicker value={scopes} onChange={setScopes} disabled={pending} />
      </Field>

      <div className="key-dialog-section">
        <button
          type="button"
          className="key-dialog-fold"
          aria-expanded={openBilling}
          onClick={() => setOpenBilling((v) => !v)}
        >
          <ChevronDown size={13} className={openBilling ? "is-open" : ""} />
          <span>{t("keys.sectionBilling")}</span>
          {openBilling ? null : <small>{t("keys.sectionBillingHint")}</small>}
        </button>
        {openBilling ? (
          <div className="key-dialog-fold-body">
            <Field label={t("keys.quotaTotal")} hint={t("keys.quotaTotalHint")}>
              <input
                type="number"
                min={0}
                step={1}
                value={quotaTotal}
                onChange={(e) => {
                  setQuotaTotal(e.target.value);
                  setTokenInputBad(e.target.validity.badInput);
                }}
                aria-invalid={tokenQuota === null || tokenInputBad}
                placeholder={t("keys.unlimitedPlaceholder")}
              />
            </Field>
            <Field label={t("keys.quotaCost")} hint={t("keys.quotaCostHint")}>
              <input
                type="number"
                min={0}
                step="0.01"
                value={quotaCost}
                onChange={(e) => {
                  setQuotaCost(e.target.value);
                  setCostInputBad(e.target.validity.badInput);
                }}
                aria-invalid={costQuota === null || costInputBad}
                placeholder={t("keys.unlimitedPlaceholder")}
              />
            </Field>
          </div>
        ) : null}
        {!quotasValid ? (
          <p className="inline-error" role="alert">
            {t("keys.invalidQuota")}
          </p>
        ) : null}
      </div>

      <div className="key-dialog-section">
        <button
          type="button"
          className="key-dialog-fold"
          aria-expanded={openModels}
          onClick={() => setOpenModels((v) => !v)}
        >
          <ChevronDown size={13} className={openModels ? "is-open" : ""} />
          <span>{t("keys.sectionModels")}</span>
          {openModels ? null : <small>{t("keys.sectionModelsHint")}</small>}
        </button>
        {openModels ? (
          <div className="key-dialog-fold-body">
            <Field label={t("keys.modelAllowlist")} hint={t("keys.modelAllowlistHint")}>
              <div className="model-group-picker">
                <select
                  value={modelGroupSelection}
                  onChange={(event) => addModelGroup(event.target.value)}
                  disabled={pending}
                >
                  <option value="">{t("keys.modelGroupPlaceholder")}</option>
                  {modelGroupOptions.map((group) => (
                    <option key={group} value={group}>
                      {group} ({modelsByGroup.get(group)?.size ?? 0})
                    </option>
                  ))}
                </select>
                <span className="field-hint">{t("keys.modelGroupHint")}</span>
              </div>
              <ModelPicker
                options={modelOptions}
                selected={allowlist}
                onChange={setAllowlist}
                placeholder={t("modelPicker.search")}
                emptyLabel={t("keys.modelPickerEmpty")}
                flagMissing
              />
            </Field>
            <Field label={t("keys.modelDenylist")} hint={t("keys.modelDenylistHint")}>
              <ModelPicker
                options={modelOptions}
                selected={denylist}
                onChange={setDenylist}
                placeholder={t("modelPicker.search")}
                emptyLabel={t("keys.modelPickerEmpty")}
                flagMissing
              />
            </Field>
          </div>
        ) : null}
      </div>

      <div className="key-dialog-section">
        <button
          type="button"
          className="key-dialog-fold"
          aria-expanded={openAdvanced}
          onClick={() => setOpenAdvanced((v) => !v)}
        >
          <ChevronDown size={13} className={openAdvanced ? "is-open" : ""} />
          <span>{t("keys.sectionAdvanced")}</span>
          {openAdvanced ? null : <small>{t("keys.sectionAdvancedHint")}</small>}
        </button>
        {openAdvanced ? (
          <div className="key-dialog-fold-body">
            {tenantGroupNames.length > 0 || tenantGroup ? (
              <Field label={t("keys.group")} hint={t("keys.groupHint")}>
                <select
                  value={tenantGroup}
                  disabled={pending}
                  onChange={(e) => setTenantGroup(e.target.value)}
                >
                  <option value="">{t("keys.groupDefault")}</option>
                  {tenantGroupNames.map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </select>
              </Field>
            ) : null}
            <Field label={t("keys.routeGroup")} hint={t("keys.routeGroupHint")}>
              <select
                value={routeGroup}
                disabled={pending}
                onChange={(e) => setRouteGroup(e.target.value)}
              >
                <option value="">{t("keys.routeGroupNone")}</option>
                {routeGroup && !routeGroupNames.includes(routeGroup) ? (
                  <option value={routeGroup}>{routeGroup}</option>
                ) : null}
                {routeGroupNames.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            </Field>
            <div className="split is-tight">
              <Field label={t("keys.expiresAt")} hint={t("keys.expiresAtHint")}>
                <input
                  type="datetime-local"
                  value={expiresAt ? toLocalInput(expiresAt) : ""}
                  disabled={pending}
                  onChange={(e) => setExpiresAt(e.target.value ? toRFC3339(e.target.value) : "")}
                />
              </Field>
              <Field label={t("keys.allowedIPs")} hint={t("keys.allowedIPsHint")}>
                <textarea
                  value={allowedIPs}
                  disabled={pending}
                  onChange={(e) => setAllowedIPs(e.target.value)}
                  placeholder="1.2.3.4&#10;10.0.0.0/8"
                  className="textarea-sm"
                />
              </Field>
            </div>
            {mode === "edit" ? (
              <label className="check is-spaced">
                <input
                  type="checkbox"
                  checked={resetUsed}
                  disabled={pending}
                  onChange={(event) => setResetUsed(event.target.checked)}
                />
                <span>{t("keys.resetUsed")}</span>
              </label>
            ) : (
              <>
                <label className="check is-spaced">
                  <input
                    type="checkbox"
                    checked={useCustomToken}
                    disabled={pending}
                    aria-label={t("keys.useCustomToken")}
                    onChange={(event) => {
                      setUseCustomToken(event.target.checked);
                      if (!event.target.checked) setCustomToken("");
                    }}
                  />
                  <span>{t("keys.useCustomToken")}</span>
                </label>
                {useCustomToken ? (
                  <Field label={t("keys.customToken")} hint={t("keys.customTokenHint")}>
                    <input
                      type="password"
                      autoComplete="new-password"
                      aria-label={t("keys.customToken")}
                      value={customToken}
                      onChange={(e) => setCustomToken(e.target.value)}
                      placeholder={t("keys.customTokenPlaceholder")}
                      disabled={pending}
                    />
                  </Field>
                ) : (
                  <p className="exchange-panel-note">{t("keys.autoTokenHint")}</p>
                )}
              </>
            )}
          </div>
        ) : null}
      </div>
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}

/** Converts an RFC3339 string to a local datetime-local input value. */
function toLocalInput(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** Converts a datetime-local input value to RFC3339 (local time). */
function toRFC3339(local: string): string {
  const date = new Date(local);
  if (Number.isNaN(date.getTime())) return "";
  return date.toISOString();
}
