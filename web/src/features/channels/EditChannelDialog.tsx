import { ChevronDown, ExternalLink } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../../api/client";
import type { Channel, RouteOverview, Site } from "../../api/types";
import { parseCredentialMeta } from "../credentialMeta";
import { Drawer } from "../../components/Drawer";
import { SearchableSelect } from "../../components/SearchableSelect";
import { Button, ErrorState, Field } from "../../components/ui";
import { useI18n } from "../../i18n";
import {
  UA_PRESETS,
  isValidUserAgent,
  setUAInHeaderOverride,
  uaFromHeaderOverride,
} from "../../lib/uaPresets";
import { useSession } from "../../session";
import { SECRET_MASK, TYPE_GROUPS, TYPE_OPTIONS } from "./helpers";

export function EditChannelDialog({
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
  onManageModels,
  onManageKeys,
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
    stable_first?: boolean;
    userToken: string;
    apiKey: string;
  }) => void;
  onManageModels?: () => void;
  onManageKeys?: () => void;
}) {
  const { t } = useI18n();
  const inheritedBase = !value.base_url.trim();
  const initialBase = value.base_url || site?.base_url || "";
  const [name, setName] = useState(value.name);
  const [baseUrl, setBaseUrl] = useState(initialBase);
  const [typeHint, setTypeHint] = useState(
    value.type_hint || site?.platform || "openai-compatible",
  );
  const [groupName, setGroupName] = useState(value.group_name || "default");
  const [maxReasoningEffort, setMaxReasoningEffort] = useState(
    value.max_reasoning_effort ?? "",
  );
  const [payloadRules, setPayloadRules] = useState(value.payload_rules ?? "");
  const [proxyUrl, setProxyUrl] = useState(value.proxy_url ?? "");
  const [maxConcurrent, setMaxConcurrent] = useState(value.max_concurrent ?? 0);
  const [priority, setPriority] = useState(value.priority);
  const [weight, setWeight] = useState(value.weight);
  const [headerOverride, setHeaderOverride] = useState(
    value.header_override ?? "",
  );
  const [uaDraft, setUaDraft] = useState(
    uaFromHeaderOverride(value.header_override ?? ""),
  );
  const applyUA = (ua: string) => {
    setUaDraft(ua);
    setHeaderOverride(setUAInHeaderOverride(headerOverride, ua));
  };
  const [systemPrompt, setSystemPrompt] = useState(value.system_prompt ?? "");
  const [retryConfig, setRetryConfig] = useState(value.retry_config ?? "");
  const [stableFirst, setStableFirst] = useState(value.stable_first ?? false);
  const [userToken, setUserToken] = useState(
    userCredential?.has_secret ? SECRET_MASK : "",
  );
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
                group_name: groupName,
                max_reasoning_effort: maxReasoningEffort,
                payload_rules: payloadRules,
                proxy_url: proxyUrl,
                max_concurrent: maxConcurrent,
                priority,
                weight,
                header_override: headerOverride,
                system_prompt: systemPrompt,
                retry_config: retryConfig,
                stable_first: stableFirst,
                userToken,
                apiKey: "",
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
          <Field label={t("common.name")}>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={pending}
            />
          </Field>
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
          <Field label={t("channels.group")} hint={t("channels.groupHint")}>
            <input
              value={groupName}
              onChange={(event) => setGroupName(event.target.value)}
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
          className="credential-key-panel connection-subpanel"
          aria-label={t("channels.apiKeysTitle")}
        >
          <div className="credential-key-panel-head">
            <div>
              <strong>{t("channels.apiKeysTitle")}</strong>
              <p>
                {apiKeys.length === 0
                  ? t("channels.apiKeysEmpty")
                  : t("channels.apiKeysSummary", {
                      n: apiKeys.filter((item) => item.status === "enabled")
                        .length,
                      total: apiKeys.length,
                    })}
              </p>
            </div>
            <Button
              variant="secondary"
              className="connection-manage-button"
              disabled={pending}
              onClick={onManageKeys}
            >
              <ExternalLink size={12} />
              {t("channels.apiKeysManage")}
            </Button>
          </div>
          {apiKeys.length > 0 ? (
            <ul className="credential-key-list is-summary">
              {apiKeys.slice(0, 3).map((item) => {
                const meta = parseCredentialMeta(item.meta_json);
                const label =
                  meta.name?.trim() ||
                  t("channels.apiKeyUnnamed", { id: item.id });
                const usedByThisConnection = value.credential_id === item.id;
                return (
                  <li
                    key={item.id}
                    className={[
                      "credential-key-row",
                      usedByThisConnection ? "is-bound" : "",
                      item.status !== "enabled" ? "is-disabled" : "",
                    ]
                      .filter(Boolean)
                      .join(" ")}
                  >
                    <div className="credential-key-main">
                      <strong>{label}</strong>
                      <small>
                        {`#${item.id}`}
                        {usedByThisConnection
                          ? ` · ${t("channels.apiKeyUsedByConnection")}`
                          : ""}
                      </small>
                    </div>
                    <span
                      className={`credential-key-summary-check${item.status === "enabled" ? " is-checked" : ""}`}
                      role="img"
                      aria-label={
                        item.status === "enabled"
                          ? t("common.enabled")
                          : t("common.disabled")
                      }
                    />
                  </li>
                );
              })}
            </ul>
          ) : null}
          {apiKeys.length > 3 ? (
            <p className="credential-key-more">
              {t("channels.apiKeysMore", { n: apiKeys.length - 3 })}
            </p>
          ) : null}
        </section>

        <section
          className="detail-section channel-model-summary-section connection-subpanel"
          aria-label={t("channels.modelsSection")}
        >
          <div className="detail-section-head">
            <h3>{t("channels.modelsSection")}</h3>
            <span className="detail-section-count">{editModels.length}</span>
            <button
              type="button"
              className="detail-section-expand connection-manage-button connection-manage-button-models"
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

        <div className="advanced-section-divider">
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
        </div>
        {showAdvanced ? (
          <div className="advanced-fields">
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
              <Field
                label={t("channels.maxReasoningEffort")}
                hint={t("channels.maxReasoningEffortHint")}
              >
                <select
                  value={maxReasoningEffort}
                  onChange={(e) => setMaxReasoningEffort(e.target.value)}
                  disabled={pending}
                >
                  <option value="">
                    {t("channels.maxReasoningEffortNone")}
                  </option>
                  {[
                    "none",
                    "minimal",
                    "low",
                    "medium",
                    "high",
                    "xhigh",
                    "max",
                  ].map((level) => (
                    <option key={level} value={level}>
                      {level}
                    </option>
                  ))}
                </select>
              </Field>
              <Field
                label={t("channels.maxConcurrent")}
                hint={t("channels.maxConcurrentHint")}
              >
                <input
                  type="number"
                  min={0}
                  max={10000}
                  value={maxConcurrent}
                  onChange={(e) =>
                    setMaxConcurrent(Math.max(0, Number(e.target.value) || 0))
                  }
                  disabled={pending}
                />
              </Field>
              <Field
                label={t("channels.proxyUrl")}
                hint={t("channels.proxyUrlHint")}
              >
                <input
                  type="url"
                  value={proxyUrl}
                  placeholder="http://127.0.0.1:7897"
                  onChange={(e) => setProxyUrl(e.target.value)}
                  disabled={pending}
                />
              </Field>
            </div>

            <section className="detail-section">
              <div className="detail-section-head">
                <h3>{t("channels.overrides")}</h3>
              </div>
              <Field
                label={t("channels.uaPreset")}
                hint={t("channels.uaPresetHint")}
              >
                <div className="ua-preset-row">
                  <select
                    aria-label={t("channels.uaPreset")}
                    value={
                      uaDraft && UA_PRESETS.includes(uaDraft)
                        ? uaDraft
                        : "custom"
                    }
                    onChange={(e) => {
                      const preset = e.target.value;
                      if (preset !== "custom") applyUA(preset);
                    }}
                    disabled={pending}
                  >
                    <option value="custom">{t("channels.uaCustom")}</option>
                    {UA_PRESETS.map((preset) => (
                      <option key={preset} value={preset}>
                        {preset}
                      </option>
                    ))}
                  </select>
                  <input
                    value={uaDraft}
                    onChange={(e) => applyUA(e.target.value)}
                    placeholder={t("channels.uaPlaceholder")}
                    disabled={pending}
                  />
                </div>
                {uaDraft && !isValidUserAgent(uaDraft) ? (
                  <p className="ua-preset-error">{t("channels.uaInvalid")}</p>
                ) : null}
              </Field>
              <Field
                label={t("channels.headerOverride")}
                hint={t("channels.headerOverrideHint")}
              >
                <textarea
                  className="mono"
                  value={headerOverride}
                  onChange={(e) => {
                    const next = e.target.value;
                    setHeaderOverride(next);
                    setUaDraft(uaFromHeaderOverride(next));
                  }}
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
              <label className="check" style={{ marginTop: 12 }}>
                <input
                  type="checkbox"
                  checked={stableFirst}
                  onChange={(e) => setStableFirst(e.target.checked)}
                  disabled={pending}
                />
                <span>{t("channels.stableFirst")}</span>
              </label>
              <Field
                label={t("channels.payloadRules")}
                hint={t("channels.payloadRulesHint")}
              >
                <textarea
                  className="mono"
                  value={payloadRules}
                  onChange={(e) => setPayloadRules(e.target.value)}
                  disabled={pending}
                  placeholder={JSON.stringify(
                    [
                      {
                        name: "cap max tokens",
                        match: {
                          model: "gpt-*",
                          payload: { max_tokens: { exists: true } },
                        },
                        actions: [
                          {
                            op: "set",
                            path: "max_tokens",
                            value: { num: 8000 },
                          },
                        ],
                      },
                    ],
                    null,
                    2,
                  )}
                  style={{ minHeight: 90 }}
                />
              </Field>
            </section>
          </div>
        ) : null}

        {error ? <ErrorState error={error} /> : null}
      </>
    </Drawer>
  );
}
