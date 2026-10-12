import { useCallback, useEffect, useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../api/client";
import { Button, Dialog, Empty, ErrorState, Field, Loading } from "../../components/ui";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";
import { formatUnitPrice, relativeTime } from "../../lib/format";
import { useUnsavedChanges } from "../../lib/unsavedChanges";
import {
  PriceTiersEditor,
  PriceWindowsEditor,
  blankTier,
  blankWindow,
  validTiers,
  validWindows,
} from "./PricingEditors";
import { ModelRatioDialog } from "./ModelRatioDialog";
import { ManualQuoteDialog } from "./ManualQuoteDialog";
import { QuoteActions } from "./QuoteActions";
import type { BillingRule, BillingView, PriceQuote } from "./pricingTypes";
import { nativeAmount } from "./pricingTypes";

type Target = {
  model: string;
  memberId?: number;
  siteId?: number;
  upstreamModel?: string;
  /** The channel this member belongs to, for the layer explanation. */
  channelName?: string;
};
const tokenFields = [
  "input_per_1k",
  "output_per_1k",
  "cache_per_1k",
  "cache_write_per_1k",
] as const;
const fieldLabels = ["billing.input", "billing.output", "billing.cacheRead", "billing.cacheWrite"];
const amount = (raw: string) => (raw.trim() === "" ? undefined : Number(raw));

export function BillingRuleDialog({ onClose, ...target }: Target & { onClose: () => void }) {
  const { t } = useI18n();
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const guard = useUnsavedChanges(dirty);
  const close = () => {
    if (!busy && guard()) onClose();
  };
  return (
    <Dialog title={t("billing.title", { name: target.model })} onClose={close} busy={busy}>
      <BillingRulePanel
        {...target}
        onClose={close}
        onDirtyChange={setDirty}
        onBusyChange={setBusy}
      />
    </Dialog>
  );
}

/** One explicit policy editor, separate from model capabilities and observations. */
export function BillingRulePanel({
  model,
  memberId,
  siteId,
  upstreamModel,
  channelName,
  onClose,
  onDirtyChange,
  onBusyChange,
}: Target & {
  onClose?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  onBusyChange?: (busy: boolean) => void;
}) {
  const { client } = useSession();
  const { t } = useI18n();

  const [editorDirty, setEditorDirty] = useState(false);
  const [manualOpen, setManualOpen] = useState(false);
  const [ratioOpen, setRatioOpen] = useState(false);
  const scope = memberId ? "member" : "model";
  const key = memberId ? String(memberId) : model;
  const updateDirty = useCallback(
    (dirty: boolean) => {
      setEditorDirty(dirty);
      onDirtyChange?.(dirty);
    },
    [onDirtyChange],
  );
  const url = `/admin/billing-rule?${new URLSearchParams({ scope, key })}`;
  const view = useQuery({
    queryKey: ["billing-rule", scope, key],
    queryFn: ({ signal }) => client!.get<BillingView>(url, signal),
  });
  const quotes = useQuery({
    queryKey: ["price-quotes", upstreamModel ?? model, siteId],
    queryFn: ({ signal }) =>
      client!.get<{ items: PriceQuote[] }>(
        `/admin/price-quotes?${new URLSearchParams({ model: upstreamModel ?? model, ...(siteId ? { site_id: String(siteId) } : {}) })}`,
        signal,
      ),
  });
  // The three sources collapse into one line the operator can read without
  // opening anything: what the newest price says, when it was read, and how
  // many more there are. The list itself (with the explicit "use as my price"
  // action) is one click away in the evidence area.
  const quoteIsUsable = (quote: PriceQuote) =>
    quote.status === "calculable" &&
    (!quote.expires_at || new Date(quote.expires_at).getTime() > Date.now());
  const quoteList = quotes.data?.items ?? [];
  const newestQuote = [...quoteList].sort(
    (a, b) => Date.parse(b.observed_at) - Date.parse(a.observed_at),
  )[0];
  const evidenceLine = (() => {
    if (quotes.isPending) return t("common.loading");
    if (!newestQuote) return t("billing.noQuotes");
    const amount =
      newestQuote.values.input_per_million !== undefined
        ? `${nativeAmount(newestQuote.values.input_per_million, newestQuote.unit, t("billing.credit"))} /1M`
        : nativeAmount(newestQuote.values.per_request, newestQuote.unit, t("billing.credit"));
    return [
      t(newestQuote.purpose === "reference" ? "billing.reference" : "billing.upstream"),
      amount,
      relativeTime(newestQuote.observed_at, t),
      quoteList.length > 1 ? t("billing.evidenceMore", { count: quoteList.length - 1 }) : "",
      quoteIsUsable(newestQuote)
        ? ""
        : t("billing.quoteUnavailable", { status: t(`billing.reason.${newestQuote.status}`) }),
    ]
      .filter(Boolean)
      .join(" · ");
  })();
  return (
    <section className="billing-workspace">
      {manualOpen && siteId ? (
        <ManualQuoteDialog
          model={upstreamModel ?? model}
          siteId={siteId}
          onClose={() => setManualOpen(false)}
        />
      ) : null}
      <p className="panel-hint">{t("billing.description")}</p>
      {ratioOpen ? <ModelRatioDialog model={model} onClose={() => setRatioOpen(false)} /> : null}
      {view.isPending ? (
        <Loading />
      ) : view.isError ? (
        <ErrorState error={view.error} retry={() => void view.refetch()} />
      ) : view.data ? (
        <BillingEditor
          key={`${scope}:${key}`}
          view={view.data}
          scope={scope}
          url={url}
          model={model}
          channelName={channelName}
          onClose={onClose}
          onDirtyChange={updateDirty}
          onBusyChange={onBusyChange}
          onEditRatio={() => setRatioOpen(true)}
        />
      ) : null}
      <details className="billing-evidence">
        <summary>
          <strong>{t("billing.quotes")}</strong>
          <span>{evidenceLine}</span>
        </summary>
        <div className="billing-evidence-actions">
          {siteId ? (
            <Button variant="secondary" disabled={editorDirty} onClick={() => setManualOpen(true)}>
              {t("billing.manual")}
            </Button>
          ) : null}
          <Button
            variant="quiet"
            onClick={() => void quotes.refetch()}
            disabled={quotes.isFetching}
          >
            {t("common.refresh")}
          </Button>
        </div>
        <p className="panel-hint">{t("billing.quoteHint")}</p>
        {quotes.isPending ? (
          <Loading />
        ) : quotes.isError ? (
          <ErrorState error={quotes.error} retry={() => void quotes.refetch()} />
        ) : !quoteList.length ? (
          <Empty>{t("billing.noQuotes")}</Empty>
        ) : (
          quoteList.map((quote) => (
            <QuoteActions
              key={quote.scope_key ?? quote.id}
              quote={quote}
              inUse={
                view.data?.rule?.source === "quote_copy" && view.data?.rule?.quote_id === quote.id
              }
              target={
                !editorDirty && view.data ? { scope, key, revision: view.data.revision } : undefined
              }
              current={view.data?.effective_rates}
            />
          ))
        )}
      </details>
    </section>
  );
}

function BillingEditor({
  view,
  scope,
  url,
  model,
  channelName,
  onClose,
  onDirtyChange,
  onBusyChange,
  onEditRatio,
}: {
  view: BillingView;
  scope: string;
  url: string;
  model: string;
  channelName?: string;
  onClose?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  onBusyChange?: (busy: boolean) => void;
  onEditRatio?: () => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();

  const qc = useQueryClient();
  const initial = (() => {
    const r = view.rule;
    const rates = view.effective_rates;
    const mode =
      r?.mode ??
      (rates.per_request > 0
        ? rates.input_per_1k > 0 || rates.output_per_1k > 0
          ? "hybrid"
          : "request"
        : "token");
    return {
      state: r?.state ?? (view.effective_state === "legacy" ? "explicit" : "inherit"),
      mode,
      rates: tokenFields.map((k) =>
        r ? (r[k] === undefined ? "" : String(r[k]! * 1000)) : String(rates[k] * 1000),
      ),
      fee: String(r?.per_request ?? rates.per_request),
      timezone:
        r?.timezone ??
        (rates.timezone && rates.timezone !== "Local"
          ? rates.timezone
          : Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"),
      tiers: r?.tiers ?? view.tiers ?? [],
      windows: r?.windows ?? view.windows ?? [],
    };
  })();
  const [draft, setDraft] = useState(initial);
  const [baseline, setBaseline] = useState(JSON.stringify(initial));
  const [revision, setRevision] = useState(view.revision);
  const dirty = JSON.stringify(draft) !== baseline;
  const guardLeave = useUnsavedChanges(dirty);
  const timezoneId = useId();
  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);
  const save = useMutation({
    mutationFn: (rule: BillingRule) =>
      client!.request<BillingRule>(url, {
        method: "PUT",
        body: JSON.stringify({ rule, expected_revision: revision }),
      }),
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409)
        void qc.invalidateQueries({ queryKey: ["billing-rule", scope] });
    },
    onSuccess: async (rule) => {
      setRevision(rule.revision);
      setBaseline(JSON.stringify(draft));
      await Promise.all(
        ["billing-rule", "model-pricing", "route-overviews", "site-probe"].map((key) =>
          qc.invalidateQueries({ queryKey: [key] }),
        ),
      );
    },
  });
  useEffect(() => {
    onBusyChange?.(save.isPending);
  }, [save.isPending, onBusyChange]);
  useEffect(() => {
    if (!dirty && view.revision > revision) {
      setDraft(initial);
      setBaseline(JSON.stringify(initial));
      setRevision(view.revision);
    }
    // A server update is adopted only while clean. Dirty drafts keep their original version.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view.revision, dirty, revision]);
  const invalidNumber = (raw: string, required: boolean) =>
    raw.trim() === "" ? required : !Number.isFinite(Number(raw)) || Number(raw) < 0;
  let valid =
    draft.state !== "explicit" ||
    (!invalidNumber(draft.fee, draft.mode !== "token") &&
      validTiers(draft.tiers) &&
      validWindows(draft.windows) &&
      (draft.mode === "request" ||
        draft.rates.every(
          (raw, i) =>
            !invalidNumber(raw, i < 2 && draft.tiers.filter((x) => !blankTier(x)).length === 0),
        )));
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: draft.timezone }).format();
  } catch {
    valid = false;
  }
  const submit = () => {
    const rule: BillingRule = {
      state: draft.state,
      mode: draft.mode,
      timezone: draft.timezone,
      source: "manual",
      revision,
    };
    if (draft.state === "explicit") {
      if (draft.mode !== "request") {
        tokenFields.forEach((key, i) => {
          const n = amount(draft.rates[i]!);
          if (n !== undefined) rule[key] = n / 1000;
        });
        rule.tiers = draft.tiers.filter((x) => !blankTier(x));
      }
      if (draft.mode !== "token") rule.per_request = amount(draft.fee);
      rule.windows = draft.windows.filter((x) => !blankWindow(x));
    }
    save.mutate(rule);
  };
  // The one question this panel exists to answer, in rates rather than in rule
  // vocabulary: what a request costs right now. The rule's own words
  // (inherit/free/explicit) move to the line underneath, where they explain the
  // answer instead of being it.
  const rates = view.effective_rates;
  const currentPrice = (() => {
    if (view.effective_state === "unpriced") return t("billing.unpriced");
    if (view.effective_state === "free") return t("billing.free");
    const pieces: string[] = [];
    // A per-request-only rule says nothing through token rates, so they are
    // shown only when they are the price (or when they are zero, which is still
    // the honest answer for a rule that charges nothing per token).
    if (rates.per_request === 0 || rates.input_per_1k > 0 || rates.output_per_1k > 0)
      pieces.push(
        t("billing.ratesToken", {
          input: formatUnitPrice(rates.input_per_1k * 1000),
          output: formatUnitPrice(rates.output_per_1k * 1000),
        }),
      );
    if (rates.per_request > 0)
      pieces.push(t("billing.ratesRequest", { fee: formatUnitPrice(rates.per_request) }));
    return t("billing.currentPrice", { rates: pieces.join(" · ") });
  })();
  return (
    <form
      className="billing-editor"
      onSubmit={(event) => {
        event.preventDefault();
        if (valid && (dirty || (!view.rule && draft.state === "explicit"))) submit();
      }}
    >
      <div className="billing-readout" role="status">
        <span>{currentPrice}</span>
        <small>
          {t(`billing.${view.effective_state}`)} ·{" "}
          {t("billing.scope", {
            scope: t(scope === "member" ? "billing.memberScope" : "billing.modelScope"),
            revision,
          })}
        </small>
        {/* Two layers, and what a layer is for: the model default prices every
            channel serving this model; a member override exists for the one
            channel that must be priced differently. The internal owner id is
            deliberately not shown — it is not something an operator acts on. */}
        {/* The multiplier belongs with the price it scales: it rides the price
            line's right edge, above the layer explanation. */}
        {onEditRatio ? (
          <Button
            variant="quiet"
            className="billing-ratio-button"
            disabled={save.isPending}
            onClick={onEditRatio}
          >
            {t("billing.multiplier")}
          </Button>
        ) : null}
        <small className="billing-layers">
          {t(scope === "member" ? "billing.layers.member" : "billing.layers.model", {
            name: model,
            channel: channelName ?? "",
          })}
        </small>
      </div>
      {/* One way in to change it: the rule editor opens here instead of filling
          the panel before the operator asked for it. */}
      <details className="billing-change">
        <summary>
          <strong>{t("billing.change")}</strong>
          <span>{t("billing.changeHint")}</span>
        </summary>
        <fieldset disabled={save.isPending}>
          <Field
            label={t("billing.state")}
            hint={t(scope === "member" ? "billing.inheritHint" : "billing.inheritHintModel")}
          >
            <select
              value={draft.state}
              onChange={(e) =>
                setDraft({ ...draft, state: e.target.value as BillingRule["state"] })
              }
            >
              <option value="inherit">
                {t(scope === "member" ? "billing.inherit" : "billing.inheritModel")}
              </option>
              <option value="free">{t("billing.free")}</option>
              <option value="explicit">
                {t(scope === "member" ? "billing.explicit" : "billing.explicitModel")}
              </option>
            </select>
          </Field>
          {draft.state === "explicit" ? (
            <>
              <Field label={t("billing.mode")}>
                <select
                  value={draft.mode}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      mode: e.target.value as BillingRule["mode"],
                      tiers: e.target.value === "request" ? [] : draft.tiers,
                    })
                  }
                >
                  <option value="token">{t("billing.token")}</option>
                  <option value="request">{t("billing.request")}</option>
                  <option value="hybrid">{t("billing.hybrid")}</option>
                </select>
              </Field>
              <div className="billing-rate-grid">
                {draft.mode !== "request"
                  ? tokenFields.map((key, i) => (
                      <Field key={key} label={t(fieldLabels[i]!)}>
                        <input
                          type="number"
                          min={0}
                          step="any"
                          value={draft.rates[i]}
                          placeholder={t("billing.missing")}
                          aria-invalid={invalidNumber(
                            draft.rates[i]!,
                            i < 2 && draft.tiers.length === 0,
                          )}
                          onChange={(e) =>
                            setDraft({
                              ...draft,
                              rates: draft.rates.map((v, index) =>
                                index === i ? e.target.value : v,
                              ),
                            })
                          }
                        />
                      </Field>
                    ))
                  : null}
                {draft.mode !== "token" ? (
                  <Field label={t("billing.fee")}>
                    <input
                      type="number"
                      min={0}
                      step="any"
                      value={draft.fee}
                      onChange={(e) => setDraft({ ...draft, fee: e.target.value })}
                    />
                  </Field>
                ) : null}
              </div>
              <p className="field-hint">{t("billing.presenceHint")}</p>
            </>
          ) : null}
          <Field label={t("billing.timezone")}>
            <input
              value={draft.timezone}
              onChange={(e) => setDraft({ ...draft, timezone: e.target.value })}
              list={timezoneId}
            />
            <datalist id={timezoneId}>
              <option value="UTC" />
              <option value="Asia/Shanghai" />
            </datalist>
          </Field>
          {draft.state === "explicit" ? (
            <details className="billing-advanced">
              <summary>{t("billing.advanced")}</summary>
              {draft.mode !== "request" ? (
                <PriceTiersEditor
                  value={draft.tiers}
                  onChange={(tiers) => setDraft({ ...draft, tiers })}
                  displayScale={1000}
                />
              ) : null}
              <PriceWindowsEditor
                value={draft.windows}
                onChange={(windows) => setDraft({ ...draft, windows })}
              />
            </details>
          ) : null}
        </fieldset>
        {save.isError ? (
          save.error instanceof ApiError && save.error.status === 409 ? (
            <p role="alert" className="inline-error">
              {t("billing.conflict")}
            </p>
          ) : (
            <ErrorState error={save.error} />
          )
        ) : null}
        {save.isSuccess && !dirty ? (
          <p role="status" className="field-hint">
            {t("billing.saved")}
          </p>
        ) : null}
        <div className="editor-footer">
          {onClose ? (
            <Button
              variant="secondary"
              disabled={save.isPending}
              onClick={() => {
                onClose();
              }}
            >
              {t("common.close")}
            </Button>
          ) : null}
          <Button
            variant="quiet"
            disabled={save.isPending}
            onClick={() => {
              if (!guardLeave()) return;
              setDraft(initial);
              setBaseline(JSON.stringify(initial));
              setRevision(view.revision);
              save.reset();
            }}
          >
            {t("billing.reload")}
          </Button>
          <Button
            type="submit"
            loading={save.isPending}
            disabled={(!dirty && (Boolean(view.rule) || draft.state !== "explicit")) || !valid}
          >
            {t("common.save")}
          </Button>
        </div>
      </details>
    </form>
  );
}
