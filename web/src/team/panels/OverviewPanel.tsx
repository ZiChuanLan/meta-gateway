import { useQuery } from "@tanstack/react-query";
import { ShieldCheck, UserCheck, Users } from "lucide-react";
import { TelemetryStrip } from "../../components/TelemetryStrip";
import { ModePanel } from "../ModePanel";
import { useUsers } from "../UsersContext";
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
 *
 * The summary used to be three hand-rolled boxes, the last of which showed the
 * site's brand NAME where the others showed counts — a "statistic" that could
 * never change. It is now the console's own telemetry strip, with three numbers
 * an operator can act on: how many accounts exist, how many of them can actually
 * sign in, and how many access policies they are spread across.
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
  const roster = users.data;
  const active = roster?.filter((user) => user.status === "active").length;

  return (
    <div className="team-board">
      <p className="panel-hint">{t("overviewHint")}</p>
      <ModePanel request={request} locale={locale} />
      {enabled && (
        <TelemetryStrip
          items={[
            {
              label: t("members"),
              value: roster ? roster.length : "—",
              icon: <Users size={16} />,
            },
            {
              label: t("activeMembers"),
              value: active ?? "—",
              icon: <UserCheck size={16} />,
            },
            {
              label: t("policies"),
              value: policies.data ? policies.data.length : "—",
              icon: <ShieldCheck size={16} />,
            },
          ]}
        />
      )}
      <p className="panel-hint">{t("moduleMap")}</p>
    </div>
  );
}
