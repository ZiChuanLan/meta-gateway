import { NavLink, Outlet } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "../i18n";
import { ErrorState, Loading } from "../components/ui";
import { teamText, type TeamKey } from "./text";
import type { ModeInfo, TeamRequest, TeamSettings } from "./types";
import type { UsersContext } from "./UsersContext";
import "./team.css";

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
 * Sections are routes, not tabs: each board is deep-linkable, survives a
 * reload, and can be hidden individually from the appearance panel. An admin
 * (non-owner) sees only the boards they may actually operate.
 */
interface Section {
  to: string;
  label: TeamKey;
  /** Owner-only boards are absent for an admin, not merely disabled. */
  ownerOnly: boolean;
}

const SECTIONS: Section[] = [
  { to: "overview", label: "overview", ownerOnly: false },
  { to: "members", label: "members", ownerOnly: false },
  { to: "policies", label: "policies", ownerOnly: true },
  { to: "quotas", label: "quotas", ownerOnly: true },
  { to: "pricing", label: "pricing", ownerOnly: true },
  { to: "codes", label: "codes", ownerOnly: true },
  { to: "oauth", label: "oauth", ownerOnly: true },
  { to: "branding", label: "branding", ownerOnly: true },
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
  const sections = SECTIONS.filter(
    (section) => section.to === "overview" || (enabled && (!section.ownerOnly || owner)),
  );
  return (
    <section className="team-surface">
      <div className="team-head">
        <div>
          <h1>{t("team")}</h1>
          <p className="team-muted">{t("teamDescription")}</p>
        </div>
      </div>
      <nav className="team-subnav" aria-label={t("team")}>
        {sections.map((section) => (
          <NavLink
            key={section.to}
            to={section.to}
            className={({ isActive }) => (isActive ? "is-active" : "")}
          >
            {t(section.label)}
          </NavLink>
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
    </section>
  );
}
