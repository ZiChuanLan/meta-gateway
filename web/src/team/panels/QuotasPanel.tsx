import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { ActionMenu } from "../../components/ActionMenu";
import { Button, DataTable, ErrorState, Field, Panel } from "../../components/ui";
import { QuotaMeters } from "../QuotaMeters";
import { TeamModal } from "../ui";
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
 *
 * It reads as two panels — the groups an operator can create, and the roster's
 * own ceilings — instead of four headings stacked on one column, and every
 * budget renders through the same meter the member table uses.
 */
export function QuotasPanel() {
  const { request, t } = useUsers();
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
  const failure = error || groups.error || members.error;

  return (
    <div className="team-board">
      <p className="panel-hint">{t("quotasIntro")}</p>
      {failure ? <ErrorState error={failure} /> : null}
      <Panel
        title={t("groupQuotas")}
        titleHelp={t("groupQuotasHint")}
        actions={
          <Button
            icon={<Plus size={14} />}
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
          </Button>
        }
      >
        <DataTable headers={[t("name"), t("quota"), t("rpm"), ""]} empty={!groups.data?.length}>
          {groups.data?.map((g) => (
            <tr key={g.name}>
              <td>{g.name}</td>
              <td>
                <QuotaMeters
                  usedTokens={g.quota_used_tokens}
                  totalTokens={g.quota_total_tokens}
                  usedCost={g.quota_used_cost}
                  totalCost={g.quota_total_cost}
                  onEdit={() => setEditing(structuredClone(g))}
                />
              </td>
              <td>{g.rate_per_minute || "—"}</td>
              <td className="row-actions">
                <ActionMenu
                  compact
                  label={t("moreActions")}
                  items={[
                    {
                      key: "edit",
                      label: t("edit"),
                      onSelect: () => setEditing(structuredClone(g)),
                    },
                    ...(g.name === "default"
                      ? []
                      : [
                          {
                            key: "delete",
                            group: t("dangerZone"),
                            label: t("delete"),
                            danger: true,
                            disabled: busy,
                            onSelect: () => {
                              if (confirm(t("groupDeleteWarning", { name: g.name })))
                                void run(`/admin/groups/${encodeURIComponent(g.name)}`, "DELETE");
                            },
                          },
                        ]),
                  ]}
                />
              </td>
            </tr>
          ))}
        </DataTable>
      </Panel>
      <Panel title={t("memberQuotas")} titleHelp={t("memberQuotasHint")}>
        <DataTable headers={[t("name"), t("quota")]} empty={!members.data?.length}>
          {members.data?.map((u) => (
            <tr key={u.id}>
              <td>
                <span className="member-ident">
                  <span className="member-avatar" aria-hidden="true">
                    {(u.name || u.username || "?").trim().slice(0, 1).toUpperCase()}
                  </span>
                  <span className="member-ident-text">
                    <strong>{u.name}</strong>
                    <small>{u.username}</small>
                  </span>
                </span>
              </td>
              <td>
                <QuotaMeters
                  usedTokens={u.quota_used_tokens}
                  totalTokens={u.quota_total_tokens}
                  usedCost={u.quota_used_cost}
                  totalCost={u.quota_total_cost}
                />
              </td>
            </tr>
          ))}
        </DataTable>
      </Panel>
      {editing && <GroupDialog group={editing} onClose={() => setEditing(null)} />}
    </div>
  );
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
        <div className="meta-form">
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
          <p role="alert" className="inline-error">
            {teamError(error, locale)}
          </p>
        ) : null}
        <div className="form-actions">
          <Button variant="quiet" type="button" onClick={onClose}>
            {t("close")}
          </Button>
          <Button type="submit" loading={busy}>
            {t("save")}
          </Button>
        </div>
      </form>
    </TeamModal>
  );
}
