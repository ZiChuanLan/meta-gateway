import { useQuery } from "@tanstack/react-query";
import { ModePanel } from "../ModePanel";
import { useUsers } from "../UsersLayout";
import type { Policy, TeamUser } from "../types";

/**
 * The module's front door: what state the gateway is in, and the switch that
 * changes it.
 *
 * This is also where a gateway that has never used team accounts starts — the
 * navigation entry stays reachable (see UsersLayout), so an operator who wants
 * multi-user mode lands here, reads what turning it on will do, and does it in
 * one place. Everything else on this board is a summary of what is already
 * there.
 */
export function OverviewPanel() {
  const { request, locale, t, settings } = useUsers();
  const enabled = settings.mode === "team";
  // Not fetched in personal mode: those endpoints answer for an account model
  // that is switched off, and a count of zero would read like a fact.
  const users = useQuery({
    queryKey: ["team", "users"],
    queryFn: ({ signal }) => request<TeamUser[]>("/admin/team/users", { signal }),
    enabled,
  });
  const policies = useQuery({
    queryKey: ["team", "policies"],
    queryFn: ({ signal }) => request<Policy[]>("/admin/team/policies", { signal }),
    enabled,
  });

  return (
    <div className="team-board">
      <p className="team-muted">{t("overviewHint")}</p>
      <ModePanel request={request} locale={locale} className="team-mode-card" />
      {enabled && (
        <dl className="team-summary">
          <div>
            <dt>{t("members")}</dt>
            <dd>{users.data?.length ?? "—"}</dd>
          </div>
          <div>
            <dt>{t("policies")}</dt>
            <dd>{policies.data?.length ?? "—"}</dd>
          </div>
          <div>
            <dt>{t("branding")}</dt>
            <dd>{settings.branding.name}</dd>
          </div>
        </dl>
      )}
      <p className="team-muted">{t("moduleMap")}</p>
    </div>
  );
}
