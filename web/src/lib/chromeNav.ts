import type { ConsoleRole } from "../session";
import {
  Activity,
  ArrowLeftRight,
  Boxes,
  Cable,
  CalendarCheck,
  KeyRound,
  Package,
  ScrollText,
  Settings,
  Wand2,
  UserRound,
  Users,
  type LucideIcon,
} from "lucide-react";

/**
 * The console's own navigation entries, in the order the chrome shows them.
 *
 * This is the single source for both the sidebar/rail (App builds its nav from
 * it) and the appearance panel's per-entry switches — a label that exists twice
 * drifts, and the panel would list a page the nav does not have.
 *
 * Plugin entries are not here: their labels are runtime data and they already
 * carry their own per-plugin hide switch (lib/pluginNav.ts).
 */
export interface ChromeNavItem {
  path: string;
  /** i18n key for the entry's label, shared with the nav itself. */
  labelKey: string;
  icon: LucideIcon;
  /** The pages the console opens on, kept apart from the utility entries. */
  group: "primary" | "settings";
  /**
   * Roles that may see this entry. Absent means everyone.
   *
   * This is a display rule, not the security boundary: the API already scopes
   * a member to their own rows and refuses the gateway's endpoints outright
   * (team principal gate). What it buys is a console that does not offer a
   * member doors that will not open.
   */
  roles?: ConsoleRole[];
  /** Requires a real team account, not the deployment bearer credential. */
  accountOnly?: boolean;
}

/** Everyone signed in, staff or not. */
const ALL: ConsoleRole[] = ["owner", "admin", "member"];
/** The gateway's own pages: an operator's business, not a member's. */
const STAFF: ConsoleRole[] = ["owner", "admin"];

export const CHROME_NAV_ITEMS: ChromeNavItem[] = [
  { path: "/", labelKey: "app.nav.overview", icon: Activity, group: "primary", roles: ALL },
  { path: "/channels", labelKey: "app.nav.channels", icon: Cable, group: "primary", roles: STAFF },
  { path: "/models", labelKey: "app.nav.models", icon: Boxes, group: "primary", roles: ALL },
  { path: "/keys", labelKey: "app.nav.keys", icon: KeyRound, group: "primary", roles: ALL },
  { path: "/workbench", labelKey: "app.nav.workbench", icon: Wand2, group: "primary", roles: ALL },
  { path: "/logs", labelKey: "app.nav.logs", icon: ScrollText, group: "primary", roles: ALL },
  {
    path: "/checkins",
    labelKey: "app.nav.checkins",
    icon: CalendarCheck,
    group: "primary",
    roles: STAFF,
  },
  {
    path: "/exchange",
    labelKey: "app.nav.exchange",
    icon: ArrowLeftRight,
    group: "primary",
    roles: STAFF,
  },
  { path: "/store", labelKey: "app.nav.store", icon: Package, group: "primary", roles: STAFF },
  {
    path: "/account",
    labelKey: "app.nav.account",
    icon: UserRound,
    group: "primary",
    roles: ALL,
    accountOnly: true,
  },
  {
    path: "/settings",
    labelKey: "app.nav.settings",
    icon: Settings,
    group: "settings",
    roles: STAFF,
  },
  { path: "/users", labelKey: "app.nav.team", icon: Users, group: "primary", roles: STAFF },
];

/** Paths only staff may open. Used by the router guard; the API refuses them
 *  to a member anyway, so this is about not walking into a dead end. */
export const STAFF_ONLY_PATHS = [
  "/channels",
  "/checkins",
  "/exchange",
  "/settings",
  "/store",
  "/users",
  "/setup",
  "/plugins",
];

export function canAccessNav(item: ChromeNavItem, role: ConsoleRole | null): boolean {
  if (item.accountOnly && role === null) return false;
  return !item.roles || item.roles.includes(role ?? "owner");
}
