import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../api/client";
import { Button, Dialog, ErrorState, Field } from "../../components/ui";
import { useSession } from "../../session";
import { useI18n } from "../../i18n";
import { nativeAmount, type BillingView, type PriceQuote } from "./pricingTypes";

type CopyTarget = { scope: string; key: string; revision: number };
type Estimate = { status: string; reason?: string; cost_per_call?: number; calls?: number };
const workloadLabels = [
  "billing.inputCount",
  "billing.outputCount",
  "billing.readCount",
  "billing.writeCount",
];
const workloadKeys = ["input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens"];

/** A quote is read-only evidence until a versioned copy is explicitly submitted. */
export function QuoteActions({
  quote,
  target,
  current,
}: {
  quote: PriceQuote;
  target?: CopyTarget;
  current?: BillingView["effective_rates"];
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const qc = useQueryClient();
  const [copyTarget, setCopyTarget] = useState<
    (CopyTarget & { quote: PriceQuote; current?: BillingView["effective_rates"] }) | null
  >(null);
  const [conversion, setConversion] = useState("");
  const [workload, setWorkload] = useState(["1000", "200", "0", "0"]);
  const [balance, setBalance] = useState("");
  const [estimateInput, setEstimateInput] = useState("");
  const expired = Boolean(quote.expires_at && new Date(quote.expires_at).getTime() <= Date.now());
  const calculable = quote.status === "calculable" && !expired;
  const values = quote.values;
  const amount = (value?: number) => nativeAmount(value, quote.unit, t("billing.credit"));
  const quoteFields = [
    ["billing.input", values.input_per_million],
    ["billing.output", values.output_per_million],
    ["billing.cacheRead", values.cache_read_per_million],
    ["billing.cacheWrite", values.cache_write_per_million],
    ["billing.fee", values.per_request],
  ] as const;
  const label = (key: string) => t(key).replace(/\s*\((?:USD).*\)|（USD.*）/g, "");
  const copy = useMutation({
    mutationFn: () =>
      client!.request(
        `/admin/billing-rule/copy-quote?${new URLSearchParams({ scope: copyTarget!.scope, key: copyTarget!.key })}`,
        {
          method: "POST",
          body: JSON.stringify({
            quote_id: copyTarget!.quote.id,
            expected_revision: copyTarget!.revision,
            ...(copyTarget!.quote.unit.kind === "iso" && copyTarget!.quote.unit.code === "USD"
              ? {}
              : { usd_per_unit: Number(conversion) }),
          }),
        },
      ),
    onSuccess: async () => {
      setCopyTarget(null);
      await Promise.all(
        ["billing-rule", "model-pricing", "route-overviews", "site-probe"].map((key) =>
          qc.invalidateQueries({ queryKey: [key] }),
        ),
      );
    },
  });
  const inputSignature = JSON.stringify([workload, balance]);
  const estimate = useMutation({
    mutationFn: (signature: string) => {
      const [counts, available] = JSON.parse(signature) as [string[], string];
      return client!.request<Estimate>("/admin/price-quotes/estimate", {
        method: "POST",
        body: JSON.stringify({
          quote_id: quote.id,
          workload: Object.fromEntries(workloadKeys.map((key, i) => [key, Number(counts[i])])),
          ...(available.trim()
            ? { balance: Number(available), balance_unit: quote.unit.code }
            : {}),
        }),
      });
    },
    onSuccess: (_data, signature) => setEstimateInput(signature),
  });
  const scenarioValid =
    workload.every(
      (v) =>
        v.trim() !== "" && Number.isInteger(Number(v)) && Number(v) >= 0 && Number(v) <= 100000000,
    ) &&
    (!balance.trim() || (Number.isFinite(Number(balance)) && Number(balance) >= 0));
  const copyUSD = copyTarget?.quote.unit.kind === "iso" && copyTarget.quote.unit.code === "USD";
  const multiplier = copyUSD ? 1 : Number(conversion);
  const copyFields = copyTarget
    ? ([
        ["billing.input", copyTarget.quote.values.input_per_million, "input_per_1k"],
        ["billing.output", copyTarget.quote.values.output_per_million, "output_per_1k"],
        ["billing.cacheRead", copyTarget.quote.values.cache_read_per_million, "cache_per_1k"],
        [
          "billing.cacheWrite",
          copyTarget.quote.values.cache_write_per_million,
          "cache_write_per_1k",
        ],
        ["billing.fee", copyTarget.quote.values.per_request, "per_request"],
      ] as const)
    : [];
  const copyCalculable =
    copyTarget?.quote.status === "calculable" &&
    (!copyTarget.quote.expires_at || new Date(copyTarget.quote.expires_at).getTime() > Date.now());
  return (
    <article className="quote-entry">
      <header className="quote-entry-head">
        <div>
          <strong>
            {t(quote.purpose === "reference" ? "billing.reference" : "billing.upstream")}
          </strong>
          <small>{quote.source}</small>
        </div>
        <span>{quote.unit.kind === "iso" ? quote.unit.code : t("billing.credit")}</span>
      </header>
      {quote.unit.display_currency ? (
        <p className="field-hint">
          {t("billing.displayOnly", { currency: quote.unit.display_currency })}
        </p>
      ) : null}
      <p className="field-hint">
        {t("billing.observed", { time: new Date(quote.observed_at).toLocaleString() })}
        {quote.account_id ? ` · ${t("billing.account", { id: quote.account_id })}` : ""}
        {quote.upstream_group ? ` · ${t("billing.group", { group: quote.upstream_group })}` : ""}
      </p>
      {!calculable ? (
        <p role="status" className="field-hint">
          {t("billing.quoteUnavailable", {
            status: t(`billing.reason.${expired ? "stale_quote" : quote.status}`),
          })}
          {quote.raw ? <code>{quote.raw}</code> : null}
        </p>
      ) : null}
      <dl className="quote-rate-list">
        {quoteFields
          .filter(([, value]) => value !== undefined)
          .map(([key, value]) => (
            <div key={key}>
              <dt>
                {label(key)}
                {key !== "billing.fee" ? " /1M" : ""}
              </dt>
              <dd>{amount(value)}</dd>
            </div>
          ))}
      </dl>
      <div className="quote-entry-actions">
        {target ? (
          <Button
            variant="secondary"
            disabled={!calculable}
            onClick={() => {
              setCopyTarget({
                ...target,
                quote: structuredClone(quote),
                current: current ? { ...current } : undefined,
              });
              setConversion("");
              copy.reset();
            }}
          >
            {t("billing.copy")}
          </Button>
        ) : null}
      </div>
      <details className="quote-estimate">
        <summary>{t("billing.estimate")}</summary>
        <p className="field-hint">{t("billing.estimateHint")}</p>
        <div className="billing-rate-grid">
          {workloadLabels.map((key, i) => (
            <Field key={key} label={t(key)}>
              <input
                type="number"
                min={0}
                step={1}
                value={workload[i]}
                onChange={(e) =>
                  setWorkload((values) =>
                    values.map((v, index) => (i === index ? e.target.value : v)),
                  )
                }
              />
            </Field>
          ))}
        </div>
        <Field label={t("billing.balance")}>
          <input
            type="number"
            min={0}
            step="any"
            value={balance}
            onChange={(e) => setBalance(e.target.value)}
          />
        </Field>
        <Button
          variant="secondary"
          loading={estimate.isPending}
          disabled={!calculable || !scenarioValid}
          onClick={() => estimate.mutate(inputSignature)}
        >
          {t("billing.calculate")}
        </Button>
        {estimate.isError ? <ErrorState error={estimate.error} /> : null}
        {estimate.data && estimateInput === inputSignature ? (
          <p role="status" className="estimate-result">
            {estimate.data.status !== "estimated"
              ? t("billing.estimateUnavailable", {
                  reason: t(`billing.reason.${estimate.data.reason}`),
                })
              : estimate.data.calls === undefined
                ? estimate.data.reason
                  ? t("billing.zeroEstimate")
                  : amount(estimate.data.cost_per_call)
                : t("billing.estimateResult", {
                    cost: amount(estimate.data.cost_per_call),
                    calls: estimate.data.calls,
                  })}
          </p>
        ) : null}
      </details>
      {copyTarget ? (
        <Dialog
          title={t("billing.copyTitle")}
          busy={copy.isPending}
          onClose={() => {
            if (!copy.isPending) setCopyTarget(null);
          }}
          actions={
            <>
              <Button variant="secondary" onClick={() => setCopyTarget(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                loading={copy.isPending}
                disabled={!copyCalculable || !Number.isFinite(multiplier) || multiplier <= 0}
                onClick={() => copy.mutate()}
              >
                {t("billing.copyCommit")}
              </Button>
            </>
          }
        >
          <p>{t("billing.copyHint")}</p>
          <p className="field-hint">
            {t("billing.scope", {
              scope: copyTarget.scope === "member" ? `#${copyTarget.key}` : copyTarget.key,
              revision: copyTarget.revision,
            })}
          </p>
          {!copyUSD ? (
            <Field label={t("billing.conversion")} hint={t("billing.conversionHint")}>
              <input
                type="number"
                min={0}
                step="any"
                value={conversion}
                onChange={(e) => setConversion(e.target.value)}
              />
            </Field>
          ) : null}
          <div className="table-wrap">
            <table className="billing-copy-table">
              <thead>
                <tr>
                  <th />
                  <th>{t("billing.current")}</th>
                  <th>{t("billing.target")}</th>
                </tr>
              </thead>
              <tbody>
                {copyFields
                  .filter(([, value]) => value !== undefined)
                  .map(([key, value, field]) => (
                    <tr key={key}>
                      <th>{label(key)}</th>
                      <td>
                        {copyTarget.current
                          ? copyTarget.current[field] * (field === "per_request" ? 1 : 1000)
                          : "—"}{" "}
                        USD
                      </td>
                      <td>
                        {nativeAmount(value, copyTarget.quote.unit, t("billing.credit"))}
                        {!copyUSD && Number.isFinite(multiplier) && multiplier > 0 ? (
                          <small> ≈ {Number((value! * multiplier).toPrecision(10))} USD</small>
                        ) : null}
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
          {copy.isError ? (
            copy.error instanceof ApiError && copy.error.status === 409 ? (
              <p role="alert" className="inline-error">
                {t("billing.conflict")}
              </p>
            ) : (
              <ErrorState error={copy.error} />
            )
          ) : null}
        </Dialog>
      ) : null}
    </article>
  );
}
