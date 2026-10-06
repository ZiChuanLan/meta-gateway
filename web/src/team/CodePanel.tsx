import { useMemo, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { teamError, type TeamKey, type TeamText } from "./text";
import { formatCost } from "../lib/format";
import type { MintedCode, Policy, TeamCode, TeamRequest } from "./types";

type CodeStatus = "pending" | "consumed" | "expired" | "revoked";

/** Status codes to copy keys: the table renders words, never raw states. */
const CODE_STATUS: Record<CodeStatus, TeamKey> = {
  pending: "pending",
  consumed: "consumed",
  expired: "expired",
  revoked: "revoked",
};

const KIND_HINT: Record<TeamCode["kind"], TeamKey> = {
  invite: "codeKindInviteHint",
  credit: "codeKindCreditHint",
};

const EXPIRY_CHOICES = [1, 24, 72, 168, 720] as const;

/**
 * The code board: invitations that may be used more than once, credit
 * vouchers, and the batch generation both of them need.
 *
 * Generated codes are shown exactly once — the table stores only a hash — so
 * the mint result stays on screen, selectable and copyable as a block, until
 * the operator dismisses it. That is the only way to hand out thirty codes
 * without thirty round-trips.
 */
export function CodePanel({
  request,
  policies,
  locale,
  t,
}: {
  request: TeamRequest;
  policies: Policy[];
  locale: string;
  t: TeamText;
}) {
  const qc = useQueryClient();
  const [minting, setMinting] = useState(false);
  const [kindFilter, setKindFilter] = useState<"all" | TeamCode["kind"]>("all");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [minted, setMinted] = useState<MintedCode[] | null>(null);
  const [copied, setCopied] = useState("");

  const codes = useQuery({
    queryKey: ["team", "codes"],
    queryFn: ({ signal }) => request<TeamCode[]>("/admin/team/codes", { signal }),
  });

  const visible = useMemo(
    () => (codes.data ?? []).filter((c) => kindFilter === "all" || c.kind === kindFilter),
    [codes.data, kindFilter],
  );

  async function copy(value: string, key: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(key);
      window.setTimeout(() => setCopied(""), 1600);
    } catch {
      setCopied("");
    }
  }

  async function revoke(id: number) {
    if (!confirm(t("codeRevokeWarning"))) return;
    setBusy(true);
    setError(null);
    try {
      await request(`/admin/team/codes/${id}`, { method: "DELETE" });
      await qc.invalidateQueries({ queryKey: ["team", "codes"] });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  async function createCodes(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError(null);
    try {
      const result = await request<{ codes: MintedCode[] }>("/admin/team/codes", {
        method: "POST",
        body: JSON.stringify({
          kind: form.get("kind"),
          count: Number(form.get("count") ?? 1),
          policy_id: Number(form.get("policy_id") ?? 0),
          role: form.get("role") ?? "member",
          expires_in_hours: Number(form.get("expires_in_hours") ?? 24),
          max_uses: Number(form.get("max_uses") ?? 1),
          quota_tokens: Number(form.get("quota_tokens") ?? 0),
          // A voucher may carry tokens, money, or both: the account pool holds two
          // budgets and the code says which one it tops up.
          quota_cost: Number(form.get("quota_cost") ?? 0),
          label: form.get("label") ?? "",
          note: form.get("note") ?? "",
        }),
      });
      setMinted(result.codes);
      setMinting(false);
      await qc.invalidateQueries({ queryKey: ["team", "codes"] });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="team-codes">
      <div className="team-head">
        <div>
          <h3 style={{ margin: 0 }}>{t("codes")}</h3>
          <p className="team-muted" style={{ margin: "4px 0 0" }}>
            {t("codesHint")}
          </p>
        </div>
        <div className="team-actions">
          <select
            className="team-search"
            aria-label={t("codeKind")}
            value={kindFilter}
            onChange={(e) => setKindFilter(e.target.value as typeof kindFilter)}
          >
            <option value="all">{t("all")}</option>
            <option value="invite">{t("codeKindInvite")}</option>
            <option value="credit">{t("codeKindCredit")}</option>
          </select>
          <button
            className="team-button primary"
            onClick={() => {
              setError(null);
              setMinting(true);
            }}
          >
            ＋ {t("codeMint")}
          </button>
        </div>
      </div>

      {error ? (
        <div className="team-error" role="alert">
          {teamError(error, locale)}
        </div>
      ) : null}

      {minted ? (
        <div className="team-notice team-minted">
          <div className="team-minted-head">
            <strong>{t("codeMinted", { n: minted.length })}</strong>
            <div className="team-actions">
              <button
                className="team-button"
                onClick={() => void copy(minted.map((item) => item.code).join("\n"), "all")}
              >
                {copied === "all" ? t("copied") : t("copyAll")}
              </button>
              <button className="team-button quiet" onClick={() => setMinted(null)}>
                {t("close")}
              </button>
            </div>
          </div>
          <p className="team-muted">{t("codeMintedOnce")}</p>
          <ul className="team-minted-list">
            {minted.map((item) => (
              <li key={item.id}>
                <code>{item.code}</code>
                <button
                  className="team-button quiet"
                  onClick={() => void copy(item.code, String(item.id))}
                >
                  {copied === String(item.id) ? t("copied") : t("copy")}
                </button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {minting ? (
        <form className="team-section" onSubmit={createCodes}>
          <div className="team-grid">
            <label className="team-field">
              <span>{t("codeKind")}</span>
              <select name="kind" defaultValue="invite">
                <option value="invite">{t("codeKindInvite")}</option>
                <option value="credit">{t("codeKindCredit")}</option>
              </select>
            </label>
            <label className="team-field">
              <span>{t("codeCount")}</span>
              <input name="count" type="number" min={1} max={500} defaultValue={1} />
            </label>
            <label className="team-field">
              <span>{t("codeMaxUses")}</span>
              <input name="max_uses" type="number" min={1} max={100000} defaultValue={1} />
            </label>
            <label className="team-field">
              <span>{t("codeExpiry")}</span>
              <select name="expires_in_hours" defaultValue={168}>
                {EXPIRY_CHOICES.map((hours) => (
                  <option key={hours} value={hours}>
                    {t("codeExpiryHours", { n: hours })}
                  </option>
                ))}
              </select>
            </label>
            <label className="team-field">
              <span>{t("codeQuota")}</span>
              <input name="quota_tokens" type="number" min={0} step={1000} defaultValue={0} />
            </label>
            <label className="team-field">
              <span>{t("codeQuotaCost")}</span>
              <input
                name="quota_cost"
                type="number"
                min={0}
                step="0.01"
                defaultValue={0}
                placeholder="0"
              />
            </label>
            <label className="team-field">
              <span>{t("policy")}</span>
              <select name="policy_id" defaultValue={policies[0]?.id ?? 1}>
                {policies.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="team-field">
              <span>{t("role")}</span>
              <select name="role" defaultValue="member">
                <option value="member">{t("member")}</option>
                <option value="admin">{t("admin")}</option>
              </select>
            </label>
            <label className="team-field">
              <span>{t("label")}</span>
              <input name="label" maxLength={80} placeholder={t("codeLabelHint")} />
            </label>
          </div>
          <p className="team-muted">{t("codeFormHint")}</p>
          <div className="team-actions">
            <button className="team-button primary" type="submit" disabled={busy}>
              {busy ? t("saving") : t("codeMint")}
            </button>
            <button className="team-button quiet" type="button" onClick={() => setMinting(false)}>
              {t("cancel")}
            </button>
          </div>
        </form>
      ) : null}

      {codes.isPending ? (
        <p>{t("load")}</p>
      ) : codes.error ? (
        <div className="team-error" role="alert">
          {teamError(codes.error, locale)}
        </div>
      ) : !visible.length ? (
        <p className="team-muted">{t("codeEmpty")}</p>
      ) : (
        <div className="team-table-wrap">
          <table className="team-table">
            <thead>
              <tr>
                <th>{t("codeKind")}</th>
                <th>{t("label")}</th>
                <th>{t("uses")}</th>
                <th className="team-hide-mobile">{t("quota")}</th>
                <th>{t("status")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {visible.map((code) => (
                <tr key={code.id}>
                  <td>{t(code.kind === "invite" ? "codeKindInvite" : "codeKindCredit")}</td>
                  <td>
                    {code.label || `#${code.id}`}
                    <small>{t(KIND_HINT[code.kind])}</small>
                  </td>
                  <td>
                    {code.used_count}/{code.max_uses}
                  </td>
                  <td className="team-hide-mobile">
                    {code.quota_tokens > 0 ? code.quota_tokens.toLocaleString() : "—"}
                    {(code.quota_cost ?? 0) > 0 ? (
                      <small>{formatCost(code.quota_cost)}</small>
                    ) : null}
                  </td>
                  <td>
                    <span className={`team-status ${codeStatus(code)}`}>
                      {t(CODE_STATUS[codeStatus(code)])}
                    </span>
                  </td>
                  <td>
                    {!code.revoked && (
                      <button
                        className="team-button quiet"
                        disabled={busy}
                        onClick={() => void revoke(code.id)}
                      >
                        {t("revoke")}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

/** spent/expired/revoked are the three states an operator must tell apart. */
function codeStatus(code: TeamCode): CodeStatus {
  if (code.revoked) return "revoked";
  if (code.used_count >= code.max_uses) return "consumed";
  if (code.expires_at * 1000 < Date.now()) return "expired";
  return "pending";
}
