import { ChevronDown, ChevronUp, Copy, Eye, Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import type { Channel, Credential } from "../api/types";
import { ModelPicker } from "../components/ModelPicker";
import { Button, ConfirmDialog, Dialog, ErrorState, Field, InfoTip } from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { Drawer } from "../components/Drawer";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { parseCredentialMeta } from "./credentialMeta";

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
  onDeleteKey: (id: number) => void;
  onAddApiKey: (secret: string) => void;
  onSyncKeys: () => void;
  onClose: () => void;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const service = api(client!);
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
                      </small>
                    </div>
                    <div className="credential-key-actions">
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
                        <ModelPicker
                          allModels={channelModelNames}
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
      {revealing && (
        <Dialog
          title={t("channels.apiKeyRevealTitle")}
          onClose={() => {
            if (!reveal.isPending) setRevealing(null);
          }}
          actions={
            <Button
              onClick={() => setRevealing(null)}
              disabled={reveal.isPending}
            >
              {t("common.close")}
            </Button>
          }
        >
          <p className="warning">{t("channels.apiKeyRevealWarning")}</p>
          {reveal.isPending ? (
            <p className="exchange-panel-note">{t("common.loading")}</p>
          ) : reveal.error ? (
            <ErrorState
              error={reveal.error}
              retry={() => reveal.mutate({ siteId: channel.site_id!, id: revealing.id })}
            />
          ) : revealedSecret ? (
            <div className="secret-output">
              <code>{revealedSecret}</code>
              <button
                type="button"
                className="icon-button"
                aria-label={t("keys.copyToken")}
                title={t("keys.copyToken")}
                onClick={() => navigator.clipboard.writeText(revealedSecret)}
              >
                <Copy size={14} />
              </button>
            </div>
          ) : null}
        </Dialog>
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
