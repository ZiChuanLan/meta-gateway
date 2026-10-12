import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button, Dialog, ErrorState, Field } from "../../components/ui";
import { useSession } from "../../session";
import { useI18n } from "../../i18n";
import { useUnsavedChanges } from "../../lib/unsavedChanges";
import type { PriceQuote } from "./pricingTypes";

export function ManualQuoteDialog({
  model,
  siteId,
  onClose,
}: {
  model: string;
  siteId: number;
  onClose: () => void;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const qc = useQueryClient();
  const [unit, setUnit] = useState("site_credit");
  const [mode, setMode] = useState("token");
  const [group, setGroup] = useState("");
  const [rates, setRates] = useState(["", "", "", ""]);
  const [fee, setFee] = useState("");
  const dirty =
    unit !== "site_credit" ||
    mode !== "token" ||
    group !== "" ||
    rates.some((x) => x !== "") ||
    fee !== "";
  const guard = useUnsavedChanges(dirty);
  const values: PriceQuote["values"] = { mode };
  const keys = [
    "input_per_million",
    "output_per_million",
    "cache_read_per_million",
    "cache_write_per_million",
  ] as const;
  if (mode !== "request")
    keys.forEach((key, i) => {
      if (rates[i]!.trim() !== "") values[key] = Number(rates[i]);
    });
  if (mode !== "token" && fee.trim() !== "") values.per_request = Number(fee);
  const numberValid = (v: string, required: boolean) =>
    v.trim() === "" ? !required : Number.isFinite(Number(v)) && Number(v) >= 0 && Number(v) <= 1e9;
  const valid =
    (mode === "request" || rates.every((v, i) => numberValid(v, i < 2))) &&
    (mode === "token" || numberValid(fee, true));
  const save = useMutation({
    mutationFn: () =>
      client!.request<PriceQuote>("/admin/price-quotes", {
        method: "POST",
        body: JSON.stringify({
          model,
          site_id: siteId,
          upstream_group: group.trim(),
          unit: { kind: unit === "site_credit" ? "site_credit" : "iso", code: unit },
          values,
        }),
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["price-quotes"] });
      onClose();
    },
  });
  const close = () => {
    if (!save.isPending && guard()) onClose();
  };
  return (
    <Dialog
      title={t("billing.manualTitle", { model })}
      busy={save.isPending}
      onClose={close}
      actions={
        <>
          <Button variant="secondary" onClick={close}>
            {t("common.cancel")}
          </Button>
          <Button loading={save.isPending} disabled={!valid} onClick={() => save.mutate()}>
            {t("billing.saveQuote")}
          </Button>
        </>
      }
    >
      <p className="panel-hint">{t("billing.manualHint")}</p>
      <Field label={t("billing.unit")}>
        <select value={unit} onChange={(e) => setUnit(e.target.value)}>
          <option value="site_credit">{t("billing.credit")}</option>
          <option value="USD">USD</option>
          <option value="CNY">CNY</option>
        </select>
      </Field>
      <Field label={t("billing.upstreamGroup")}>
        <input value={group} maxLength={64} onChange={(e) => setGroup(e.target.value)} />
      </Field>
      <Field label={t("billing.mode")}>
        <select value={mode} onChange={(e) => setMode(e.target.value)}>
          <option value="token">{t("billing.token")}</option>
          <option value="request">{t("billing.request")}</option>
          <option value="hybrid">{t("billing.hybrid")}</option>
        </select>
      </Field>
      <div className="billing-rate-grid">
        {mode !== "request"
          ? [
              "billing.nativeInput",
              "billing.nativeOutput",
              "billing.nativeRead",
              "billing.nativeWrite",
            ].map((key, i) => (
              <Field key={key} label={t(key)}>
                <input
                  type="number"
                  min={0}
                  step="any"
                  value={rates[i]}
                  placeholder={t("billing.missing")}
                  onChange={(e) =>
                    setRates(rates.map((v, index) => (index === i ? e.target.value : v)))
                  }
                />
              </Field>
            ))
          : null}
        {mode !== "token" ? (
          <Field label={t("billing.nativeFee")}>
            <input
              type="number"
              min={0}
              step="any"
              value={fee}
              onChange={(e) => setFee(e.target.value)}
            />
          </Field>
        ) : null}
      </div>
      {save.isError ? <ErrorState error={save.error} /> : null}
    </Dialog>
  );
}
