import {
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
import type { CreatedDownstreamKey, DownstreamKey } from "../api/types";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { ModelPicker } from "../components/ModelPicker";
import { ScopePicker } from "../components/ScopePicker";
import { modelGroup, modelPatternMatches } from "./models/modelGroups";
import { PaginationBar } from "../components/PaginationBar";
import { SecretRevealDialog } from "../components/SecretRevealDialog";
import { EntityState } from "../components/EntityState";
import { StatGrid } from "../components/StatGrid";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
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
    mutationFn: () =>
      service.createRedemptionCodes({ count, quota_tokens: quota }),
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
    <Dialog title={t("keys.redemptionTitle")} onClose={onClose}>
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
              <code className={c.redeemed_by_key_id ? "is-used" : ""}>
                {c.code}
              </code>
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
                  {voidCode.pendingId === c.id
                    ? t("common.working")
                    : t("keys.redemptionVoid")}
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
  if (value == null || value <= 0) return "—";
  return value.toFixed(4);
}

export function Keys() {
  const { client } = useSession();
  const { t } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const service = api(client!);
  const query = useQuery({
    queryKey: ["keys"],
    queryFn: ({ signal }) => service.keys(signal),
  });
  const discovered = useQuery({
    queryKey: ["discovered-models"],
    queryFn: ({ signal }) => service.discoveredModels(undefined, signal),
  });
  const allModels = useMemo(() => {
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
    queryKey: ["usage-summary"],
    queryFn: ({ signal }) => service.usageSummary(undefined, signal),
  });
  const modelRoutes = useQuery({
    queryKey: ["route-overviews"],
    queryFn: ({ signal }) => service.routeOverviews(signal),
  });
  const metadata = useQuery({
    queryKey: ["model-metadata"],
    queryFn: ({ signal }) => service.modelMetadata(signal),
  });
  const metaByModel = useMemo(() => {
    const map = new Map<string, string>();
    for (const item of metadata.data?.items ?? []) {
      map.set(item.model_name, item.vendor);
    }
    return map;
  }, [metadata.data]);
  const modelGroupOptions = useMemo(() => {
    const groups = new Set<string>();
    for (const overview of modelRoutes.data ?? []) {
      groups.add(
        modelGroup(
          overview.route.model_pattern,
          overview.route.model_group,
          metaByModel.get(overview.route.model_pattern),
        ),
      );
    }
    return [...groups].sort((a, b) => a.localeCompare(b));
  }, [metaByModel, modelRoutes.data]);
  const modelsByGroup = useMemo(() => {
    const grouped = new Map<string, Set<string>>();
    for (const overview of modelRoutes.data ?? []) {
      const group = modelGroup(
        overview.route.model_pattern,
        overview.route.model_group,
        metaByModel.get(overview.route.model_pattern),
      );
      const models = grouped.get(group) ?? new Set<string>();
      const pattern = overview.route.model_pattern.trim();
      if (pattern && !/[?*]/.test(pattern)) {
        models.add(pattern);
      }
      for (const model of allModels) {
        if (modelPatternMatches(pattern, model)) {
          models.add(model);
        }
      }
      grouped.set(group, models);
    }
    return grouped;
  }, [allModels, metaByModel, modelRoutes.data]);
  const [add, setAdd] = useState(false);
  const [edit, setEdit] = useState<DownstreamKey | null>(null);
  const [redemption, setRedemption] = useState(false);
  const [created, setCreated] = useState<CreatedDownstreamKey | null>(null);
  const [remove, setRemove] = useState<number | null>(null);
  // Re-view a stored plaintext token (created after plaintext storage).
  const [viewing, setViewing] = useState<{ id: number; name: string } | null>(
    null,
  );
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
    mutationFn: (v: {
      name: string;
      scopes?: string;
      token?: string;
      quota_total_tokens?: number;
      price_prompt_per_1k?: number;
      price_completion_per_1k?: number;
      price_cache_per_1k?: number;
      model_allowlist?: string;
      model_denylist?: string;
      expires_at?: string;
      allowed_ips?: string;
    }) => service.createKey(v),
    invalidateKeys: [["keys"], ["usage-summary"]],
    toastOnError: false,
    onSuccess: (result) => {
      setCreated(result);
      setAdd(false);
    },
  });
  const update = useAdminMutation({
    mutationFn: (v: {
      id: number;
      body: {
        name?: string;
        enabled?: boolean;
        scopes?: string;
        quota_total_tokens?: number;
        price_prompt_per_1k?: number;
        price_completion_per_1k?: number;
        price_cache_per_1k?: number;
        model_allowlist?: string;
        model_denylist?: string;
        expires_at?: string;
        allowed_ips?: string;
        reset_used?: boolean;
      };
    }) => service.updateKey(v.id, v.body),
    invalidateKeys: [["keys"], ["usage-summary"]],
    toastOnError: false,
    onSuccess: () => setEdit(null),
  });
  const del = useAdminMutation({
    mutationFn: (id: number) => service.deleteKey(id),
    invalidateKeys: [["keys"], ["usage-summary"]],
    pendingIdOf: (id) => id,
    toastOnError: false,
    onSuccess: () => setRemove(null),
  });
  const reveal = useAdminMutation({
    mutationFn: (id: number) => service.revealKey(id),
    pendingIdOf: (id) => id,
    toastOnError: false,
    onSuccess: (result) => setViewedToken(result.token),
  });
  const rotate = useAdminMutation({
    mutationFn: (v: { id: number; name: string }) => service.rotateKey(v.id),
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
      (key) =>
        key.name.toLowerCase().includes(searchTerm) ||
        String(key.id).includes(searchTerm),
    );
  }, [query.data, searchTerm]);
  const pagination = useClientPagination(rows, 12);
  const pageRows = pagination.pageItems;
  const enabledCount = useMemo(
    () => rows.filter((key) => key.enabled).length,
    [rows],
  );
  const totalUsed = useMemo(
    () => rows.reduce((sum, key) => sum + (key.quota_used_tokens ?? 0), 0),
    [rows],
  );

  const openCreate = () => {
    create.reset();
    setAdd(true);
  };

  return (
    <Page
      kicker={t("keys.kicker")}
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
          <Button icon={<Plus size={16} />} onClick={openCreate}>
            {t("keys.create")}
          </Button>
          <Button
            variant="secondary"
            icon={<Ticket size={15} />}
            onClick={() => setRedemption(true)}
          >
            {t("keys.redemption")}
          </Button>
        </>
      }
    >
      <div className="ops-canvas">
        <StatGrid
          items={[
            {
              label: t("keys.stat.total"),
              value: query.isPending ? "—" : rows.length,
            },
            {
              label: t("keys.stat.enabled"),
              value: query.isPending ? "—" : enabledCount,
            },
            {
              label: t("keys.stat.usedTokens"),
              value: query.isPending
                ? "—"
                : formatNumber(usage.data?.total_tokens ?? totalUsed),
            },
            {
              label: t("keys.stat.requests"),
              value: usage.isPending
                ? "—"
                : formatNumber(usage.data?.request_count ?? 0),
            },
          ]}
        />

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
                    <Button icon={<Plus size={16} />} onClick={openCreate}>
                      {t("keys.create")}
                    </Button>
                    <Link className="button button-secondary" to="/channels">
                      {t("keys.ctaConnections")}
                    </Link>
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
              <DataTable
                headers={[
                  t("common.name"),
                  t("keys.accessCol"),
                  t("keys.quotaCol"),
                  t("keys.costCol"),
                  t("common.status"),
                  t("common.created"),
                  t("common.actions"),
                ]}
              >
                {pageRows.map((k) => (
                  <tr key={k.id}>
                    <td>
                      <strong>{k.name}</strong>
                      <small>#{k.id}</small>
                    </td>
                    <td>{k.scopes?.trim() || "relay"}</td>
                    <td>
                      <code>
                        {formatQuota(k.quota_used_tokens, k.quota_total_tokens)}
                      </code>
                    </td>
                    <td>{formatCost(k.estimated_cost)}</td>
                    <td>
                      <StatusBadge value={k.enabled} />
                    </td>
                    <td>{formatDate(k.created_at)}</td>
                    <td className="actions">
                      {(k.has_token || k.id === rotatedToken?.id) && (
                        <IconButton
                          className="is-bare"
                          label={t("keys.view")}
                          disabled={reveal.pendingId === k.id}
                          onClick={() => {
                            reveal.reset();
                            setViewedToken(null);
                            setViewing({ id: k.id, name: k.name });
                            reveal.mutate(k.id);
                          }}
                        >
                          <Eye size={14} />
                        </IconButton>
                      )}
                      <IconButton
                        className="is-bare"
                        label={t("keys.rotate")}
                        disabled={rotating === k.id || rotate.isPending}
                        onClick={() => {
                          rotate.reset();
                          setRotateError(null);
                          setRotating(k.id);
                        }}
                      >
                        <RefreshCw size={14} />
                      </IconButton>
                      <IconButton
                        className="is-bare"
                        label={t("keys.edit")}
                        onClick={() => {
                          update.reset();
                          setEdit(k);
                        }}
                      >
                        <Pencil size={14} />
                      </IconButton>
                      <IconButton
                        className="is-bare"
                        label={t("keys.delete")}
                        disabled={del.pendingId === k.id}
                        onClick={() => setRemove(k.id)}
                      >
                        <Trash2 size={14} />
                      </IconButton>
                    </td>
                  </tr>
                ))}
              </DataTable>
            </ListShell>
          </EntityState>
        </Panel>
      </div>

      {redemption && <RedemptionDialog onClose={() => setRedemption(false)} />}
      {add && (
        <KeyDialog
          mode="create"
          pending={create.isPending}
          error={create.error}
          onClose={() => setAdd(false)}
          onSave={(v) => create.mutate(v)}
          allModels={allModels}
          modelGroupOptions={modelGroupOptions}
          modelsByGroup={modelsByGroup}
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
                name: v.name,
                scopes: v.scopes,
                quota_total_tokens: v.quota_total_tokens,
                price_prompt_per_1k: v.price_prompt_per_1k,
                price_completion_per_1k: v.price_completion_per_1k,
                price_cache_per_1k: v.price_cache_per_1k,
                model_allowlist: v.model_allowlist,
                model_denylist: v.model_denylist,
                expires_at: v.expires_at ?? "",
                allowed_ips: v.allowed_ips ?? "",
                reset_used: v.reset_used,
              },
            })
          }
          allModels={allModels}
          modelGroupOptions={modelGroupOptions}
          modelsByGroup={modelsByGroup}
        />
      )}
      {created && (
        <Dialog
          title={t("keys.copyTitle")}
          onClose={() => setCreated(null)}
          actions={
            <Button onClick={() => setCreated(null)}>{t("keys.stored")}</Button>
          }
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

type KeyFormValues = {
  name: string;
  scopes?: string;
  token?: string;
  quota_total_tokens?: number;
  price_prompt_per_1k?: number;
  price_completion_per_1k?: number;
  price_cache_per_1k?: number;
  model_allowlist?: string;
  model_denylist?: string;
  expires_at?: string;
  allowed_ips?: string;
  reset_used?: boolean;
};

function KeyDialog({
  mode,
  initial,
  pending,
  error,
  onClose,
  onSave,
  allModels,
  modelGroupOptions,
  modelsByGroup,
}: {
  mode: "create" | "edit";
  initial?: DownstreamKey;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (v: KeyFormValues) => void;
  allModels: string[];
  modelGroupOptions: string[];
  modelsByGroup: Map<string, Set<string>>;
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
  const [pricePrompt, setPricePrompt] = useState(
    String(
      initial?.price_prompt_per_1k && initial.price_prompt_per_1k > 0
        ? initial.price_prompt_per_1k
        : "",
    ),
  );
  const [priceCompletion, setPriceCompletion] = useState(
    String(
      initial?.price_completion_per_1k && initial.price_completion_per_1k > 0
        ? initial.price_completion_per_1k
        : "",
    ),
  );
  const [priceCache, setPriceCache] = useState(
    String(
      initial?.price_cache_per_1k && initial.price_cache_per_1k > 0
        ? initial.price_cache_per_1k
        : "",
    ),
  );
  const splitModels = (raw?: string) =>
    (raw ?? "")
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
  const [allowlist, setAllowlist] = useState<string[]>(() =>
    splitModels(initial?.model_allowlist),
  );
  const [denylist, setDenylist] = useState<string[]>(() =>
    splitModels(initial?.model_denylist),
  );
  const [expiresAt, setExpiresAt] = useState(initial?.expires_at ?? "");
  const [allowedIPs, setAllowedIPs] = useState(initial?.allowed_ips ?? "");
  const [resetUsed, setResetUsed] = useState(false);
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
  const customTooShort =
    useCustomToken && trimmedCustom.length > 0 && trimmedCustom.length < 16;
  const canSubmit =
    Boolean(name.trim()) &&
    (mode === "edit" ||
      !useCustomToken ||
      (trimmedCustom.length >= 16 && !customTooShort));

  const parseOptionalNumber = (raw: string) => {
    const trimmed = raw.trim();
    if (!trimmed) return 0;
    const value = Number(trimmed);
    return Number.isFinite(value) && value >= 0 ? value : 0;
  };

  return (
    <Dialog
      title={mode === "create" ? t("keys.createDialog") : t("keys.editDialog")}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={pending || !canSubmit}
            icon={<KeyRound size={16} />}
            onClick={() =>
              onSave({
                name: name.trim(),
                scopes: scopes.length > 0 ? scopes.join(",") : "relay",
                token:
                  mode === "create" && useCustomToken
                    ? trimmedCustom
                    : undefined,
                quota_total_tokens: parseOptionalNumber(quotaTotal),
                price_prompt_per_1k: parseOptionalNumber(pricePrompt),
                price_completion_per_1k: parseOptionalNumber(priceCompletion),
                price_cache_per_1k: parseOptionalNumber(priceCache),
                model_allowlist: allowlist.join(","),
                model_denylist: denylist.join(","),
                expires_at: expiresAt.trim() || undefined,
                allowed_ips: allowedIPs.trim() || undefined,
                reset_used: mode === "edit" ? resetUsed : undefined,
              })
            }
          >
            {mode === "create" ? t("common.create") : t("common.save")}
          </Button>
        </>
      }
    >
      <div className="ops-panel-context">
        <span>
          {mode === "create" ? t("keys.createHint") : t("keys.editHint")}
        </span>
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
      <Field label={t("keys.quotaTotal")} hint={t("keys.quotaTotalHint")}>
        <input
          type="number"
          min={0}
          step={1}
          value={quotaTotal}
          onChange={(e) => setQuotaTotal(e.target.value)}
          placeholder="0 = unlimited"
        />
      </Field>
      <div className="split" style={{ gap: "0.75rem" }}>
        <Field label={t("keys.pricePrompt")} hint={t("keys.priceHint")}>
          <input
            type="number"
            min={0}
            step="0.0001"
            value={pricePrompt}
            onChange={(e) => setPricePrompt(e.target.value)}
            placeholder="0"
          />
        </Field>
        <Field label={t("keys.priceCompletion")}>
          <input
            type="number"
            min={0}
            step="0.0001"
            value={priceCompletion}
            onChange={(e) => setPriceCompletion(e.target.value)}
            placeholder="0"
          />
        </Field>
        <Field label={t("keys.priceCache")} hint={t("keys.priceCacheHint")}>
          <input
            type="number"
            min={0}
            step="0.0001"
            value={priceCache}
            onChange={(e) => setPriceCache(e.target.value)}
            placeholder="0"
          />
        </Field>
      </div>
      <Field
        label={t("keys.modelAllowlist")}
        hint={t("keys.modelAllowlistHint")}
      >
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
          allModels={allModels}
          selected={allowlist}
          onChange={setAllowlist}
          placeholder={t("keys.modelPickerPlaceholder")}
          emptyLabel={t("keys.modelPickerEmpty")}
        />
      </Field>
      <Field label={t("keys.modelDenylist")} hint={t("keys.modelDenylistHint")}>
        <ModelPicker
          allModels={allModels}
          selected={denylist}
          onChange={setDenylist}
          placeholder={t("keys.modelPickerPlaceholder")}
          emptyLabel={t("keys.modelPickerEmpty")}
        />
      </Field>
      <div className="split" style={{ gap: "0.75rem" }}>
        <Field label={t("keys.expiresAt")} hint={t("keys.expiresAtHint")}>
          <input
            type="datetime-local"
            value={expiresAt ? toLocalInput(expiresAt) : ""}
            disabled={pending}
            onChange={(e) =>
              setExpiresAt(e.target.value ? toRFC3339(e.target.value) : "")
            }
          />
        </Field>
        <Field label={t("keys.allowedIPs")} hint={t("keys.allowedIPsHint")}>
          <textarea
            value={allowedIPs}
            disabled={pending}
            onChange={(e) => setAllowedIPs(e.target.value)}
            placeholder="1.2.3.4&#10;10.0.0.0/8"
            style={{ minHeight: 64 }}
          />
        </Field>
      </div>
      {mode === "edit" ? (
        <label className="check" style={{ marginTop: 12 }}>
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
          <label className="check" style={{ marginTop: 12 }}>
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
            <Field
              label={t("keys.customToken")}
              hint={t("keys.customTokenHint")}
            >
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
