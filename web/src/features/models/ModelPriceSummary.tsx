import { useQuery } from "@tanstack/react-query";
import type { TeamRequest } from "../../team/types";
import { Button, Loading } from "../../components/ui";
import { useI18n } from "../../i18n";
import { formatUnitPrice, useCurrency } from "../../lib/format";

type Range = { min: number; max: number };
export type ModelPriceQuote = {
 model: string; currency: string; evaluated_at: string; timezone: string;
 input_tokens: number; candidates: number; ratio: number;
 input_per_1k: Range; output_per_1k: Range; cache_per_1k: Range;
 per_request: Range; tiered: boolean; scheduled: boolean;
};

/** Same presentation and wire contract; only the scoped transport differs. */
export function ModelPriceSummary({ model, scope, request }: {
 model: string; scope: "admin" | "member"; request: TeamRequest;
}) {
 const { t } = useI18n();
 useCurrency();
 const input = 0;
 const quote = useQuery({
  queryKey: ["model-pricing", scope, model, input],
  queryFn: async ({ signal }) => {
    const value = await request<ModelPriceQuote>(`${scope === "admin" ? "/admin" : "/me"}/model-pricing?${new URLSearchParams({model, input_tokens:String(input)})}`, { signal });
    if (!value || value.model !== model || ![value.input_per_1k,value.output_per_1k,value.cache_per_1k,value.per_request].every((range)=>range && Number.isFinite(range.min) && Number.isFinite(range.max))) throw new Error(t("pricing.unavailable"));
    return value;
  },
  staleTime: 60000,

 });
 const range = (value: Range, scale = 1) => value.min === value.max ? formatUnitPrice(value.min * scale) : `${formatUnitPrice(value.min * scale)} – ${formatUnitPrice(value.max * scale)}`;
 return <section className="model-price-summary">
  {quote.isPending ? <Loading /> : quote.isError ? <div role="alert" className="inline-error">{t("pricing.unavailable")} <Button variant="secondary" onClick={()=>void quote.refetch()}>{t("common.retry")}</Button></div> : quote.data ? <>
   <dl className="model-price-grid">
    {[["pricing.input",quote.data.input_per_1k,1000],["pricing.output",quote.data.output_per_1k,1000],...(quote.data.per_request.max > 0 ? [["pricing.perCall",quote.data.per_request,1]] : [])].map(([key,value,scale]) => <div key={String(key)}><dt>{t(String(key))}</dt><dd>{range(value as Range,Number(scale))}</dd></div>)}
   </dl>
   {quote.data.ratio !== 1 ? <p className="field-hint">{t("modelsPage.cardPriceNote",{ratio:quote.data.ratio})}</p> : null}
   {quote.data.tiered || quote.data.scheduled ? <p className="field-hint">{t("modelsPage.cardVariablePrice")}</p> : null}
  </> : null}
 </section>;
}
