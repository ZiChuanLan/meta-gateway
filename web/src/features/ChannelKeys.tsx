import { Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import type { Channel, Credential } from "../api/types";
import { ModelPicker } from "../components/ModelPicker";
import { Button, Field, InfoTip } from "../components/ui";
import { Drawer } from "../components/Drawer";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { parseCredentialMeta } from "./credentialMeta";

/**
 * Left-side drawer with the full API-key management UI for one channel.
 * Opened from the channel editor so the edit form stays visible on the
 * right while keys are managed in a second window.
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

  const discovered = useQuery({
    queryKey: ["discovered-models", channel.id],
    queryFn: ({ signal }) => service.discoveredModels(channel.id, signal),
  });
  const channelModelNames = (discovered.data ?? []).map(
    (model) => model.model_name,
  );

  return (
    <Drawer
      title={`${t("channels.apiKeysTitle")} · ${channel.name}`}
      width={560}
      side="left"
      rightOffset={520}
      plain
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
                    <div className="credential-key-model-label">
                      <span>{t("keys.modelAllowlist")}</span>
                      <InfoTip label={t("channels.keyModelsHint")} />
                    </div>
                    <ModelPicker
                      allModels={channelModelNames}
                      selected={(keyModelsDraft[item.id] ??
                        item.models_csv ??
                        "")
                        .split(",")
                        .map((model) => model.trim())
                        .filter(Boolean)}
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
    </Drawer>
  );
}
