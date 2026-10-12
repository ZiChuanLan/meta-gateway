import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../api/client";
import { Button, Dialog, ErrorState, Field } from "../../components/ui";
import { useSession } from "../../session";
import { useI18n } from "../../i18n";
import { nativeAmount, type BillingView, type PriceQuote } from "./pricingTypes";

type CopyTarget = { scope: string; key: string; revision: number };

/** A quote is read-only evidence until a versioned copy is explicitly submitted. */
export function QuoteActions({
  quote,
  target,
  current,
  inUse = false,
}: {
  quote: PriceQuote;
  target?: CopyTarget;
  current?: BillingView["effective_rates"];
  /** The rule now in force was copied from this quote. */
  inUse?: boolean;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const qc = useQueryClient();
  const [copyTarget, setCopyTarget] = useState<
    (CopyTarget & { quote: PriceQuote; current?: BillingView["effective_rates"] }) | null
  >(null);
  const [conversion, setConversion] = useState("");
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
        <div className="quote-entry-title">
          <strong>
            {t(quote.purpose === "reference" ? "billing.reference" : "billing.upstream")}
          </strong>
          {inUse ? <span className="quote-chip is-current">{t("billing.inUse")}</span> : null}
        </div>
        <span className="quote-unit">
          {quote.unit.kind === "iso" ? quote.unit.code : t("billing.credit")}
        </span>
      </header>
      {/* Who published this price, and when it was read — a footnote, so the
          numbers below it keep the weight. */}
      <p className="quote-entry-meta">
        {quote.source}
        {quote.account_id ? ` · ${t("billing.account", { id: quote.account_id })}` : ""}
        {quote.upstream_group ? ` · ${t("billing.group", { group: quote.upstream_group })}` : ""}
        {` · ${t("billing.observed", { time: new Date(quote.observed_at).toLocaleString() })}`}
        {quote.unit.display_currency
          ? ` · ${t("billing.displayOnly", { currency: quote.unit.display_currency })}`
          : ""}
      </p>
      {!calculable ? (
        <p role="status" className="quote-entry-note">
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
      {target ? (
        <div className="quote-entry-actions">
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
        </div>
      ) : null}
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
