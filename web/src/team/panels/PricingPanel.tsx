import { Link } from "react-router-dom";
import { useI18n } from "../../i18n";
import { PricingRules } from "../../features/models/PriceFields";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { TeamField as Field, TeamModal } from "../ui";
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
  const { request, locale, t } = useUsers();
  const { busy, error, run } = useTeamMutation(request, t);
  const [editing, setEditing] = useState<ModelRatio | null>(null);
  const ratios = useQuery({
    queryKey: ["team", "ratios"],
    queryFn: ({ signal }) => request<ModelRatio[]>("/admin/ratios", { signal }),
  });
  return (
    <div className="team-board">
      <p className="team-muted">{t("pricingIntro")}</p>
      <PricingRules />
      {Boolean(error || ratios.error) && (
        <div role="alert" className="team-error">
          {teamError(error || ratios.error, locale)}
        </div>
      )}
      <div className="team-head">
        <h3>{t("billingRatios")}</h3>
        <button
          className="team-button primary"
          onClick={() => setEditing({ model: "", ratio: 1 })}
        >
          {t("newRatio")}
        </button>
      </div>
      <p className="team-muted">{t("billingRatiosHint")}</p>
      {ratios.data?.length ? (
        <div className="team-table-wrap">
          <table className="team-table">
            <thead>
              <tr>
                <th>{t("model")}</th>
                <th>{t("ratioLabel")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {ratios.data.map((r) => (
                <tr key={r.model}>
                  <td>
                    <code>{r.model}</code>
                  </td>
                  <td>× {r.ratio}</td>
                  <td>
                    <div className="team-actions">
                      <button
                        className="team-button"
                        onClick={() => setEditing(structuredClone(r))}
                      >
                        {t("edit")}
                      </button>
                      <button
                        className="team-button danger"
                        disabled={busy}
                        onClick={() => {
                          if (confirm(t("ratioDeleteWarning", { model: r.model })))
                            void run(
                              `/admin/ratios/${encodeURIComponent(r.model)}`,
                              "PUT",
                              // A negative ratio is the endpoint's documented
                              // way of removing the row (store.SetRatio).
                              { ratio: -1 },
                            );
                        }}
                      >
                        {t("delete")}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="team-muted">{t("noRatios")}</p>
      )}
      <h3>{t("unitPrices")}</h3>
      <p className="team-muted">{t("unitPricesHint")}</p>
      <Link className="team-button" to="/models">{t("unitPrices")}</Link>
      {editing && <RatioDialog ratio={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function RatioDialog({
  ratio,
  onClose,
}: {
  ratio: ModelRatio;
  onClose: () => void;
}) {
  const { request, locale, t } = useUsers();
  const { busy, error, run } = useTeamMutation(request, t);
  const [model, setModel] = useState(ratio.model);
  const [value, setValue] = useState(String(ratio.ratio));
  const { t: ui } = useI18n();
  const creating = ratio.model === "";
  const valid = model.trim() !== "" && value.trim() !== "" && Number.isFinite(Number(value)) && Number(value) >= 0 && Number(value) <= 1000;
  return (
    <TeamModal
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
        {value.trim() !== "" && Number(value) === 0 ? <p role="status" className="team-muted">{ui("pricing.zeroRatio")}</p> : null}
        {error ? (
          <div role="alert" className="team-error">
            {teamError(error, locale)}
          </div>
        ) : null}
        <div className="team-actions">
          <button className="team-button quiet" type="button" onClick={onClose}>
            {t("close")}
          </button>
          <button className="team-button primary" disabled={busy || !valid}>
            {busy ? t("saving") : t("save")}
          </button>
        </div>
      </form>
    </TeamModal>
  );
}
