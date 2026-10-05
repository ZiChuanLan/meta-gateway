import { useState, useRef, type FormEvent } from "react";
import { TeamField as Field, TeamModal } from "./ui";
import { formatCost } from "../lib/format";
import { teamError, type TeamKey, type TeamText } from "./text";
import type { ImportOutcome, Policy, TeamRequest, TeamUser } from "./types";

/**
 * Per-line import failures carry a reason code; the console names each one so a
 * roster paste reports "username already taken" rather than "failed".
 */
const IMPORT_REASON: Record<string, TeamKey> = {
  invalid_username: "importReason.invalid_username",
  username_taken: "importReason.username_taken",
  invalid_quota: "importReason.invalid_quota",
  invalid_request: "importReason.invalid_request",
  password_length: "importReason.password_length",
  invalid_policy: "importReason.invalid_policy",
  save_failed: "importReason.save_failed",
};

/**
 * The three writes an operator needs when they already know who they are
 * onboarding: create one account, paste a list, or top up an existing account's
 * credit pool. Invitations remain the self-service path and live on the code
 * board.
 */

export function NewMemberDialog({
  request,
  policies,
  isOwner,
  locale,
  t,
  onClose,
  onCreated,
}: {
  request: TeamRequest;
  policies: Policy[];
  isOwner: boolean;
  locale: string;
  t: TeamText;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [created, setCreated] = useState<{ username: string; password: string } | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError(null);
    try {
      const result = await request<{ user: TeamUser; password: string }>("/admin/team/users", {
        method: "POST",
        body: JSON.stringify({
          username: form.get("username"),
          name: form.get("name"),
          password: form.get("password") ?? "",
          policy_id: Number(form.get("policy_id") ?? 0),
          role: form.get("role") ?? "member",
          quota_tokens: Number(form.get("quota_tokens") ?? 0),
          quota_cost: Number(form.get("quota_cost") ?? 0),
        }),
      });
      setCreated({ username: result.user.username, password: result.password });
      onCreated();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <TeamModal title={t("newMember")} onClose={onClose} busy={busy}>
      {created ? (
        <>
          <p className="team-muted">{t("memberCreatedHint")}</p>
          <div className="team-secret">
            <code>
              {created.username} / {created.password}
            </code>
            <button
              className="team-button quiet"
              onClick={() =>
                void navigator.clipboard.writeText(
                  `${created.username} / ${created.password}`,
                )
              }
            >
              {t("copy")}
            </button>
          </div>
          <div className="team-actions">
            <button className="team-button" type="button" onClick={onClose}>
              {t("close")}
            </button>
          </div>
        </>
      ) : (
        <form id="new-member" onSubmit={submit}>
          <div className="team-grid">
            <Field label={t("username")}>
              <input name="username" required minLength={3} maxLength={64} autoComplete="off" />
            </Field>
            <Field label={t("name")}>
              <input name="name" required maxLength={80} />
            </Field>
            <Field label={`${t("password")} · ${t("passwordOptional")}`}>
              <input name="password" type="password" minLength={10} autoComplete="new-password" />
            </Field>
            <Field label={t("policy")}>
              <select name="policy_id" defaultValue={policies[0]?.id ?? 1}>
                {policies.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t("role")}>
              <select name="role" defaultValue="member" disabled={!isOwner}>
                <option value="member">{t("member")}</option>
                <option value="admin">{t("admin")}</option>
              </select>
            </Field>
            <Field label={t("quota")}>
              <input name="quota_tokens" type="number" min={0} step={1000} defaultValue={0} />
            </Field>
            <Field label={t("quotaCostTotal")}>
              <input name="quota_cost" type="number" min={0} step="0.01" defaultValue={0} placeholder="0" />
            </Field>
          </div>
          <p className="team-muted">{t("quotaHint")}</p>
          {error ? (
            <div className="team-error" role="alert">
              {teamError(error, locale)}
            </div>
          ) : null}
          <div className="team-actions">
            <button className="team-button primary" type="submit" disabled={busy}>
              {busy ? t("saving") : t("create")}
            </button>
            <button className="team-button quiet" type="button" onClick={onClose}>
              {t("cancel")}
            </button>
          </div>
        </form>
      )}
    </TeamModal>
  );
}

export function ImportMembersDialog({
  request,
  policies,
  locale,
  t,
  onClose,
  onImported,
}: {
  request: TeamRequest;
  policies: Policy[];
  locale: string;
  t: TeamText;
  onClose: () => void;
  onImported: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [outcome, setOutcome] = useState<ImportOutcome | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError(null);
    try {
      const result = await request<ImportOutcome>("/admin/team/users/import", {
        method: "POST",
        body: JSON.stringify({
          text: form.get("text"),
          policy_id: Number(form.get("policy_id") ?? 0),
          quota_tokens: Number(form.get("quota_tokens") ?? 0),
        quota_cost: Number(form.get("quota_cost") ?? 0),
        }),
      });
      setOutcome(result);
      onImported();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  const credentials = (outcome?.created ?? [])
    .map((item) => `${item.username} / ${item.password}`)
    .join("\n");

  return (
    <TeamModal title={t("importMembers")} onClose={onClose} busy={busy}>
      {outcome ? (
        <>
          <p className="team-muted">{t("importSummary", { n: outcome.created.length })}</p>
          {outcome.created.length ? (
            <ul className="team-minted-list">
              {outcome.created.map((item) => (
                <li key={item.id}>
                  <code>
                    {item.username} / {item.password}
                  </code>
                </li>
              ))}
            </ul>
          ) : null}
          {outcome.failed.length ? (
            <>
              <p className="team-muted" style={{ marginTop: 14 }}>
                {t("importFailures", { n: outcome.failed.length })}
              </p>
              <ul className="team-import-failures">
                {outcome.failed.map((item) => (
                  <li key={`${item.line}-${item.input}`}>
                    <span className="mono">#{item.line}</span> {item.input || "—"}
                    <small>{t(IMPORT_REASON[item.reason] ?? "importReason.save_failed")}</small>
                  </li>
                ))}
              </ul>
            </>
          ) : null}
          <div className="team-actions">
            <button
              className="team-button primary"
              disabled={!outcome.created.length}
              onClick={() => void navigator.clipboard.writeText(credentials)}
            >
              {t("copyCredentials")}
            </button>
            <button className="team-button quiet" type="button" onClick={onClose}>
              {t("close")}
            </button>
          </div>
        </>
      ) : (
        <form id="import-members" onSubmit={submit}>
          <Field label={t("importList")}>
            <textarea
              name="text"
              rows={10}
              required
              spellCheck={false}
              placeholder={"alice,Alice,password-123\nalice,Alice,password-123,500000\n# comments and blank lines are ignored"}
            />
          </Field>
          <p className="team-muted">{t("importHint")}</p>
          <div className="team-grid">
            <Field label={t("policy")}>
              <select name="policy_id" defaultValue={policies[0]?.id ?? 1}>
                {policies.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t("quota")}>
              <input name="quota_tokens" type="number" min={0} step={1000} defaultValue={0} />
            </Field>
            <Field label={t("quotaCostTotal")}>
              <input name="quota_cost" type="number" min={0} step="0.01" defaultValue={0} placeholder="0" />
            </Field>
          </div>
          <p className="team-muted">{t("quotaHint")}</p>
          {error ? (
            <div className="team-error" role="alert">
              {teamError(error, locale)}
            </div>
          ) : null}
          <div className="team-actions">
            <button className="team-button primary" type="submit" disabled={busy}>
              {busy ? t("saving") : t("import")}
            </button>
            <button className="team-button quiet" type="button" onClick={onClose}>
              {t("cancel")}
            </button>
          </div>
        </form>
      )}
    </TeamModal>
  );
}

export function QuotaDialog({
  request,
  user,
  locale,
  t,
  onClose,
  onSaved,
}: {
  request: TeamRequest;
  user: TeamUser;
  locale: string;
  t: TeamText;
  onClose: () => void;
  onSaved: () => void;
}) {
  const submitting = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [total, setTotal] = useState(String(user.quota_total_tokens));
  const [costTotal, setCostTotal] = useState(
    user.quota_total_cost > 0 ? String(user.quota_total_cost) : "",
  );
  const [reset, setReset] = useState(false);

  // Blank explicitly means unlimited, matching existing quota semantics.
  // Invalid numeric input must not become null in JSON and silently preserve
  // the previous limit while telling the operator the save succeeded.
  const tokenLimit = total.trim() === "" ? 0 : Number(total);
  const moneyLimit = costTotal.trim() === "" ? 0 : Number(costTotal);
  const valid = Number.isSafeInteger(tokenLimit) && tokenLimit >= 0 && tokenLimit <= 1_000_000_000_000 &&
    Number.isFinite(moneyLimit) && moneyLimit >= 0 && moneyLimit <= 1_000_000;
  async function save() {
    if (!valid || submitting.current) return;
    submitting.current = true;
    setBusy(true);
    setError(null);
    try {
      await request(`/admin/team/users/${user.id}`, {
        method: "PATCH",
        body: JSON.stringify({
          quota_total_tokens: tokenLimit,
          quota_total_cost: moneyLimit,
          quota_reset: reset,
        }),
      });
      onSaved();
      onClose();
    } catch (e) {
      setError(e);
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }

  return (
    <TeamModal
      title={`${t("quota")} · ${user.name}`}
      onClose={onClose}
      busy={busy}
    >
      <p className="team-muted">
        {t("quotaUsed", {
          used: user.quota_used_tokens.toLocaleString(),
          total: user.quota_total_tokens
            ? user.quota_total_tokens.toLocaleString()
            : t("unlimited"),
        })}
        {user.quota_total_cost > 0
          ? ` · ${formatCost(user.quota_used_cost)} / ${formatCost(user.quota_total_cost)}`
          : ""}
      </p>
      <div className="team-grid">
        <Field label={t("quotaTotal")}>
          <input
            type="number"
            min={0}
            step={1}
            max={1_000_000_000_000}
            aria-invalid={!Number.isSafeInteger(tokenLimit) || tokenLimit < 0 || tokenLimit > 1_000_000_000_000}
            value={total}
            onChange={(e) => setTotal(e.target.value)}
          />
        </Field>
        <Field label={t("quotaCostTotal")}>
          <input
            type="number"
            min={0}
            step="0.01"
            max={1_000_000}
            aria-invalid={!Number.isFinite(moneyLimit) || moneyLimit < 0 || moneyLimit > 1_000_000}
            value={costTotal}
            onChange={(e) => setCostTotal(e.target.value)}
            placeholder="0"
          />
        </Field>
        <Field label={t("quotaReset")}>
          <label className="check">
            <input
              type="checkbox"
              checked={reset}
              onChange={(e) => setReset(e.target.checked)}
            />
            <span>{t("quotaResetLabel")}</span>
          </label>
        </Field>
      </div>
      <p className="team-muted">{t("quotaHint")}</p>
      <p className="team-muted">{t("quotaEditSemantics")}</p>
      {error ? (
        <div className="team-error" role="alert">
          {teamError(error, locale)}
        </div>
      ) : null}
      <div className="team-actions">
        <button className="team-button primary" disabled={busy || !valid} onClick={() => void save()}>
          {busy ? t("saving") : t("save")}
        </button>
        <button className="team-button quiet" type="button" onClick={onClose}>
          {t("cancel")}
        </button>
      </div>
    </TeamModal>
  );
}
