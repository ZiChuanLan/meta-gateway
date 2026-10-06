import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { TeamField as Field, TeamModal } from "../ui";
import { teamError } from "../text";
import { useTeamMutation } from "../useTeamMutation";
import { useUsers } from "../UsersContext";
import type { Candidate, Policy } from "../types";

/** A brand new policy's defaults, matching what the server accepts. */
const EMPTY_POLICY: Policy = {
  id: 0,
  name: "",
  models: [],
  member_ids: [],
  all_models: false,
  max_keys: 5,
  rpm: 60,
  allow_routing: false,
  allow_request_preferences: false,
};

/**
 * Access policies: which models a member may call, which upstream members back
 * them, and the per-account limits (keys, requests per minute).
 *
 * A policy is the unit the operator assigns to a member, so this board is where
 * "what is this person allowed to do" is decided — the members board only picks
 * which policy applies.
 */
export function PoliciesPanel() {
  const { request, locale, t } = useUsers();
  const { busy, error, run } = useTeamMutation(request, t);
  const [editing, setEditing] = useState<Policy | null>(null);
  const policies = useQuery({
    queryKey: ["team", "policies"],
    queryFn: ({ signal }) => request<Policy[]>("/admin/team/policies", { signal }),
  });
  const candidates = useQuery({
    queryKey: ["team", "candidates"],
    queryFn: ({ signal }) => request<Candidate[]>("/admin/team/candidates", { signal }),
    enabled: editing !== null,
  });

  return (
    <div>
      {Boolean(error || policies.error) && (
        <div role="alert" className="team-error">
          {teamError(error || policies.error, locale)}
        </div>
      )}
      <div className="team-head">
        <p className="team-muted">{t("policiesHint")}</p>
        <button className="team-button primary" onClick={() => setEditing({ ...EMPTY_POLICY })}>
          {t("newPolicy")}
        </button>
      </div>
      <div className="team-table-wrap">
        <table className="team-table">
          <thead>
            <tr>
              <th>{t("name")}</th>
              <th>{t("candidates")}</th>
              <th>{t("maxKeys")}</th>
              <th>{t("routing")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {policies.data?.map((p) => (
              <tr key={p.id}>
                <td>{p.name}</td>
                <td>{p.member_ids.length}</td>
                <td>{p.max_keys}</td>
                <td>{p.allow_routing ? t("on") : t("off")}</td>
                <td>
                  <div className="team-actions">
                    <button className="team-button" onClick={() => setEditing(structuredClone(p))}>
                      {t("edit")}
                    </button>
                    {/* Policy #1 is the shipped default every member starts on;
                        deleting it would leave accounts pointing at nothing. */}
                    {p.id !== 1 && (
                      <button
                        className="team-button danger"
                        disabled={busy}
                        onClick={() => {
                          if (confirm(t("deletionWarning")))
                            void run(`/admin/team/policies/${p.id}`, "DELETE");
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
      {editing && (
        <TeamModal title={t("policy")} busy={busy} onClose={() => setEditing(null)}>
          <form
            className="team-surface"
            onSubmit={(e) => {
              e.preventDefault();
              if (confirm(t("policyChanged")))
                void run(
                  `/admin/team/policies${editing.id ? `/${editing.id}` : ""}`,
                  editing.id ? "PUT" : "POST",
                  {
                    ...editing,
                    models: editing.models.map((m) => m.trim()).filter(Boolean),
                  },
                  () => setEditing(null),
                );
            }}
          >
            <Field label={t("name")}>
              <input
                value={editing.name}
                required
                maxLength={80}
                onChange={(e) => setEditing({ ...editing, name: e.target.value })}
              />
            </Field>
            <div className="team-grid">
              <Field label={t("maxKeys")}>
                <input
                  type="number"
                  min={1}
                  max={100}
                  required
                  value={editing.max_keys}
                  onChange={(e) => setEditing({ ...editing, max_keys: Number(e.target.value) })}
                />
              </Field>
              <Field label={t("rpm")}>
                <input
                  type="number"
                  min={1}
                  max={100000}
                  required
                  value={editing.rpm}
                  onChange={(e) => setEditing({ ...editing, rpm: Number(e.target.value) })}
                />
              </Field>
            </div>
            <label className="team-check">
              <input
                type="checkbox"
                checked={editing.allow_routing}
                onChange={(e) => setEditing({ ...editing, allow_routing: e.target.checked })}
              />
              {t("routing")}
            </label>
            <label className="team-check">
              <input
                type="checkbox"
                checked={editing.allow_request_preferences}
                onChange={(e) =>
                  setEditing({
                    ...editing,
                    allow_request_preferences: e.target.checked,
                  })
                }
              />
              {t("allowRequestControls")}
            </label>
            <label className="team-check">
              <input
                type="checkbox"
                checked={editing.all_models}
                onChange={(e) => setEditing({ ...editing, all_models: e.target.checked })}
              />
              {t("allModels")}
            </label>
            {!editing.all_models && (
              <Field label={t("modelList")}>
                <textarea
                  value={editing.models.join("\n")}
                  onChange={(e) => setEditing({ ...editing, models: e.target.value.split("\n") })}
                />
              </Field>
            )}
            <h3>{t("candidates")}</h3>
            <p className="team-muted">{t("candidatesHint")}</p>
            <div className="team-candidates">
              {candidates.data?.length ? (
                candidates.data.map((c) => (
                  <label className="team-check" key={c.id}>
                    <input
                      type="checkbox"
                      checked={editing.member_ids.includes(c.id)}
                      onChange={(e) =>
                        setEditing({
                          ...editing,
                          member_ids: e.target.checked
                            ? [...editing.member_ids, c.id]
                            : editing.member_ids.filter((id) => id !== c.id),
                          // Granting a member also grants the model it serves:
                          // the two lists describe one decision, so they move
                          // together instead of leaving the model unreachable.
                          models: e.target.checked
                            ? [...new Set([...editing.models.filter(Boolean), c.model])]
                            : editing.models,
                        })
                      }
                    />
                    <span>
                      {c.model} · {c.name}{" "}
                      <small>
                        #{c.id}
                        {c.enabled ? "" : ` · ${t("off")}`}
                      </small>
                    </span>
                  </label>
                ))
              ) : (
                <p className="team-muted">{t("noCandidates")}</p>
              )}
            </div>
            {!!error && (
              <div role="alert" className="team-error">
                {teamError(error, locale)}
              </div>
            )}
            <button className="team-button primary" disabled={busy}>
              {busy ? t("saving") : t("save")}
            </button>
          </form>
        </TeamModal>
      )}
    </div>
  );
}
