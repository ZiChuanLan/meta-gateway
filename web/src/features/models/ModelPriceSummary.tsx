import { useQuery } from "@tanstack/react-query";
import type { TeamRequest } from "../../team/types";
import { Button, Loading } from "../../components/ui";
import { useI18n } from "../../i18n";
import { formatUnitPrice, useCurrency } from "../../lib/format";

type Range = { min: number; max: number };
/** How a model's candidates split between priced and unpriced layers. */
export type ModelPriceStatus = "priced" | "partially_priced" | "unpriced";
export type ModelPriceQuote = {
  model: string;
  currency: string;
  evaluated_at: string;
  timezone: string;
  input_tokens: number;
  candidates: number;
  ratio: number;
  input_per_1k: Range;
  output_per_1k: Range;
  cache_per_1k: Range;
  per_request: Range;
  tiered: boolean;
  scheduled: boolean;
  /**
   * Candidates whose layer actually prices, and the summary of that split.
   * Optional so an older gateway that omits them keeps rendering the ranges it
   * sent; an `unpriced` model bills at zero only because no layer priced it and
   * must not be shown as a free quote.
   */
  priced_candidates?: number;
  status?: ModelPriceStatus;
  /** Whether the cache prices are declarations, not the input-price fallback. */
  cache_known?: boolean;
  cache_write_known?: boolean;
};

/** One page's reference quotes, as `GET …/model-pricing/batch` returns them. */
export type ModelPriceBatchResponse = {
  items: ModelPriceQuote[];
  unavailable: string[];
};

/**
 * One model's slice of a page-wide batch. When supplied, the batch owns the
 * request and this component only renders it — it never fetches per model.
 */
export type ModelPriceBatch = {
  data?: ModelPriceQuote;
  pending: boolean;
  error?: unknown;
  retry: () => void;
};

/** Same presentation and wire contract; only the scoped transport differs. */
export function ModelPriceSummary({
  model,
  scope,
  request,
  batch,
}: {
  model: string;
  scope: "admin" | "member";
  request: TeamRequest;
  batch?: ModelPriceBatch;
}) {
  const { t } = useI18n();
  useCurrency();
  const input = 0;
  // The hook always runs. A caller that supplies a batch disables this query
  // instead of skipping it, which would break the rules of hooks.
  const quote = useQuery({
    queryKey: ["model-pricing", scope, model, input],
    queryFn: async ({ signal }) => {
      const value = await request<ModelPriceQuote>(
        `${scope === "admin" ? "/admin" : "/me"}/model-pricing?${new URLSearchParams({ model, input_tokens: String(input) })}`,
        { signal },
      );
      if (
        !value ||
        value.model !== model ||
        ![value.input_per_1k, value.output_per_1k, value.cache_per_1k, value.per_request].every(
          (range) => range && Number.isFinite(range.min) && Number.isFinite(range.max),
        )
      )
        throw new Error(t("pricing.unavailable"));
      return value;
    },
    staleTime: 60000,
    enabled: batch === undefined,
  });
  const pending = batch ? batch.pending : quote.isPending;
  const error = batch ? batch.error : quote.isError ? quote.error : undefined;
  const data = batch ? batch.data : quote.data;
  const retry = batch ? batch.retry : () => void quote.refetch();
  const range = (value: Range, scale = 1) =>
    value.min === value.max
      ? formatUnitPrice(value.min * scale)
      : `${formatUnitPrice(value.min * scale)} – ${formatUnitPrice(value.max * scale)}`;
  return (
    <section className="model-price-summary">
      {pending ? (
        <Loading />
      ) : error ? (
        <div role="alert" className="inline-error">
          {t("pricing.unavailable")}{" "}
          <Button variant="secondary" onClick={retry}>
            {t("common.retry")}
          </Button>
        </div>
      ) : !data || data.status === "unpriced" ? (
        // No authorized candidate resolved, or every candidate is unpriced: the
        // zero rates here mean "no price configured", never "free".
        <p className="field-hint">{t("pricing.unavailable")}</p>
      ) : (
        <>
          <dl className="model-price-grid">
            {[
              ["pricing.input", data.input_per_1k, 1000],
              ["pricing.output", data.output_per_1k, 1000],
              ...(data.per_request.max > 0 ? [["pricing.perCall", data.per_request, 1]] : []),
            ].map(([key, value, scale]) => (
              <div key={String(key)}>
                <dt>{t(String(key))}</dt>
                <dd>{range(value as Range, Number(scale))}</dd>
              </div>
            ))}
          </dl>
          {data.ratio !== 1 ? (
            <p className="field-hint">{t("modelsPage.cardPriceNote", { ratio: data.ratio })}</p>
          ) : null}
          {data.status === "partially_priced" ? (
            // Some candidates are unpriced, so a low end of zero reflects an
            // unpriced member rather than a promise that the model is free.
            <p className="field-hint">{t("pricing.referenceHint")}</p>
          ) : null}
          {data.tiered || data.scheduled ? (
            <p className="field-hint">{t("modelsPage.cardVariablePrice")}</p>
          ) : null}
        </>
      )}
    </section>
  );
}
