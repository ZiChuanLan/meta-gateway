import { useUsers } from "./UsersContext";

/**
 * The two budgets a pool holds, as meters.
 *
 * Every level of the account model (a client token, a tenant group, an account)
 * is limited by the same pair — tokens and money — and whichever runs out first
 * refuses the request. They were printed as "used / total · $used / $total" in
 * three different boards, each with its own formatter, which is how the same
 * number came to look different depending on which page you read it from. One
 * component now renders all of them: the figures, the percentage, and a bar that
 * turns amber past 70% and red past 90%, so "almost out" is visible before
 * somebody hits it.
 */
export function QuotaMeters({
  usedTokens,
  totalTokens,
  usedCost,
  totalCost,
  onEdit,
}: {
  usedTokens: number;
  totalTokens: number;
  usedCost: number;
  totalCost: number;
  /** Supply it and the whole block becomes the control that opens the editor. */
  onEdit?: () => void;
}) {
  const { t } = useUsers();
  const budgets = [
    {
      key: "tokens",
      total: totalTokens,
      used: usedTokens,
      format: (v: number) => v.toLocaleString(),
    },
    { key: "cost", total: totalCost, used: usedCost, format: (v: number) => `$${v.toFixed(2)}` },
  ].filter((budget) => budget.total > 0);
  const body = (
    <span className="quota-meters">
      {budgets.length === 0 ? (
        <span className="quota-unlimited">{t("unlimited")}</span>
      ) : (
        budgets.map((budget) => {
          const percent = Math.max(
            0,
            Math.min(100, Math.round((budget.used / budget.total) * 100)),
          );
          return (
            <span className="quota-meter" key={budget.key}>
              <span className="quota-meter-head">
                <span className="quota-meter-figures">
                  {budget.format(budget.used)} / {budget.format(budget.total)}
                </span>
                <span className="quota-meter-percent">{percent}%</span>
              </span>
              <span
                className="quota-meter-track"
                role="img"
                aria-label={t("quotaUsedPercent", { percent })}
              >
                <span
                  className={
                    "quota-meter-fill" +
                    (percent >= 90 ? " is-critical" : percent >= 70 ? " is-high" : "")
                  }
                  style={{ width: `${percent}%` }}
                />
              </span>
            </span>
          );
        })
      )}
    </span>
  );
  if (!onEdit) return body;
  return (
    <button className="quota-meters-button" onClick={onEdit} title={t("editQuota")}>
      {body}
    </button>
  );
}
