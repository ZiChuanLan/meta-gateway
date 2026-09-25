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
}

export const CHROME_NAV_ITEMS: ChromeNavItem[] = [
	{ path: "/", labelKey: "app.nav.overview", icon: Activity, group: "primary" },
	{ path: "/channels", labelKey: "app.nav.channels", icon: Cable, group: "primary" },
	{ path: "/models", labelKey: "app.nav.models", icon: Boxes, group: "primary" },
	{ path: "/keys", labelKey: "app.nav.keys", icon: KeyRound, group: "primary" },
	{ path: "/workbench", labelKey: "app.nav.workbench", icon: Wand2, group: "primary" },
	{ path: "/logs", labelKey: "app.nav.logs", icon: ScrollText, group: "primary" },
	{ path: "/checkins", labelKey: "app.nav.checkins", icon: CalendarCheck, group: "primary" },
	{ path: "/exchange", labelKey: "app.nav.exchange", icon: ArrowLeftRight, group: "primary" },
	{ path: "/store", labelKey: "app.nav.store", icon: Package, group: "primary" },
	{ path: "/settings", labelKey: "app.nav.settings", icon: Settings, group: "settings" },
];
