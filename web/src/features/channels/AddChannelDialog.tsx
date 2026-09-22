import { Cable, ChevronDown } from "lucide-react";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import { SearchableSelect } from "../../components/SearchableSelect";
import {
  Button,
  Dialog,
  ErrorState,
  Field,
  InfoTip,
} from "../../components/ui";
import { useI18n } from "../../i18n";
import { PROVIDER_BASE_URLS } from "../../connectionTypes";
import { useSession } from "../../session";
import { TYPE_OPTIONS } from "./helpers";
import { TYPE_GROUPS, type CreateConnectionInput } from "./helpers";
import { SyncModePicker, type ModelSyncMode } from "./SyncModePicker";

export function AddChannelDialog({
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
  // Whether the operator has stated the type themselves. Auto-detection only
  // fills the field while it is still untouched: the detector is a heuristic
  // (it reported `new-api` for Zhipu's URL on a bare `user-self-401` response),
  // so letting it overwrite an explicit pick silently reverted "智谱 GLM" back to
  // "New API" on blur — taking the correct preset base URL with it.
  const [typeTouched, setTypeTouched] = useState(false);
  const [groupName, setGroupName] = useState("default");
  const [showAdvanced, setShowAdvanced] = useState(false);
  // The endpoint the gateway would actually call for this base URL. The
  // provider presets are only as good as the URL builder, and a preset whose
  // base lands on the wrong path is invisible until the first request 404s —
  // which is what used to push operators into hand-written endpoint overrides.
  // Showing the resolved URL in the dialog makes a wrong join obvious up front.
  const [endpointPreview, setEndpointPreview] = useState<string | null>(null);
  const canSubmit = Boolean(baseUrl.trim() && secret.trim());

  // The sync mode is a per-channel decision with a real operational cost, so
  // it is asked up front instead of being silently inherited. The system
  // default only seeds the pre-selection and is labelled as such.
  const runtimeSettings = useQuery({
    queryKey: ["runtime-settings"],
    queryFn: ({ signal }) => service.runtimeSettings(signal),
    retry: false,
  });
  const defaultSyncMode: ModelSyncMode =
    runtimeSettings.data?.editable.default_model_sync_mode === "auto"
      ? "auto"
      : "manual";
  const [syncMode, setSyncMode] = useState<ModelSyncMode | null>(null);
  const effectiveSyncMode = syncMode ?? defaultSyncMode;

  return (
    <Dialog
      title={t("channels.add")}
      onClose={onClose}
      busy={pending}
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
                  group_name: groupName,
                  model_sync_mode: effectiveSyncMode,
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
              setTypeTouched(true);
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
                .endpointPreview(url)
                .then((preview) => setEndpointPreview(preview.chat_url ?? null))
                .catch(() => setEndpointPreview(null));
              service
                .detectSiteType(url)
                .then((detected) => {
                  // Never clobber an explicit choice: the operator picked a
                  // provider, and this heuristic has no authority over that.
                  if (typeTouched) return;
                  if (
                    detected.family &&
                    TYPE_OPTIONS.some((o) => o.value === detected.family)
                  ) {
                    setTypeHint(detected.family);
                  }
                })
                .catch(() => undefined);
            }}
            placeholder="https://api.example.com"
            disabled={pending}
          />
          {endpointPreview ? (
            <small className="field-preview mono" title={endpointPreview}>
              {t("channels.endpointPreview", { url: endpointPreview })}
            </small>
          ) : null}
        </Field>
        <Field label={t("channels.group")} hint={t("channels.groupHint")}>
          <input
            value={groupName}
            onChange={(event) => setGroupName(event.target.value)}
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

      <section
        className="detail-section connection-subpanel"
        aria-label={t("channels.syncMode")}
      >
        <div className="detail-section-head">
          <h3>{t("channels.modelsSection")}</h3>
        </div>
        <SyncModePicker
          value={effectiveSyncMode}
          onChange={setSyncMode}
          disabled={pending}
          defaultMode={defaultSyncMode}
        />
      </section>

      <button
        type="button"
        className={`advanced-toggle${showAdvanced ? " is-open" : ""}`}
        onClick={() => setShowAdvanced((value) => !value)}
      >
        <ChevronDown size={13} />
        {showAdvanced ? t("channels.hideAdvanced") : t("channels.showAdvanced")}
      </button>
      {showAdvanced ? (
        <div className="stack-tight">
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
                  group_name: groupName,
                  model_sync_mode: effectiveSyncMode,
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
