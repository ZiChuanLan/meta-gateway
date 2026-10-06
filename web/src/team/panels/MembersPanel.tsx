import { useId, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { TeamField as Field } from "../ui";
import { ActionMenu } from "../../components/ActionMenu";
import { Drawer } from "../../components/Drawer";
import { ModelDirectoryToolbar } from "../../components/ModelDirectoryToolbar";
import { QuotaMeters } from "../QuotaMeters";
import {
  Button,
  DataTable,
  ErrorState,
  Loading,
  PageActions,
  StatusBadge,
} from "../../components/ui";
import { ImportMembersDialog, NewMemberDialog, QuotaDialog } from "../MemberForms";
import { teamError } from "../text";
import { useTeamMutation } from "../useTeamMutation";
import { useUsers } from "../UsersContext";
import type { BulkMemberAction, OAuthBinding, Policy, TeamUser, UserKey } from "../types";

/**
 * Members: the roster, one member in detail, and the bulk actions over a
 * selection.
 *
 * The roster is the only board an admin (non-owner) gets, so the owner-only
 * controls are absent here rather than disabled: an admin who cannot change a
 * policy should not be shown a policy picker that does nothing.
 */
export function MembersPanel() {
  const { request, locale, t, owner, settings } = useUsers();
  const enabled = settings.mode === "team";
  const qc = useQueryClient();
  const refresh = () => void qc.invalidateQueries({ queryKey: ["team"] });
  const { busy, error, notice, setNotice, run } = useTeamMutation(request, t);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<TeamUser | null>(null);
  const [checked, setChecked] = useState<Set<number>>(new Set());
  const [newMember, setNewMember] = useState(false);
  const [importing, setImporting] = useState(false);
  const [quotaFor, setQuotaFor] = useState<TeamUser | null>(null);
  const [bulkPolicy, setBulkPolicy] = useState("");
  const [copyMessage, setCopyMessage] = useState("");

  const users = useQuery({
    queryKey: ["team", "users"],
    queryFn: ({ signal }) => request<TeamUser[]>("/admin/team/users", { signal }),
  });
  const policies = useQuery({
    queryKey: ["team", "policies"],
    queryFn: ({ signal }) => request<Policy[]>("/admin/team/policies", { signal }),
  });
  const userKeys = useQuery({
    queryKey: ["team", "user-keys", selected?.id],
    queryFn: ({ signal }) =>
      request<UserKey[]>(`/admin/team/users/${selected!.id}/keys`, { signal }),
    enabled: !!selected,
  });
  const bindings = useQuery({
    queryKey: ["team", "identities", selected?.id],
    queryFn: ({ signal }) =>
      request<OAuthBinding[]>(`/admin/team/users/${selected!.id}/identities`, { signal }),
    enabled: !!selected,
  });

  async function copy(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopyMessage(t("copied"));
    } catch {
      setCopyMessage(t("copyDenied"));
    }
  }

  async function bulk(action: BulkMemberAction, policyID?: number) {
    const ids = [...checked];
    if (!ids.length) return;
    const done = await run("/admin/team/users/bulk", "POST", {
      ids,
      action,
      ...(policyID ? { policy_id: policyID } : {}),
    });
    if (done === undefined) return;
    setChecked(new Set());
    setBulkPolicy("");
  }

  const visibleUsers = (users.data ?? []).filter((u) =>
    `${u.name} ${u.username}`.toLowerCase().includes(search.toLowerCase()),
  );
  const queryError = users.error || policies.error;
  const policyName = (id: number) => policies.data?.find((p) => p.id === id)?.name ?? `#${id}`;

  return (
    <div>
      {Boolean(error || queryError) && (
        <div role="alert" className="team-error">
          {teamError(error || queryError, locale)}
        </div>
      )}
      {notice && (
        <div role="status" className="team-notice">
          {notice.startsWith("http") || notice.startsWith("/") ? (
            <>
              <p>{t("linkOnce")}</p>
              <code>{notice}</code>{" "}
              <button className="team-button" onClick={() => void copy(notice)}>
                {t("copy")}
              </button>
              <small>{copyMessage}</small>
            </>
          ) : (
            notice
          )}
        </div>
      )}
      {/* The roster stays mounted: the drawer edits one member beside the list
          they were found in, instead of replacing the page they were on. */}
      <>
        <PageActions>
          <Button variant="secondary" disabled={!enabled} onClick={() => setImporting(true)}>
            {t("importMembers")}
          </Button>
          <Button disabled={!enabled} onClick={() => setNewMember(true)}>
            ＋ {t("newMember")}
          </Button>
        </PageActions>
        <div className="team-toolbar">
          <ModelDirectoryToolbar value={search} onChange={setSearch} label={t("search")} />
        </div>
        {checked.size > 0 && (
          <BulkBar
            count={checked.size}
            policies={policies.data ?? []}
            owner={owner}
            busy={busy}
            policy={bulkPolicy}
            onPolicy={setBulkPolicy}
            onRun={bulk}
            onClear={() => setChecked(new Set())}
          />
        )}
        {users.isPending ? (
          <p>{t("load")}</p>
        ) : (
          <DataTable
            headers={[
              <input
                key="all"
                type="checkbox"
                aria-label={t("selectAll")}
                checked={visibleUsers.length > 0 && visibleUsers.every((u) => checked.has(u.id))}
                onChange={(e) =>
                  setChecked(
                    e.target.checked
                      ? new Set(visibleUsers.filter((u) => u.role !== "owner").map((u) => u.id))
                      : new Set(),
                  )
                }
              />,
              t("name"),
              t("role"),
              t("policy"),
              t("status"),
              t("quota"),
              t("keyCount"),
              "",
            ]}
            empty={visibleUsers.length === 0}
          >
            {visibleUsers.map((u) => (
              <tr key={u.id} className={checked.has(u.id) ? "is-selected" : undefined}>
                <td>
                  <input
                    type="checkbox"
                    aria-label={t("selectMember", { name: u.name })}
                    disabled={u.role === "owner"}
                    checked={checked.has(u.id)}
                    onChange={(e) => {
                      const next = new Set(checked);
                      if (e.target.checked) next.add(u.id);
                      else next.delete(u.id);
                      setChecked(next);
                    }}
                  />
                </td>
                <td>
                  {/* The name is what an operator says out loud; the login is
                        how the account actually signs in. Both belong here. */}
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
                  <span className={`badge badge-role-${u.role}`}>{t(u.role)}</span>
                </td>
                <td>{policyName(u.policy_id)}</td>
                <td>
                  <StatusBadge value={u.status} />
                </td>
                <td>
                  <QuotaCell user={u} owner={owner} onEdit={() => setQuotaFor(u)} />
                </td>
                <td>{u.key_count}</td>
                <td className="row-actions">
                  <ActionMenu
                    compact
                    label={t("moreActions")}
                    items={[
                      { key: "details", label: t("details"), onSelect: () => setSelected(u) },
                      ...(owner
                        ? [
                            {
                              key: "quota",
                              label: t("editQuota"),
                              onSelect: () => setQuotaFor(u),
                            },
                            {
                              key: "revoke",
                              group: t("dangerZone"),
                              label: t("revokeSessions"),
                              danger: true,
                              onSelect: () => {
                                if (confirm(t("revokeSessions")))
                                  void run(`/admin/team/users/${u.id}/revoke-sessions`, "POST", {});
                              },
                            },
                          ]
                        : []),
                    ]}
                  />
                </td>
              </tr>
            ))}
          </DataTable>
        )}
      </>
      {selected ? (
        <MemberDetail
          user={selected}
          policies={policies.data ?? []}
          keys={userKeys.data}
          keysPending={userKeys.isPending}
          keysError={userKeys.error}
          bindings={bindings.data}
          owner={owner}
          busy={busy}
          onBack={() => setSelected(null)}
          onSave={(body) =>
            void run(`/admin/team/users/${selected.id}`, "PATCH", body, () => setSelected(null))
          }
          onRevoke={() => {
            if (confirm(t("revokeSessions")))
              void run(`/admin/team/users/${selected.id}/revoke-sessions`, "POST", {});
          }}
          onRecovery={() =>
            void run<{ path: string }>(
              `/admin/team/users/${selected.id}/recovery`,
              "POST",
              {},
              (d) => setNotice(new URL(d.path, location.origin).href),
            )
          }
          onUnlink={(bindingID) => {
            if (confirm(t("oauthUnlinkWarning")))
              void run(`/admin/team/users/${selected.id}/identities/${bindingID}`, "DELETE");
          }}
        />
      ) : null}
      {newMember && (
        <NewMemberDialog
          request={request}
          policies={policies.data ?? []}
          isOwner={owner}
          locale={locale}
          t={t}
          onClose={() => setNewMember(false)}
          onCreated={refresh}
        />
      )}
      {importing && (
        <ImportMembersDialog
          request={request}
          policies={policies.data ?? []}
          locale={locale}
          t={t}
          onClose={() => setImporting(false)}
          onImported={refresh}
        />
      )}
      {quotaFor && (
        <QuotaDialog
          request={request}
          user={quotaFor}
          locale={locale}
          t={t}
          onClose={() => setQuotaFor(null)}
          onSaved={refresh}
        />
      )}
    </div>
  );
}

/**
 * The member table's credit cell: the same meters every other pool uses, made
 * clickable for an operator who may change the ceiling.
 */
function QuotaCell({
  user,
  owner,
  onEdit,
}: {
  user: TeamUser;
  owner: boolean;
  onEdit: () => void;
}) {
  return (
    <QuotaMeters
      usedTokens={user.quota_used_tokens}
      totalTokens={user.quota_total_tokens}
      usedCost={user.quota_used_cost}
      totalCost={user.quota_total_cost}
      onEdit={owner ? onEdit : undefined}
    />
  );
}

function BulkBar({
  count,
  policies,
  owner,
  busy,
  policy,
  onPolicy,
  onRun,
  onClear,
}: {
  count: number;
  policies: Policy[];
  owner: boolean;
  busy: boolean;
  policy: string;
  onPolicy: (value: string) => void;
  onRun: (action: BulkMemberAction, policyID?: number) => void;
  onClear: () => void;
}) {
  const { t } = useUsers();
  return (
    <div className="team-bulkbar" role="region" aria-label={t("bulkActions")}>
      <strong>{t("bulkSelected", { n: count })}</strong>
      <div className="team-actions">
        {owner ? (
          <>
            <select
              className="team-search"
              aria-label={t("bulkPolicy")}
              value={policy}
              onChange={(e) => onPolicy(e.target.value)}
            >
              <option value="">{t("bulkPolicy")}</option>
              {policies.map((p) => (
                <option key={p.id} value={p.id}>
                  {t("applyPolicy", { name: p.name })}
                </option>
              ))}
            </select>
            <button
              className="team-button"
              disabled={busy || !policy}
              onClick={() => onRun("policy", Number(policy))}
            >
              {t("applyBulk")}
            </button>
            <button className="team-button" disabled={busy} onClick={() => onRun("pause")}>
              {t("pause")}
            </button>
            <button className="team-button" disabled={busy} onClick={() => onRun("resume")}>
              {t("resume")}
            </button>
            <button
              className="team-button"
              disabled={busy}
              onClick={() => onRun("revoke_sessions")}
            >
              {t("revokeSessions")}
            </button>
            <button
              className="team-button danger"
              disabled={busy}
              onClick={() => {
                if (confirm(t("bulkDeleteWarning", { n: count }))) onRun("delete");
              }}
            >
              {t("delete")}
            </button>
          </>
        ) : (
          <button className="team-button" disabled={busy} onClick={() => onRun("revoke_sessions")}>
            {t("revokeSessions")}
          </button>
        )}
        <button className="team-button quiet" onClick={onClear}>
          {t("clearSelection")}
        </button>
      </div>
    </div>
  );
}

/**
 * One member in full: their account fields, the keys they own, and the
 * third-party identities bound to the account.
 *
 * The credit fields live in the account form (see the quota section) because
 * granting credit is part of creating or editing a member — it was previously
 * only reachable from a small button back on the list, which is why operators
 * reported that quotas could not be changed from user management at all.
 */
/**
 * One member's whole record, opened beside the roster rather than instead of it.
 *
 * This used to replace the list: click a row and the page became a form with a
 * "← members" link, so the operator lost the list they were working from (and on
 * a phone the identity of the page they were on). A right-hand Drawer is how
 * every other entity in this console is edited, and it keeps the roster — and the
 * search that found the row — in view.
 */
function MemberDetail({
  user,
  policies,
  keys,
  keysPending,
  keysError,
  bindings,
  owner,
  busy,
  onBack,
  onSave,
  onRevoke,
  onRecovery,
  onUnlink,
}: {
  user: TeamUser;
  policies: Policy[];
  keys?: UserKey[];
  keysPending: boolean;
  keysError: unknown;
  bindings?: OAuthBinding[];
  owner: boolean;
  busy: boolean;
  onBack: () => void;
  onSave: (body: Record<string, unknown>) => void;
  onRevoke: () => void;
  onRecovery: () => void;
  onUnlink: (bindingID: number) => void;
}) {
  const { t } = useUsers();
  const [quotaTokens, setQuotaTokens] = useState(String(user.quota_total_tokens));
  const [quotaCost, setQuotaCost] = useState(String(user.quota_total_cost));
  const [resetUsed, setResetUsed] = useState(false);
  // The save button lives in the drawer's footer, outside the form element, so
  // the two have to be tied together explicitly.
  const formID = useId();
  return (
    <Drawer
      title={user.name}
      busy={busy}
      onClose={onBack}
      footer={
        <>
          {user.role !== "owner" && (
            <Button variant="secondary" disabled={busy} onClick={onRevoke}>
              {t("revokeSessions")}
            </Button>
          )}
          {owner && (
            <Button variant="secondary" disabled={busy} onClick={onRecovery}>
              {t("recovery")}
            </Button>
          )}
          <span className="flex-spacer" />
          <Button variant="quiet" disabled={busy} onClick={onBack}>
            {t("cancel")}
          </Button>
          <Button type="submit" form={formID} loading={busy}>
            {t("save")}
          </Button>
        </>
      }
    >
      <p className="drawer-subtitle">
        {user.username} · {t(user.role)} · <StatusBadge value={user.status} />
      </p>
      <form
        id={formID}
        onSubmit={(e) => {
          e.preventDefault();
          const f = new FormData(e.currentTarget);
          onSave({
            name: f.get("name"),
            policy_id: Number(f.get("policy_id")),
            status: f.get("status"),
            ...(owner && user.role !== "owner" ? { role: f.get("role") } : {}),
            ...(owner
              ? {
                  quota_total_tokens: Number(quotaTokens || 0),
                  quota_total_cost: Number(quotaCost || 0),
                  ...(resetUsed ? { quota_reset: true } : {}),
                }
              : {}),
          });
        }}
      >
        <div className="meta-form">
          <Field label={t("name")}>
            <input name="name" defaultValue={user.name} required maxLength={80} />
          </Field>
          <Field label={t("policy")}>
            <select name="policy_id" defaultValue={user.policy_id}>
              {policies.map((p) => (
                <option value={p.id} key={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label={t("status")}>
            <select name="status" defaultValue={user.status} disabled={user.role === "owner"}>
              <option value="active">{t("active")}</option>
              <option value="paused">{t("paused")}</option>
            </select>
            {user.role === "owner" && <input type="hidden" name="status" value="active" />}
          </Field>
          {owner && user.role !== "owner" && (
            <Field label={t("role")}>
              <select name="role" defaultValue={user.role}>
                <option value="member">{t("member")}</option>
                <option value="admin">{t("admin")}</option>
              </select>
            </Field>
          )}
        </div>
        {owner && (
          <section className="drawer-section">
            <h3>{t("credit")}</h3>
            <p className="panel-hint">{t("creditHint")}</p>
            <div className="meta-form">
              <Field label={t("quotaTotal")} hint={t("quotaHint")}>
                <input
                  type="number"
                  min={0}
                  value={quotaTokens}
                  onChange={(e) => setQuotaTokens(e.target.value)}
                />
              </Field>
              <Field label={t("quotaCostTotal")} hint={t("quotaCostHint")}>
                <input
                  type="number"
                  min={0}
                  step="0.01"
                  value={quotaCost}
                  onChange={(e) => setQuotaCost(e.target.value)}
                />
              </Field>
            </div>
            <label className="check marginless">
              <input
                type="checkbox"
                checked={resetUsed}
                onChange={(e) => setResetUsed(e.target.checked)}
              />
              <span>{t("quotaResetLabel")}</span>
            </label>
            <p className="panel-hint">
              {user.role === "owner" ? t("protectedOwner") : t("userChangeHint")}
            </p>
          </section>
        )}
      </form>
      <section className="drawer-section">
        <h3>{t("keys")}</h3>
        {keysPending ? (
          <Loading />
        ) : keysError ? (
          <ErrorState error={keysError} />
        ) : (
          <DataTable headers={[t("name"), "Key", t("status")]} empty={!keys?.length}>
            {keys?.map((k) => (
              <tr key={k.id}>
                <td>{k.name}</td>
                <td>
                  <code>mg-···{k.hint}</code>
                </td>
                <td>
                  <StatusBadge value={k.enabled} />
                </td>
              </tr>
            ))}
          </DataTable>
        )}
      </section>
      <section className="drawer-section">
        <h3>{t("oauthBindings")}</h3>
        <p className="panel-hint">{t("oauthBindingsHint")}</p>
        {bindings && bindings.length > 0 ? (
          <DataTable headers={[t("oauth"), t("name"), t("lastUsed"), ""]}>
            {bindings.map((binding) => (
              <tr key={binding.id}>
                <td>{binding.label}</td>
                <td>
                  {binding.name || binding.email || "—"}
                  {binding.email ? <small>{binding.email}</small> : null}
                </td>
                <td>{binding.last_login_at}</td>
                <td className="row-actions">
                  <Button variant="danger" disabled={busy} onClick={() => onUnlink(binding.id)}>
                    {t("oauthUnlink")}
                  </Button>
                </td>
              </tr>
            ))}
          </DataTable>
        ) : (
          <p className="panel-hint">{t("oauthNoBindings")}</p>
        )}
      </section>
    </Drawer>
  );
}
