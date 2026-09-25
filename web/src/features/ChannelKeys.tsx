import { ChevronDown, ChevronUp, Eye, Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import type { Channel, Credential } from "../api/types";
import { ModelPicker } from "../components/ModelPicker";
import { SecretRevealDialog } from "../components/SecretRevealDialog";
import { Button, ConfirmDialog, Field, InfoTip } from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { Drawer } from "../components/Drawer";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { parseCredentialMeta } from "./credentialMeta";

/**
 * Pool tiers this drawer exposes, mirroring domain.CredentialPriority*.
 * Higher goes first; keys that share a tier rotate, so equal tiers spread the
 * traffic instead of pinning it on whichever key happens to come first.
 */
const KEY_TIERS = [
  {
    value: 10,
    label: "channels.keyTierPreferred",
    hint: "channels.keyTierPreferredHint",
  },
  {
    value: 0,
    label: "channels.keyTierBalanced",
    hint: "channels.keyTierBalancedHint",
  },
  {
    value: -10,
    label: "channels.keyTierBackup",
    hint: "channels.keyTierBackupHint",
  },
] as const;

/**
 * matchesModel reports whether one allowlist entry covers a model: an exact
 * name, or a "*" suffix prefix wildcard — the same syntax the proxy's per-key
 * allowlist accepts.
 */
function matchesModel(entry: string, model: string): boolean {
  if (entry.endsWith("*")) return model.startsWith(entry.slice(0, -1));
  return entry === model;
}

/**
 * Overlay drawer with the full API-key management UI for one channel.
 * Opened from the channel editor and covering it so the management surface
 * remains easy to use on narrow screens.
 */
export function ChannelKeysDrawer({
  channel,
  apiKeys,
  pending,
  addApiKeyPending,
  syncKeysPending,
  onToggleKey,
  onUpdateKeyModels,
  onUpdateKeyPriority,
  onDeleteKey,
  onAddApiKey,
  onSyncKeys,
  onClose,
}: {
  channel: Channel;
  apiKeys: Credential[];
  pending: boolean;
  addApiKeyPending?: boolean;
  syncKeysPending?: boolean;
  onToggleKey: (id: number, enabled: boolean) => void;
  onUpdateKeyModels: (id: number, modelsCsv: string) => void;
  onUpdateKeyPriority: (id: number, priority: number) => void;
  onDeleteKey: (id: number) => void;
  onAddApiKey: (secret: string, name?: string) => void;
  onSyncKeys: () => void;
  onClose: () => void;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const service = api(client!);
  const [apiKeyName, setApiKeyName] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [keyModelsDraft, setKeyModelsDraft] = useState<
    Record<number, string>
  >({});
  const [expandedModelIds, setExpandedModelIds] = useState<Set<number>>(
    () => new Set(),
  );
  // Reveal: decrypt and show the plaintext secret of one credential.
  const [revealing, setRevealing] = useState<Credential | null>(null);
  const [revealedSecret, setRevealedSecret] = useState<string | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState<{
    id: number;
    label: string;
  } | null>(null);
  const reveal = useAdminMutation({
    mutationFn: (v: { siteId: number; id: number }) =>
      service.revealCredential(v.siteId, v.id),
    toastOnError: false,
    onSuccess: (result) => setRevealedSecret(result.secret),
  });

  const discovered = useQuery({
    queryKey: ["discovered-models", channel.id],
    queryFn: ({ signal }) => service.discoveredModels(channel.id, signal),
  });
  const channelModelNames = (discovered.data ?? []).map(
    (model) => model.model_name,
  );

  const toggleModelPicker = (id: number) => {
    setExpandedModelIds((previous) => {
      const next = new Set(previous);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const submitAdd = () => {
    const secret = apiKey.trim();
    if (!secret || pending || addApiKeyPending) return;
    // First key becomes the relay key; later keys just join the pool.
    onAddApiKey(secret, apiKeyName.trim() || undefined);
    setApiKey("");
    setApiKeyName("");
  };

  return (
    <Drawer
      title={`${t("channels.apiKeysTitle")} · ${channel.name}`}
      width={780}
      onClose={onClose}
      footer={
        <Button variant="secondary" onClick={onClose}>
          {t("common.close")}
        </Button>
      }
    >
      <section className="credential-key-panel">
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
              const usedByThisConnection = channel.credential_id === item.id;
              const label =
                meta.name?.trim() ||
                t("channels.apiKeyUnnamed", { id: item.id });
              const groupLabel =
                meta.group?.trim() || t("channels.apiKeyGroupDefault");
              const selectedModels = (keyModelsDraft[item.id] ??
                item.models_csv ??
                "")
                .split(",")
                .map((model) => model.trim())
                .filter(Boolean);
              const modelsExpanded = expandedModelIds.has(item.id);
              const tier = item.priority ?? 0;
              const ownModels = item.models ?? [];
              const scoped = ownModels.length > 0;
              // Candidates are the models THIS key synced. Without a snapshot
              // there is nothing to scope by, so fall back to the channel-wide
              // list and say so above the picker.
              const candidateModels = scoped ? ownModels : channelModelNames;
              // Selections this key never listed: an explicit allowlist
              // overrides the proxy's per-key discovered filter, so these are
              // tried upstream and 404 instead of being skipped.
              const outOfScope = scoped
                ? selectedModels.filter(
                    (model) =>
                      !ownModels.some((own) => matchesModel(model, own)),
                  )
                : [];
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
                  <div className="credential-key-row-head">
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
                        {item.model_count != null && item.model_count >= 0
                          ? ` · ${t("channels.apiKeyModelCount", {
                              count: item.model_count,
                            })}`
                          : ""}
                      </small>
                    </div>
                    <div className="credential-key-actions">
                      {/* The pool tier sits where the old read-only "preferred"
                          tag used to: same concept, upgraded from a label into
                          the control that sets it. */}
                      <div
                        className="sync-mode-toggle credential-key-tier"
                        role="radiogroup"
                        aria-label={t("channels.keyTier")}
                      >
                        {KEY_TIERS.map((option) => {
                          const active = tier === option.value;
                          return (
                            <button
                              key={option.value}
                              type="button"
                              disabled={pending}
                              className={active ? "is-active" : ""}
                              aria-pressed={active}
                              title={t(option.hint)}
                              onClick={() => {
                                if (!active) {
                                  onUpdateKeyPriority(item.id, option.value);
                                }
                              }}
                            >
                              {t(option.label)}
                            </button>
                          );
                        })}
                      </div>
                      <label
                        className={`credential-key-status ${enabled ? "is-enabled" : "is-disabled"}`}
                      >
                        <input
                          type="checkbox"
                          checked={enabled}
                          disabled={pending}
                          onChange={(event) =>
                            onToggleKey(item.id, event.target.checked)
                          }
                        />
                        <span className="credential-key-status-dot" aria-hidden="true" />
                        <span>
                          {enabled ? t("common.enabled") : t("common.disabled")}
                        </span>
                      </label>
                      <span className="credential-key-action-group">
                        {item.has_secret ? (
                          <button
                            type="button"
                            className="icon-button credential-key-action credential-key-action-reveal"
                            aria-label={t("channels.apiKeyReveal")}
                            title={t("channels.apiKeyReveal")}
                            disabled={pending || reveal.isPending}
                            onClick={() => {
                              reveal.reset();
                              setRevealedSecret(null);
                              setRevealing(item);
                              reveal.mutate({
                                siteId: channel.site_id!,
                                id: item.id,
                              });
                            }}
                          >
                            <Eye size={15} />
                          </button>
                        ) : null}
                        <button
                          type="button"
                          className="icon-button credential-key-action credential-key-action-delete"
                          aria-label={t("channels.apiKeyDelete")}
                          title={t("channels.apiKeyDelete")}
                          disabled={pending}
                          onClick={() => {
                            setConfirmingDelete({ id: item.id, label });
                          }}
                        >
                          <Trash2 size={15} />
                        </button>
                      </span>
                    </div>
                  </div>
                  <div className="credential-key-model-control">
                    <div className="credential-key-model-toggle-row">
                      <span className="credential-key-model-label">
                        <span>{t("keys.modelAllowlist")}</span>
                        <InfoTip label={t("channels.keyModelsHint")} />
                      </span>
                      <button
                        type="button"
                        className={`credential-key-model-toggle ${modelsExpanded ? "is-open" : ""}`}
                        aria-label={t("channels.keyModelsToggle")}
                        aria-expanded={modelsExpanded}
                        aria-controls={`credential-key-models-${item.id}`}
                        onClick={() => toggleModelPicker(item.id)}
                      >
                        <span className="credential-key-model-summary">
                          {selectedModels.length > 0
                            ? t("channels.keyModelsSelected", {
                                n: selectedModels.length,
                              })
                            : t("channels.keyModelsAll")}
                        </span>
                        {modelsExpanded ? (
                          <ChevronUp size={16} aria-hidden="true" />
                        ) : (
                          <ChevronDown size={16} aria-hidden="true" />
                        )}
                      </button>
                    </div>
                    {modelsExpanded ? (
                      <div
                        id={`credential-key-models-${item.id}`}
                        className="credential-key-model-editor"
                      >
                        {scoped ? null : (
                          <p className="credential-key-model-note">
                            {t("channels.keyModelsNoSnapshot")}
                          </p>
                        )}
                        {outOfScope.length > 0 ? (
                          <p className="credential-key-model-note is-warning">
                            {t("channels.keyModelsOutOfScope", {
                              n: outOfScope.length,
                            })}
                          </p>
                        ) : null}
                        <ModelPicker
                          options={candidateModels.map((name) => ({ name }))}
                          selected={selectedModels}
                          onChange={(selected) => {
                            const next = selected.join(",");
                            setKeyModelsDraft((prev) => ({
                              ...prev,
                              [item.id]: next,
                            }));
                            onUpdateKeyModels(item.id, next);
                          }}
                          placeholder={t("channels.keyModelsPlaceholder")}
                          emptyLabel={t("channels.modelsEmpty")}
                          className="credential-key-model-picker"
                        />
                      </div>
                    ) : null}
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
              className="credential-key-name-input"
              value={apiKeyName}
              onChange={(e) => setApiKeyName(e.target.value)}
              placeholder={t("channels.apiKeyNamePlaceholder")}
              disabled={pending || Boolean(addApiKeyPending)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  submitAdd();
                }
              }}
            />
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
                  submitAdd();
                }
              }}
            />
            <Button
              variant="secondary"
              disabled={
                pending || Boolean(addApiKeyPending) || !apiKey.trim()
              }
              onClick={submitAdd}
            >
              {addApiKeyPending
                ? t("common.loading")
                : t("channels.apiKeyAddSave")}
            </Button>
          </div>
        </Field>
      </section>
      {revealing && (
        <SecretRevealDialog
          title={t("channels.apiKeyRevealTitle")}
          warning={t("channels.apiKeyRevealWarning")}
          secret={revealedSecret}
          pending={reveal.isPending}
          error={reveal.error}
          onRetry={() => reveal.mutate({ siteId: channel.site_id!, id: revealing.id })}
          closeLabel={t("common.close")}
          copyLabel={t("keys.copyToken")}
          onClose={() => {
            if (!reveal.isPending) setRevealing(null);
          }}
        />
      )}
      {confirmingDelete ? (
        <ConfirmDialog
          title={t("channels.apiKeyDelete")}
          message={t("channels.apiKeyDeleteConfirm", {
            name: confirmingDelete.label,
          })}
          pending={pending}
          onClose={() => setConfirmingDelete(null)}
          onConfirm={() => {
            onDeleteKey(confirmingDelete.id);
            setConfirmingDelete(null);
          }}
        />
      ) : null}
    </Drawer>
  );
}
