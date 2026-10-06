import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { TeamField as Field, TeamModal } from "../ui";
import { teamError } from "../text";
import { useTeamMutation } from "../useTeamMutation";
import { useUsers } from "../UsersContext";
import type { KeyGroup, TeamUser } from "../types";

/**
 * Every budget a request can run into, in one place.
 *
 * A relay call is checked against three separate pools — the client token, the
 * tenant group the token is bound to, and the account behind it — and each pool
 * holds two independent budgets (tokens and money). Whichever runs out first
 * refuses the request. The boards used to expose four of those six numbers and
 * explain none of them; this board is where the model itself is stated, and
 * where the two pools that had no interface at all (tenant groups) finally have
 * one.
 */
export function QuotasPanel() {
  const { request, locale, t } = useUsers();
  const { busy, error, run } = useTeamMutation(request, t);
  const [editing, setEditing] = useState<KeyGroup | null>(null);
  const groups = useQuery({
    queryKey: ["team", "groups"],
    queryFn: ({ signal }) => request<KeyGroup[]>("/admin/groups", { signal }),
  });
  const members = useQuery({
    queryKey: ["team", "users"],
    queryFn: ({ signal }) => request<TeamUser[]>("/admin/team/users", { signal }),
  });

  return (
    <div className="team-board">
      <p className="team-muted">{t("quotasIntro")}</p>
      <h3>{t("groupQuotas")}</h3>
      <p className="team-muted">{t("groupQuotasHint")}</p>
      {Boolean(error || groups.error) && (
        <div role="alert" className="team-error">
          {teamError(error || groups.error, locale)}
        </div>
      )}
      <div className="team-head">
        <span />
        <button
          className="team-button primary"
          onClick={() =>
            setEditing({
              name: "",
              quota_total_tokens: 0,
              quota_used_tokens: 0,
              quota_total_cost: 0,
              quota_used_cost: 0,
              rate_per_minute: 0,
              rate_burst: 0,
            })
          }
        >
          {t("newGroup")}
        </button>
      </div>
      <div className="team-table-wrap">
        <table className="team-table">
          <thead>
            <tr>
              <th>{t("name")}</th>
              <th>{t("quotaTokensShort")}</th>
              <th>{t("quotaCostShort")}</th>
              <th className="team-hide-mobile">{t("rpm")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {groups.data?.map((g) => (
              <tr key={g.name}>
                <td>{g.name}</td>
                <td>{formatPool(g.quota_used_tokens, g.quota_total_tokens, t)}</td>
                <td>{formatCostPool(g.quota_used_cost, g.quota_total_cost, t)}</td>
                <td className="team-hide-mobile">{g.rate_per_minute || "—"}</td>
                <td>
                  <div className="team-actions">
                    <button className="team-button" onClick={() => setEditing(structuredClone(g))}>
                      {t("edit")}
                    </button>
                    {g.name !== "default" && (
                      <button
                        className="team-button danger"
                        disabled={busy}
                        onClick={() => {
                          if (confirm(t("groupDeleteWarning", { name: g.name })))
                            void run(`/admin/groups/${encodeURIComponent(g.name)}`, "DELETE");
                        }}
                      >
                        {t("delete")}
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <h3>{t("memberQuotas")}</h3>
      <p className="team-muted">{t("memberQuotasHint")}</p>
      <div className="team-table-wrap">
        <table className="team-table">
          <thead>
            <tr>
              <th>{t("name")}</th>
              <th>{t("quotaTokensShort")}</th>
              <th>{t("quotaCostShort")}</th>
            </tr>
          </thead>
          <tbody>
            {members.data?.map((u) => (
              <tr key={u.id}>
                <td>
                  {u.name}
                  <small>{u.username}</small>
                </td>
                <td>{formatPool(u.quota_used_tokens, u.quota_total_tokens, t)}</td>
                <td>{formatCostPool(u.quota_used_cost, u.quota_total_cost, t)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {editing && <GroupDialog group={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

/** `used / total`, or the unlimited word when the pool has no ceiling. */
function formatPool(used: number, total: number, t: (k: "unlimited") => string) {
  if (total <= 0) return t("unlimited");
  return `${used.toLocaleString()} / ${total.toLocaleString()}`;
}

function formatCostPool(used: number, total: number, t: (k: "unlimited") => string) {
  if (total <= 0) return t("unlimited");
  return `$${used.toFixed(2)} / $${total.toFixed(2)}`;
}

/**
 * One tenant group's limits.
 *
 * Every field is sent on save, because the endpoint replaces the row rather
 * than patching it: sending only what changed would zero the rest. The dialog
 * therefore starts from the stored values and resubmits all of them.
 */
function GroupDialog({ group, onClose }: { group: KeyGroup; onClose: () => void }) {
  const { request, locale, t } = useUsers();
  const { busy, error, run } = useTeamMutation(request, t);
  const [draft, setDraft] = useState(group);
  const patch = (partial: Partial<KeyGroup>) => setDraft({ ...draft, ...partial });
  const creating = group.name === "";
  return (
    <TeamModal
      title={creating ? t("newGroup") : t("quotaGroupTitle", { name: group.name })}
      onClose={onClose}
      busy={busy}
    >
      <form
        id="group-form"
        onSubmit={(e) => {
          e.preventDefault();
          void run(
            `/admin/groups/${encodeURIComponent(draft.name)}`,
            "PUT",
            {
              quota_total_tokens: draft.quota_total_tokens,
              quota_total_cost: draft.quota_total_cost,
              rate_per_minute: draft.rate_per_minute,
              rate_burst: draft.rate_burst,
            },
            () => onClose(),
          );
        }}
      >
        <Field label={t("name")}>
          <input
            value={draft.name}
            required
            maxLength={64}
            disabled={!creating}
            onChange={(e) => patch({ name: e.target.value })}
          />
        </Field>
        <div className="team-grid">
          <Field label={t("quotaTotal")} hint={t("quotaHint")}>
            <input
              type="number"
              min={0}
              value={draft.quota_total_tokens}
              onChange={(e) => patch({ quota_total_tokens: Number(e.target.value || 0) })}
            />
          </Field>
          <Field label={t("quotaCostTotal")} hint={t("quotaCostHint")}>
            <input
              type="number"
              min={0}
              step="0.01"
              value={draft.quota_total_cost}
              onChange={(e) => patch({ quota_total_cost: Number(e.target.value || 0) })}
            />
          </Field>
          <Field label={t("rpm")}>
            <input
              type="number"
              min={0}
              value={draft.rate_per_minute}
              onChange={(e) => patch({ rate_per_minute: Number(e.target.value || 0) })}
            />
          </Field>
          <Field label={t("rateBurst")}>
            <input
              type="number"
              min={0}
              value={draft.rate_burst}
              onChange={(e) => patch({ rate_burst: Number(e.target.value || 0) })}
            />
          </Field>
        </div>
        {error ? (
          <div role="alert" className="team-error">
            {teamError(error, locale)}
          </div>
        ) : null}
        <div className="team-actions">
          <button className="team-button quiet" type="button" onClick={onClose}>
            {t("close")}
          </button>
          <button className="team-button primary" disabled={busy}>
            {busy ? t("saving") : t("save")}
          </button>
        </div>
      </form>
    </TeamModal>
  );
}
