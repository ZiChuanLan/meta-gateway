import { useState } from "react";
import { Field } from "../../components/ui";
import { useI18n } from "../../i18n";

type Prices = { price_prompt_per_1k?: number; price_completion_per_1k?: number; price_cache_per_1k?: number; price_per_request?: number };
const fields = [
 ["price_prompt_per_1k", "pricing.storedInput"], ["price_completion_per_1k", "pricing.storedOutput"],
 ["price_cache_per_1k", "pricing.storedCache"], ["price_per_request", "pricing.storedCall"],
] as const;
const validPrice = (raw: string = "") => raw.trim() !== "" && Number.isFinite(Number(raw)) && Number(raw) >= 0;

/** Price amounts are stored USD, not converted display currency. Blank is not zero. */
export function PriceFields({ value, onChange, onValidityChange, disabled }: {
 value: Prices; onChange: (patch: Prices) => void; onValidityChange: (valid: boolean) => void; disabled?: boolean;
}) {
 const { t } = useI18n();
 const [draft, setDraft] = useState(()=>Object.fromEntries(fields.map(([key])=>[key,String(value[key] ?? 0)])));
 return <>
  {fields.map(([key,label]) => <Field key={key} label={t(label)}>
   <input type="number" step="any" min={0} required disabled={disabled} value={draft[key]}
    aria-invalid={!validPrice(draft[key])}
    onChange={(event)=>{
     const next={...draft,[key]:event.target.value}; setDraft(next);
     onValidityChange(Object.values(next).every(validPrice));
     if(validPrice(event.target.value)) onChange({[key]:Number(event.target.value)});
    }} />
  </Field>)}
  <p className="field-hint pricing-unit-notice">{t("pricing.storedNotice")}</p>
 </>;
}

export function PricingRules() {
 const { t } = useI18n();
 return <details className="pricing-rules"><summary>{t("pricing.rulesTitle")}</summary>
  <ol><li>{t("pricing.ruleLayer")}</li><li>{t("pricing.ruleFactors")}</li><li>{t("pricing.ruleCache")}</li><li>{t("pricing.ruleQuota")}</li></ol>
 </details>;
}
