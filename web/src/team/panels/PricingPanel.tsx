import { Link } from "react-router-dom";
import { useI18n } from "../../i18n";
import { PricingRules } from "../../features/models/PriceFields";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ActionMenu } from "../../components/ActionMenu";
import { Button, DataTable, Dialog, ErrorState, Field, Panel } from "../../components/ui";
import { teamError } from "../text";
import { useTeamMutation } from "../useTeamMutation";
import { useUsers } from "../UsersContext";
import type { ModelRatio } from "../types";

/**
 * Billing markup: a per-model multiplier applied on top of whatever unit price
 * the model or the route member carries.
 *
 * It used to be reachable only through the API — a number that changes every
 * account's bill, with no interface and no mention anywhere in the console.
 * The board states the pricing order as well (member price, then model price,
 * then this multiplier), because "why is this number what it is" was the part
 * operators could not answer.
 */
export function PricingPanel() {
  const { request, t } = useUsers();
  const { busy, error, run } = useTeamMutation(request, t);
  const [editing, setEditing] = useState<ModelRatio | null>(null);
  const ratios = useQuery({
    queryKey: ["team", "ratios"],
    queryFn: ({ signal }) => request<ModelRatio[]>("/admin/ratios", { signal }),
  });
  const failure = error || ratios.error;
  return (
    <div className="team-board">
      <p className="panel-hint">{t("pricingIntro")}</p>
      <PricingRules />
      {failure ? <ErrorState error={failure} /> : null}
      <Panel
        title={t("billingRatios")}
        titleHelp={t("billingRatiosHint")}
        actions={
          <Button onClick={() => setEditing({ model: "", ratio: 1 })}>{t("newRatio")}</Button>
        }
      >
        <DataTable headers={[t("model"), t("ratioLabel"), ""]} empty={!ratios.data?.length}>
          {ratios.data?.map((r) => (
            <tr key={r.model}>
              <td>
                <code>{r.model}</code>
              </td>
              <td>× {r.ratio}</td>
              <td className="row-actions">
                <ActionMenu
                  compact
                  label={t("moreActions")}
                  items={[
                    {
                      key: "edit",
                      label: t("edit"),
                      onSelect: () => setEditing(structuredClone(r)),
                    },
                    {
                      key: "delete",
                      group: t("dangerZone"),
                      label: t("delete"),
                      danger: true,
                      disabled: busy,
                      onSelect: () => {
                        if (confirm(t("ratioDeleteWarning", { model: r.model })))
                          void run(
                            `/admin/ratios/${encodeURIComponent(r.model)}`,
                            "PUT",
                            // A negative ratio is the endpoint's documented way
                            // of removing the row (store.SetRatio).
                            { ratio: -1 },
                          );
                      },
                    },
                  ]}
                />
              </td>
            </tr>
          ))}
        </DataTable>
      </Panel>
      <Panel title={t("unitPrices")} titleHelp={t("unitPricesHint")}>
        <Link className="button" to="/models">
          {t("openModelPricing")}
        </Link>
      </Panel>
      {editing && <RatioDialog ratio={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function RatioDialog({ ratio, onClose }: { ratio: ModelRatio; onClose: () => void }) {
  const { request, locale, t } = useUsers();
  const { busy, error, run } = useTeamMutation(request, t);
  const [model, setModel] = useState(ratio.model);
  const [value, setValue] = useState(String(ratio.ratio));
  const { t: ui } = useI18n();
  const creating = ratio.model === "";
  const valid =
    model.trim() !== "" &&
    value.trim() !== "" &&
    Number.isFinite(Number(value)) &&
    Number(value) >= 0 &&
    Number(value) <= 1000;
  return (
    <Dialog
      title={creating ? t("newRatio") : t("ratioTitle", { model: ratio.model })}
      onClose={onClose}
      busy={busy}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!valid || busy) return;
          void run(
            `/admin/ratios/${encodeURIComponent(model.trim())}`,
            "PUT",
            { ratio: Number(value) },
            () => onClose(),
          );
        }}
      >
        <Field label={t("model")}>
          <input
            value={model}
            required
            placeholder="gpt-4o"
            disabled={!creating}
            onChange={(e) => setModel(e.target.value)}
          />
        </Field>
        <Field label={t("ratioLabel")} hint={t("ratioHint")}>
          <input
            type="number"
            required
            min={0}
            max={1000}
            step="0.01"
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
        </Field>
        {value.trim() !== "" && Number(value) === 0 ? (
          <p role="status" className="panel-hint">
            {ui("pricing.zeroRatio")}
          </p>
        ) : null}
        {error ? (
          <p role="alert" className="inline-error">
            {teamError(error, locale)}
          </p>
        ) : null}
        <div className="form-actions">
          <Button variant="quiet" type="button" onClick={onClose}>
            {t("close")}
          </Button>
          <Button type="submit" loading={busy} disabled={!valid}>
            {t("save")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
