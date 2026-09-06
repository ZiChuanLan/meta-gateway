import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Route, RoutingCandidate } from "../../api/types";
import { api } from "../../api/client";
import {
  Button,
  Dialog,
  ErrorState,
  Field,
  InfoTip,
} from "../../components/ui";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";

const REASONING_LEVELS = [
  "none",
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
];

export function RouteDialog({
  value,
  members,
  pending,
  error,
  onClose,
  onSave,
}: {
  value: Partial<Route>;
  members: RoutingCandidate[];
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (
    value: Partial<Route> & {
      pin_priority?: boolean;
      auto_match_channel_ids?: number[];
    },
  ) => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const [form, setForm] = useState(value);
  const [advanced, setAdvanced] = useState(false);
  // Auto-match only exists at creation: the edit dialog manages members
  // through the member list instead.
  const isCreate = value.id == null;
  const [autoMatch, setAutoMatch] = useState(true);
  const pattern = (form.model_pattern ?? "").trim();
  // Live candidates: enabled channels serving this pattern, so the checkbox
  // states its consequence before saving instead of surprising afterwards.
  const matches = useQuery({
    queryKey: ["model-channels", pattern],
    queryFn: ({ signal }) => service.modelChannels(pattern, signal),
    enabled: isCreate && autoMatch && pattern.length > 0,
    placeholderData: (previous) => previous,
  });
  const matchItems = matches.data?.items ?? [];
  // Per-channel pick: which of the matched channels to attach. The selection
  // is scoped to the pattern — typing a new model resets to all-selected.
  const [selection, setSelection] = useState<{
    pattern: string;
    ids: Set<number>;
  } | null>(null);
  const selectedMatches =
    selection && selection.pattern === pattern
      ? selection.ids
      : new Set(matchItems.map((item) => item.channel_id));
  const toggleMatch = (id: number) => {
    const next = new Set(selectedMatches);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelection({ pattern, ids: next });
  };
  const patch = (partial: Partial<Route>) =>
    setForm((current) => ({ ...current, ...partial }));
  // Model-level overrides use the same convention as the channel advanced
  // form: an empty field inherits the channel default, a filled field
  // overrides it. Empty input maps to null so the backend keeps NULL = inherit.
  const allPinned =
    members.length > 0 &&
    members.every((candidate) => candidate.member.manual_override);
  const [pinPriority, setPinPriority] = useState(allPinned);
  return (
    <Dialog
      title={value.id ? t("routing.editRoute") : t("routing.addRoute")}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={pending || !form.model_pattern}
            onClick={() =>
              onSave({
                ...form,
                pin_priority: pinPriority,
                auto_match_channel_ids:
                  isCreate && autoMatch ? [...selectedMatches] : undefined,
              })
            }
          >
            {pending ? t("common.working") : t("common.save")}
          </Button>
        </>
      }
    >
      <div className="ops-panel-context">
        <span>{t("modelsPage.addRouteHint")}</span>
      </div>
      <Field label={t("routing.exactModel")}>
        <input
          value={form.model_pattern ?? ""}
          onChange={(event) =>
            setForm({ ...form, model_pattern: event.target.value })
          }
          placeholder="gpt-4o-mini"
        />
      </Field>
      <label className="check">
        <input
          type="checkbox"
          checked={form.enabled ?? false}
          onChange={(event) =>
            setForm({ ...form, enabled: event.target.checked })
          }
        />
        <span>{t("routing.routeEnabled")}</span>
      </label>
      {isCreate ? (
        <>
          <label className="check">
            <input
              type="checkbox"
              checked={autoMatch}
              onChange={(event) => setAutoMatch(event.target.checked)}
            />
            <span>
              <strong>{t("routing.autoMatchLabel")}</strong>
              <InfoTip label={t("routing.autoMatchHint")} />
            </span>
          </label>
          {autoMatch && pattern ? (
            matches.isPending ? (
              <p className="ops-panel-context" role="status">
                {t("common.loading")}
              </p>
            ) : matches.isError ? (
              <p className="ops-panel-context" role="status">
                {t("routing.autoMatchUnknown")}
              </p>
            ) : matchItems.length > 0 ? (
              <div className="auto-match-preview">
                <p className="ops-panel-context" role="status">
                  {t("routing.autoMatchSelected", {
                    selected: selectedMatches.size,
                    total: matchItems.length,
                  })}
                </p>
                <div
                  className="selection-list"
                  style={{ maxHeight: 220, overflowY: "auto" }}
                >
                  {matchItems.map((item) => (
                    <label className="check" key={item.channel_id}>
                      <input
                        type="checkbox"
                        checked={selectedMatches.has(item.channel_id)}
                        onChange={() => toggleMatch(item.channel_id)}
                      />
                      <span>{item.channel_name}</span>
                    </label>
                  ))}
                </div>
              </div>
            ) : (
              <p className="ops-panel-context" role="status">
                {t("routing.autoMatchNone")}
              </p>
            )
          ) : null}
        </>
      ) : null}
      <div className="ops-panel-context" style={{ marginTop: 12 }}>
        <span>{t("routing.retryOverrideTitle")}</span>
      </div>
      <div className="form-grid">
        <Field
          label={t("routing.retryRounds")}
          hint={t("routing.retryRoundsHint")}
        >
          <input
            type="number"
            min={0}
            max={100}
            placeholder={t("routing.retryFollowGlobal")}
            value={form.retry_times ?? ""}
            onChange={(event) => {
              const v = event.target.value;
              setForm({
                ...form,
                retry_times: v === "" ? null : Number(v),
              });
            }}
          />
        </Field>
        <Field
          label={t("routing.channelRetry")}
          hint={t("routing.channelRetryHint")}
        >
          <input
            type="number"
            min={0}
            max={5}
            placeholder={t("routing.retryFollowGlobal")}
            value={form.channel_retry_times ?? ""}
            onChange={(event) => {
              const v = event.target.value;
              setForm({
                ...form,
                channel_retry_times: v === "" ? null : Number(v),
              });
            }}
          />
        </Field>
      </div>
      <button
        type="button"
        className={`advanced-toggle${advanced ? " is-open" : ""}`}
        onClick={() => setAdvanced((current) => !current)}
      >
        {advanced
          ? t("modelsPage.hideOverrides")
          : t("modelsPage.showOverrides")}
      </button>
      {advanced ? (
        <section className="advanced-fields">
          <div className="ops-panel-context">
            <span>{t("modelsPage.overrideHint")}</span>
          </div>
          <Field
            label={t("modelsPage.modelGroup")}
            hint={t("modelsPage.modelGroupHint")}
          >
            <input
              value={form.model_group ?? ""}
              placeholder={t("modelsPage.modelGroupAuto")}
              onChange={(event) => patch({ model_group: event.target.value })}
            />
          </Field>
          <div className="form-grid">
            <Field label={t("channels.maxReasoningEffort")}>
              <select
                value={form.max_reasoning_effort ?? ""}
                onChange={(event) =>
                  patch({
                    max_reasoning_effort:
                      event.target.value === "" ? null : event.target.value,
                  })
                }
              >
                <option value="">{t("modelsPage.inherit")}</option>
                {REASONING_LEVELS.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t("channels.maxConcurrent")}>
              <input
                type="number"
                min={0}
                placeholder={t("modelsPage.inherit")}
                value={form.max_concurrent ?? ""}
                onChange={(event) =>
                  patch({
                    max_concurrent:
                      event.target.value === ""
                        ? null
                        : Math.max(0, Number(event.target.value) || 0),
                  })
                }
              />
            </Field>
          </div>
          <Field label={t("channels.proxyUrl")}>
            <input
              type="url"
              value={form.proxy_url ?? ""}
              placeholder="http://127.0.0.1:7897"
              onChange={(event) =>
                patch({
                  proxy_url:
                    event.target.value === "" ? null : event.target.value,
                })
              }
            />
          </Field>
          <Field label={t("channels.headerOverride")}>
            <textarea
              className="mono"
              value={form.header_override ?? ""}
              placeholder='{"User-Agent":"…"}'
              onChange={(event) =>
                patch({
                  header_override:
                    event.target.value === "" ? null : event.target.value,
                })
              }
            />
          </Field>
          <Field label={t("channels.systemPrompt")}>
            <textarea
              value={form.system_prompt ?? ""}
              onChange={(event) =>
                patch({
                  system_prompt:
                    event.target.value === "" ? null : event.target.value,
                })
              }
            />
          </Field>
          <Field label={t("channels.retryConfig")}>
            <textarea
              className="mono"
              value={form.retry_config ?? ""}
              onChange={(event) =>
                patch({
                  retry_config:
                    event.target.value === "" ? null : event.target.value,
                })
              }
            />
          </Field>
          <Field label={t("channels.payloadRules")}>
            <textarea
              className="mono"
              value={form.payload_rules ?? ""}
              onChange={(event) =>
                patch({
                  payload_rules:
                    event.target.value === "" ? null : event.target.value,
                })
              }
            />
          </Field>
          <div className="form-grid">
            <Field label={t("modelsPage.grayEnabled")}>
              <select
                value={
                  form.stable_first == null
                    ? "inherit"
                    : form.stable_first
                      ? "on"
                      : "off"
                }
                onChange={(event) =>
                  patch({
                    stable_first:
                      event.target.value === "inherit"
                        ? null
                        : event.target.value === "on",
                  })
                }
              >
                <option value="inherit">{t("modelsPage.inherit")}</option>
                <option value="on">{t("common.enabled")}</option>
                <option value="off">{t("common.disabled")}</option>
              </select>
            </Field>
            <Field label={t("modelsPage.grayDenominator")}>
              <input
                type="number"
                min={2}
                max={1000}
                placeholder={t("modelsPage.inherit")}
                value={form.stable_first_denominator ?? ""}
                onChange={(event) =>
                  patch({
                    stable_first_denominator:
                      event.target.value === ""
                        ? null
                        : Number(event.target.value),
                  })
                }
              />
            </Field>
            <Field label={t("modelsPage.grayPromote")}>
              <input
                type="number"
                min={1}
                max={100000}
                placeholder={t("modelsPage.inherit")}
                value={form.stable_first_promote_requests ?? ""}
                onChange={(event) =>
                  patch({
                    stable_first_promote_requests:
                      event.target.value === ""
                        ? null
                        : Number(event.target.value),
                  })
                }
              />
            </Field>
          </div>
        </section>
      ) : null}
      {members.length > 0 ? (
        <label className="check">
          <input
            type="checkbox"
            checked={pinPriority}
            onChange={(event) => setPinPriority(event.target.checked)}
          />
          <span>
            <strong>{t("routing.independentLabel")}</strong>
            <InfoTip label={t("routing.independentHint")} />
          </span>
        </label>
      ) : null}
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}
