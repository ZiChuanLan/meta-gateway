import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ActionMenu } from "../../components/ActionMenu";
import {
  Button,
  DataTable,
  Dialog,
  ErrorState,
  Field,
  Panel,
  StatusBadge,
} from "../../components/ui";
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
 * which policy applies. It reads as one panel of policies (the create action in
 * the panel header), and the editor is the console's own dialog rather than the
 * module's private one.
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
  const failure = error || policies.error;

  return (
    <div className="team-board">
      {failure ? <ErrorState error={failure} /> : null}
      <Panel
        title={t("policies")}
        titleHelp={t("policiesHint")}
        actions={<Button onClick={() => setEditing({ ...EMPTY_POLICY })}>{t("newPolicy")}</Button>}
      >
        <DataTable
          headers={[t("name"), t("candidates"), t("maxKeys"), t("routing"), ""]}
          empty={!policies.data?.length}
        >
          {policies.data?.map((p) => (
            <tr key={p.id}>
              <td>{p.name}</td>
              <td>{p.member_ids.length}</td>
              <td>{p.max_keys}</td>
              <td>
                <StatusBadge value={p.allow_routing} />
              </td>
              <td className="row-actions">
                <ActionMenu
                  compact
                  label={t("moreActions")}
                  items={[
                    {
                      key: "edit",
                      label: t("edit"),
                      onSelect: () => setEditing(structuredClone(p)),
                    },
                    // Policy #1 is the shipped default every member starts on;
                    // deleting it would leave accounts pointing at nothing.
                    ...(p.id === 1
                      ? []
                      : [
                          {
                            key: "delete",
                            group: t("dangerZone"),
                            label: t("delete"),
                            danger: true,
                            disabled: busy,
                            onSelect: () => {
                              if (confirm(t("deletionWarning")))
                                void run(`/admin/team/policies/${p.id}`, "DELETE");
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
      {editing && (
        <Dialog title={t("policy")} busy={busy} onClose={() => setEditing(null)}>
          <form
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
            <div className="meta-form">
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
            <label className="check marginless">
              <input
                type="checkbox"
                checked={editing.allow_routing}
                onChange={(e) => setEditing({ ...editing, allow_routing: e.target.checked })}
              />
              <span>{t("routing")}</span>
            </label>
            <label className="check marginless">
              <input
                type="checkbox"
                checked={editing.allow_request_preferences}
                onChange={(e) =>
                  setEditing({ ...editing, allow_request_preferences: e.target.checked })
                }
              />
              <span>{t("allowRequestControls")}</span>
            </label>
            <label className="check marginless">
              <input
                type="checkbox"
                checked={editing.all_models}
                onChange={(e) => setEditing({ ...editing, all_models: e.target.checked })}
              />
              <span>{t("allModels")}</span>
            </label>
            {!editing.all_models && (
              <Field label={t("modelList")}>
                <textarea
                  value={editing.models.join("\n")}
                  onChange={(e) => setEditing({ ...editing, models: e.target.value.split("\n") })}
                />
              </Field>
            )}
            <section className="drawer-section">
              <h3>{t("candidates")}</h3>
              <p className="panel-hint">{t("candidatesHint")}</p>
              <div className="team-candidates">
                {candidates.data?.length ? (
                  candidates.data.map((c) => (
                    <label className="check marginless" key={c.id}>
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
                        {c.model} · {c.name} <small>#{c.id}</small>
                        {c.enabled ? null : <StatusBadge value={false} />}
                      </span>
                    </label>
                  ))
                ) : (
                  <p className="panel-hint">{t("noCandidates")}</p>
                )}
              </div>
            </section>
            {error ? (
              <p role="alert" className="inline-error">
                {teamError(error, locale)}
              </p>
            ) : null}
            <div className="form-actions">
              <Button type="submit" loading={busy}>
                {t("save")}
              </Button>
            </div>
          </form>
        </Dialog>
      )}
    </div>
  );
}
