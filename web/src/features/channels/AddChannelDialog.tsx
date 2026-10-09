import { Cable, ChevronDown, ListPlus } from "lucide-react";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import { ModelPicker } from "../../components/ModelPicker";
import { SearchableSelect } from "../../components/SearchableSelect";
import { Button, Dialog, ErrorState, Field, InfoTip } from "../../components/ui";
import { useI18n } from "../../i18n";
import { PROVIDER_BASE_URLS } from "../../connectionTypes";
import { apiKeyLooksWrong, keyHintFor } from "../../lib/apiKeyPaste";
import { useSession } from "../../session";
import { TYPE_OPTIONS } from "./helpers";
import {
  TYPE_GROUPS,
  normalizeBase,
  type ConnectionAdvancedPatch,
  type CreateConnectionInput,
} from "./helpers";
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
  // Model list pulled from the upstream before anything is saved. Holding it
  // here (rather than creating the channel first and discovering after) is the
  // point: a wrong URL or key costs a retry, not a half-configured channel.
  const [upstreamModels, setUpstreamModels] = useState<string[] | null>(null);
  // A fresh list starts fully selected under manual sync: the operator asked for
  // this upstream's models, and unticking a few is cheaper than ticking eighty.
  const [picked, setPicked] = useState<string[]>([]);
  const [fetchingModels, setFetchingModels] = useState(false);
  const [modelsError, setModelsError] = useState<unknown>(null);
  // Only fields the operator touched. An empty patch means "whatever the
  // backend defaults to", which is why the values start undefined instead of
  // being pre-filled with what the form happens to render.
  const [advanced, setAdvanced] = useState<ConnectionAdvancedPatch>({});
  const setAdvancedField = <K extends keyof ConnectionAdvancedPatch>(
    key: K,
    value: ConnectionAdvancedPatch[K],
  ) => setAdvanced((current) => ({ ...current, [key]: value }));

  const canSubmit = Boolean(baseUrl.trim() && secret.trim());
  const canFetchModels = canSubmit && !pending && !fetchingModels;
  // A hint, not a rule: relay sites issue whatever token they like, so a
  // mismatch only ever earns a note next to the field.
  const secretHint = keyHintFor(typeHint);
  const secretLooksWrong = apiKeyLooksWrong(typeHint, secret);

  // The sync mode is a per-channel decision with a real operational cost, so
  // it is asked up front instead of being silently inherited. The system
  // default only seeds the pre-selection and is labelled as such.
  const runtimeSettings = useQuery({
    queryKey: ["runtime-settings"],
    queryFn: ({ signal }) => service.runtimeSettings(signal),
    retry: false,
  });
  // Sites are looked up only to answer one question in the interface: when this
  // base URL already exists as a site, "follow the site" can name the policy it
  // would actually follow instead of showing a blank inheritance.
  const sites = useQuery({
    queryKey: ["sites"],
    queryFn: ({ signal }) => service.sites(signal),
    retry: false,
  });
  const matchedSite = (sites.data ?? []).find(
    (site) => normalizeBase(site.base_url) === normalizeBase(baseUrl),
  );
  const inheritedPolicyLabel = matchedSite
    ? t("channels.callPolicyInherit", {
        policy: t(
          matchedSite.call_policy === "real_calls_only"
            ? "channels.keepalive.policyRealCallsOnly"
            : "channels.keepalive.policyAllowProbe",
        ),
      })
    : t("channels.callPolicyInheritUnknown");
  const defaultSyncMode: ModelSyncMode =
    runtimeSettings.data?.editable.default_model_sync_mode === "auto" ? "auto" : "manual";
  const [syncMode, setSyncMode] = useState<ModelSyncMode | null>(null);
  const effectiveSyncMode = syncMode ?? defaultSyncMode;

  const fetchModels = async () => {
    setFetchingModels(true);
    setModelsError(null);
    try {
      const result = await service.previewChannelModels({
        base_url: baseUrl.trim(),
        secret: secret.trim(),
        type_hint: typeHint,
      });
      setUpstreamModels(result.models);
      setPicked(result.models);
    } catch (err) {
      setModelsError(err);
      setUpstreamModels(null);
      setPicked([]);
    } finally {
      setFetchingModels(false);
    }
  };

  const submit = (options: { verify: boolean }) =>
    onSave(
      {
        name,
        base_url: baseUrl,
        secret,
        type_hint: typeHint,
        group_name: groupName,
        model_sync_mode: effectiveSyncMode,
        // Omitted when nothing was fetched: "never fetched" is not the same
        // statement as "fetched and picked none".
        ...(upstreamModels ? { models_csv: picked.join(",") } : {}),
        ...(Object.keys(advanced).length > 0 ? { advanced } : {}),
      },
      options,
    );

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
            onClick={() => submit({ verify: true })}
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
                  (previousDefault && currentTrimmed === previousDefault.replace(/\/+$/, ""))
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
                  if (detected.family && TYPE_OPTIONS.some((o) => o.value === detected.family)) {
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
        <Field label={t("common.secret")} hint={secretHint || undefined}>
          <input
            type="password"
            autoComplete="new-password"
            required
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            disabled={pending}
          />
          {secretLooksWrong ? (
            <p className="map-row-error">
              {t("channels.keyFormatMismatch", {
                type: typeHint,
                hint: secretHint,
              })}
            </p>
          ) : null}
        </Field>
      </div>

      <section
        className="detail-section connection-subpanel"
        aria-label={t("channels.modelsSection")}
      >
        <div className="detail-section-head">
          <h3>{t("channels.modelsSection")}</h3>
          {upstreamModels ? (
            <span className="detail-section-count">{upstreamModels.length}</span>
          ) : null}
          {/* Same header shape as the channel detail's model block: the title on
              the left, the thing you can do about it on the right. */}
          <div className="detail-section-actions">
            <button
              type="button"
              className="detail-section-expand connection-manage-button"
              onClick={fetchModels}
              disabled={!canFetchModels}
              title={
                canFetchModels ? t("channels.modelsFetchHint") : t("channels.modelsFetchNeedsInput")
              }
            >
              <ListPlus size={12} className={fetchingModels ? "spin" : undefined} />
              {fetchingModels ? t("common.working") : t("channels.modelsFetch")}
            </button>
          </div>
        </div>
        <SyncModePicker
          value={effectiveSyncMode}
          onChange={setSyncMode}
          disabled={pending}
          defaultMode={defaultSyncMode}
        />
        {upstreamModels === null ? (
          <p className="detail-section-empty is-quiet">{t("channels.modelsFetchHint")}</p>
        ) : null}
        {modelsError ? <ErrorState error={modelsError} /> : null}
        {upstreamModels !== null ? (
          <>
            <p className="muted">
              {upstreamModels.length === 0
                ? t("channels.modelsFetchedEmpty")
                : t("channels.modelsFetchedCount", { n: upstreamModels.length })}
              {effectiveSyncMode === "auto" ? ` · ${t("channels.modelsAutoNote")}` : ""}
            </p>
            {/* Under auto sync the picker would be a lie: discovery adopts every
                model the upstream serves, so there is nothing to tick. */}
            {effectiveSyncMode === "manual" && upstreamModels.length > 0 ? (
              <ModelPicker
                options={upstreamModels.map((name) => ({ name }))}
                selected={picked}
                onChange={setPicked}
                emptyLabel={t("channels.modelsFetchedEmpty")}
              />
            ) : null}
          </>
        ) : null}
      </section>

      <button
        type="button"
        className={`advanced-toggle${showAdvanced ? " is-open" : ""}`}
        onClick={() => setShowAdvanced((value) => !value)}
      >
        <ChevronDown size={13} />
        {showAdvanced
          ? t("channels.hideAdvanced")
          : Object.keys(advanced).length > 0
            ? t("channels.showAdvancedWithCount", { n: Object.keys(advanced).length })
            : t("channels.showAdvanced")}
      </button>
      {showAdvanced ? (
        <div className="advanced-fields">
          <div className="form-grid">
            {/* Only touched fields are sent (see ConnectionAdvancedPatch): the
                empty box means "backend default", not "zero". */}
            <Field label={t("common.priority")} hint={t("channels.priorityHint")}>
              <input
                type="number"
                value={advanced.priority ?? ""}
                placeholder="0"
                onChange={(event) =>
                  setAdvancedField(
                    "priority",
                    event.target.value === "" ? undefined : Number(event.target.value) || 0,
                  )
                }
                disabled={pending}
              />
            </Field>
            <Field label={t("common.weight")} hint={t("channels.weightHint")}>
              <input
                type="number"
                value={advanced.weight ?? ""}
                placeholder="100"
                onChange={(event) =>
                  setAdvancedField(
                    "weight",
                    event.target.value === "" ? undefined : Number(event.target.value) || 0,
                  )
                }
                disabled={pending}
              />
            </Field>
            <Field
              label={t("channels.maxReasoningEffort")}
              hint={t("channels.maxReasoningEffortHint")}
            >
              <select
                value={advanced.max_reasoning_effort ?? ""}
                onChange={(event) =>
                  setAdvancedField(
                    "max_reasoning_effort",
                    event.target.value === "" ? undefined : event.target.value,
                  )
                }
                disabled={pending}
              >
                <option value="">{t("channels.maxReasoningEffortNone")}</option>
                {["none", "minimal", "low", "medium", "high", "xhigh", "max"].map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t("channels.maxConcurrent")} hint={t("channels.maxConcurrentHint")}>
              <input
                type="number"
                min={0}
                max={10000}
                value={advanced.max_concurrent ?? ""}
                placeholder="0"
                onChange={(event) =>
                  setAdvancedField(
                    "max_concurrent",
                    event.target.value === ""
                      ? undefined
                      : Math.max(0, Number(event.target.value) || 0),
                  )
                }
                disabled={pending}
              />
            </Field>
            <Field label={t("channels.nonStreamTimeout")} hint={t("channels.nonStreamTimeoutHint")}>
              <input
                type="number"
                min={0}
                max={86400}
                value={advanced.non_stream_timeout_seconds ?? ""}
                placeholder="0"
                onChange={(event) =>
                  setAdvancedField(
                    "non_stream_timeout_seconds",
                    event.target.value === ""
                      ? undefined
                      : Math.max(0, Math.min(86400, Number(event.target.value) || 0)),
                  )
                }
                disabled={pending}
              />
            </Field>
            <Field label={t("channels.streamPolicy")} hint={t("channels.streamPolicyHint")}>
              <select
                value={advanced.stream_policy ?? ""}
                onChange={(event) =>
                  setAdvancedField(
                    "stream_policy",
                    event.target.value === ""
                      ? undefined
                      : (event.target.value as ConnectionAdvancedPatch["stream_policy"]),
                  )
                }
                disabled={pending}
              >
                <option value="">{t("channels.streamPolicyDefault")}</option>
                <option value="force_stream">{t("channels.streamPolicyForceStream")}</option>
                <option value="force_non_stream">{t("channels.streamPolicyForceNonStream")}</option>
              </select>
            </Field>
            <Field label={t("channels.proxyUrl")} hint={t("channels.proxyUrlHint")}>
              <input
                type="url"
                value={advanced.proxy_url ?? ""}
                placeholder="http://127.0.0.1:7897"
                onChange={(event) =>
                  setAdvancedField(
                    "proxy_url",
                    event.target.value === "" ? undefined : event.target.value,
                  )
                }
                disabled={pending}
              />
            </Field>
            <Field label={t("channels.callPolicy")} hint={t("channels.callPolicyHint")}>
              <select
                value={advanced.call_policy ?? ""}
                onChange={(event) =>
                  setAdvancedField(
                    "call_policy",
                    event.target.value === ""
                      ? undefined
                      : (event.target.value as ConnectionAdvancedPatch["call_policy"]),
                  )
                }
                disabled={pending}
              >
                <option value="">{inheritedPolicyLabel}</option>
                <option value="allow_probe">{t("channels.keepalive.policyAllowProbe")}</option>
                <option value="real_calls_only">
                  {t("channels.keepalive.policyRealCallsOnly")}
                </option>
              </select>
            </Field>
          </div>
          <div className="stack-tight">
            <Button
              variant="secondary"
              disabled={pending || !canSubmit}
              onClick={() => submit({ verify: false })}
            >
              {t("channels.saveOnly")}
            </Button>
            <InfoTip label={t("channels.saveOnlyHint")} />
          </div>
        </div>
      ) : null}

      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}
