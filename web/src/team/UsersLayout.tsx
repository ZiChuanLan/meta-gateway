import { NavLink, Outlet } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  Gauge,
  KeyRound,
  LayoutDashboard,
  Palette,
  ShieldCheck,
  Tags,
  Ticket,
  Users,
  type LucideIcon,
} from "lucide-react";
import { useI18n } from "../i18n";
import { ErrorState, Loading, Page } from "../components/ui";
import { teamText, type TeamKey } from "./text";
import type { ModeInfo, TeamRequest, TeamSettings } from "./types";
import type { UsersContext } from "./UsersContext";

/**
 * The multi-user module's shell — the one entry point for everything that only
 * exists once a gateway has more than one person using it.
 *
 * The console's personal side never mounts this: the navigation entry is hidden
 * by operating mode (App decides), and the route itself renders the enable
 * guide when the mode is still `personal`. That is deliberate — the module owns
 * its own on/off switch (see OverviewPanel), so a gateway owner who wants team
 * accounts finds the door from Settings and turns it on from here, instead of
 * the switch living among runtime parameters where it read like a tuning knob.
 *
 * Sections are routes, not stateful tabs: each board is deep-linkable, survives
 * a reload, and can be hidden individually from the appearance panel. An admin
 * (non-owner) sees only the boards they may actually operate.
 *
 * Eight entries in one unlabelled row was a wall of words that wrapped into two
 * lines on a phone and said nothing about which board belonged with which. They
 * are now two labelled groups — the people who use the gateway, and how they get
 * in and what they see — rendered with the console's own tab anatomy, so this
 * shell reads as the same product as the rest of the console.
 */
interface Section {
  to: string;
  label: TeamKey;
  icon: LucideIcon;
  /** Owner-only boards are absent for an admin, not merely disabled. */
  ownerOnly: boolean;
}

interface Group {
  label: TeamKey;
  sections: Section[];
}

const GROUPS: Group[] = [
  {
    label: "navGroupMembers",
    sections: [
      { to: "overview", label: "overview", icon: LayoutDashboard, ownerOnly: false },
      { to: "members", label: "members", icon: Users, ownerOnly: false },
      { to: "policies", label: "policies", icon: ShieldCheck, ownerOnly: true },
      { to: "quotas", label: "quotas", icon: Gauge, ownerOnly: true },
    ],
  },
  {
    label: "navGroupAccess",
    sections: [
      { to: "codes", label: "codes", icon: Ticket, ownerOnly: true },
      { to: "pricing", label: "pricing", icon: Tags, ownerOnly: true },
      { to: "oauth", label: "oauth", icon: KeyRound, ownerOnly: true },
      { to: "branding", label: "branding", icon: Palette, ownerOnly: true },
    ],
  },
];

export function UsersLayout({ request }: { request: TeamRequest }) {
  // The transport is handed in rather than taken from the console session:
  // these boards are also reachable by a member-scoped session and must not
  // reach for the admin client (an ESLint boundary enforces it).
  const { locale } = useI18n();
  const t = teamText(locale);
  // `/admin/mode` answers on either gateway and is what tells this shell which
  // one it is running on. `/admin/team/settings` deliberately does not: it sits
  // behind the mode gate and 404s while the gateway is personal — asking it
  // first would make the module's own front door an error page.
  const mode = useQuery({
    queryKey: ["team", "mode"],
    queryFn: ({ signal }) => request<ModeInfo>("/admin/mode", { signal }),
  });
  const enabled = mode.data?.mode === "team";
  const settings = useQuery({
    queryKey: ["team", "settings"],
    queryFn: ({ signal }) =>
      request<{ settings: TeamSettings; has_owner: boolean; role: string }>(
        "/admin/team/settings",
        { signal },
      ),
    enabled,
  });

  if (mode.isPending) return <Loading />;
  if (mode.isError) return <ErrorState error={mode.error} retry={() => void mode.refetch()} />;
  const info = mode.data;
  if (!info) return null;
  // Owner and has_owner come from the settings document while the module is on
  // (it reflects the live roster); on a personal gateway there is no roster to
  // read, so the ungated mode payload answers instead.
  const owner = (settings.data?.role ?? info.role) === "owner";
  const hasOwner = settings.data?.has_owner ?? info.has_owner;
  const teamSettings: TeamSettings = settings.data?.settings ?? {
    mode: info.mode,
    branding: {
      name: "Meta Gateway",
      accent: "#275b85",
      logo_url: "",
      notice: "",
      login_description: "",
      api_base_url: "",
      show_usage: true,
      show_routing: true,
    },
  };
  // Before the module is switched on there is exactly one meaningful board:
  // the guide that switches it on. Offering "members" for a gateway that has no
  // accounts yet would be a door into an empty room.
  const groups = GROUPS.map((group) => ({
    label: group.label,
    sections: group.sections.filter(
      (section) => section.to === "overview" || (enabled && (!section.ownerOnly || owner)),
    ),
  })).filter((group) => group.sections.length > 0);
  return (
    <Page
      as="section"
      className="team-surface"
      title={t("team")}
      description={t("teamDescription")}
    >
      <nav className="tabs is-grouped" aria-label={t("team")}>
        {groups.map((group) => (
          <div className="tabs-group" key={group.label}>
            <span className="tabs-group-label">{t(group.label)}</span>
            {group.sections.map((section) => (
              <NavLink key={section.to} to={section.to}>
                <section.icon size={13} aria-hidden="true" />
                {t(section.label)}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>
      <Outlet
        context={
          {
            request,
            locale,
            t,
            owner,
            settings: teamSettings,
            hasOwner,
          } satisfies UsersContext
        }
      />
    </Page>
  );
}
