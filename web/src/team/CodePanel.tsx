import { useMemo, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { ActionMenu } from "../components/ActionMenu";
import { Button, DataTable, ErrorState, Field, Panel, StatusBadge } from "../components/ui";
import { teamError, type TeamKey, type TeamText } from "./text";
import { formatCost } from "../lib/format";
import type { MintedCode, Policy, TeamCode, TeamRequest } from "./types";

type CodeStatus = "pending" | "consumed" | "expired" | "revoked";

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
 *
 * The board is a Panel whose header carries the kind filter and the mint action,
 * and its rows use the console's own table, badge and menu vocabulary — the code
 * status words (pending / used up / expired / revoked) now ride the shared badge
 * tones rather than a private colour map.
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
    <div className="team-board">
      <Panel
        title={t("codes")}
        titleHelp={t("codesHint")}
        actions={
          <>
            <select
              aria-label={t("codeKind")}
              value={kindFilter}
              onChange={(e) => setKindFilter(e.target.value as typeof kindFilter)}
            >
              <option value="all">{t("all")}</option>
              <option value="invite">{t("codeKindInvite")}</option>
              <option value="credit">{t("codeKindCredit")}</option>
            </select>
            <Button
              icon={<Plus size={14} />}
              onClick={() => {
                setError(null);
                setMinting(true);
              }}
            >
              {t("codeMint")}
            </Button>
          </>
        }
      >
        {error ? (
          <p className="inline-error" role="alert">
            {teamError(error, locale)}
          </p>
        ) : null}

        {minted ? (
          <div className="team-minted">
            <div className="team-minted-head">
              <strong>{t("codeMinted", { n: minted.length })}</strong>
              <div className="team-actions">
                <Button
                  variant="secondary"
                  onClick={() => void copy(minted.map((item) => item.code).join("\n"), "all")}
                >
                  {copied === "all" ? t("copied") : t("copyAll")}
                </Button>
                <Button variant="quiet" onClick={() => setMinted(null)}>
                  {t("close")}
                </Button>
              </div>
            </div>
            <p className="panel-hint">{t("codeMintedOnce")}</p>
            <ul className="team-minted-list">
              {minted.map((item) => (
                <li key={item.id}>
                  <code>{item.code}</code>
                  <Button variant="quiet" onClick={() => void copy(item.code, String(item.id))}>
                    {copied === String(item.id) ? t("copied") : t("copy")}
                  </Button>
                </li>
              ))}
            </ul>
          </div>
        ) : null}

        {minting ? (
          <form className="team-mint-form" onSubmit={createCodes}>
            <div className="meta-form">
              <Field label={t("codeKind")}>
                <select name="kind" defaultValue="invite">
                  <option value="invite">{t("codeKindInvite")}</option>
                  <option value="credit">{t("codeKindCredit")}</option>
                </select>
              </Field>
              <Field label={t("codeCount")}>
                <input name="count" type="number" min={1} max={500} defaultValue={1} />
              </Field>
              <Field label={t("codeMaxUses")}>
                <input name="max_uses" type="number" min={1} max={100000} defaultValue={1} />
              </Field>
              <Field label={t("codeExpiry")}>
                <select name="expires_in_hours" defaultValue={168}>
                  {EXPIRY_CHOICES.map((hours) => (
                    <option key={hours} value={hours}>
                      {t("codeExpiryHours", { n: hours })}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label={t("codeQuota")}>
                <input name="quota_tokens" type="number" min={0} step={1000} defaultValue={0} />
              </Field>
              <Field label={t("codeQuotaCost")}>
                <input
                  name="quota_cost"
                  type="number"
                  min={0}
                  step="0.01"
                  defaultValue={0}
                  placeholder="0"
                />
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
                <select name="role" defaultValue="member">
                  <option value="member">{t("member")}</option>
                  <option value="admin">{t("admin")}</option>
                </select>
              </Field>
              <Field label={t("label")}>
                <input name="label" maxLength={80} placeholder={t("codeLabelHint")} />
              </Field>
            </div>
            <p className="panel-hint">{t("codeFormHint")}</p>
            <div className="form-actions">
              <Button type="submit" loading={busy}>
                {t("codeMint")}
              </Button>
              <Button variant="quiet" type="button" onClick={() => setMinting(false)}>
                {t("cancel")}
              </Button>
            </div>
          </form>
        ) : null}

        {codes.isPending ? (
          <p className="panel-hint">{t("load")}</p>
        ) : codes.error ? (
          <ErrorState error={codes.error} />
        ) : (
          <DataTable
            headers={[t("codeKind"), t("label"), t("uses"), t("quota"), t("status"), ""]}
            empty={!visible.length}
          >
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
                <td>
                  {code.quota_tokens > 0 ? code.quota_tokens.toLocaleString() : "—"}
                  {(code.quota_cost ?? 0) > 0 ? <small>{formatCost(code.quota_cost)}</small> : null}
                </td>
                <td>
                  <StatusBadge value={codeStatus(code)} />
                </td>
                <td className="row-actions">
                  {code.revoked ? null : (
                    <ActionMenu
                      compact
                      label={t("moreActions")}
                      items={[
                        {
                          key: "revoke",
                          group: t("dangerZone"),
                          label: t("revoke"),
                          danger: true,
                          disabled: busy,
                          onSelect: () => void revoke(code.id),
                        },
                      ]}
                    />
                  )}
                </td>
              </tr>
            ))}
          </DataTable>
        )}
      </Panel>
    </div>
  );
}

/** spent/expired/revoked are the three states an operator must tell apart. */
function codeStatus(code: TeamCode): CodeStatus {
  if (code.revoked) return "revoked";
  if (code.used_count >= code.max_uses) return "consumed";
  if (code.expires_at * 1000 < Date.now()) return "expired";
  return "pending";
}
