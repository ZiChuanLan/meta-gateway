import type { LucideIcon } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { useI18n } from "../i18n";
import { Drawer } from "./Drawer";
import { UI_THEMES } from "../themes/registry";
import type { UIThemeId } from "../themes/types";

export type ConsoleNavItem = { to: string; label: string; icon: LucideIcon };
type NavSection = { label: string; items: ConsoleNavItem[] };

export function ConsoleShell({ children, sections, version, theme, onThemeChange, onSearch, onDisconnect, health, update, background, entering, appearance = "modern" }: {
  children: ReactNode;
  appearance?: UIThemeId;
  sections: NavSection[];
  version: string;
  theme: "light" | "dark";
  onThemeChange: () => void;
  onSearch: () => void;
  onDisconnect: () => void;
  health: { healthy: number; total: number; loading: boolean };
  update?: { latest: string; release_url?: string };
  background?: string;
  entering?: boolean;
}) {
  const { t } = useI18n();
  const location = useLocation();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const currentSection = sections.find((section) => section.items.some((item) => item.to === "/" ? location.pathname === "/" : location.pathname.startsWith(item.to)));
  const current = currentSection?.items.find((item) => item.to === "/" ? location.pathname === "/" : location.pathname.startsWith(item.to));
  const Chrome = UI_THEMES[appearance].Chrome;
  const tone = health.total === 0 ? "idle" : health.healthy === health.total ? "ok" : health.healthy === 0 ? "down" : "warn";

  useEffect(() => { setMobileOpen(false); }, [location.pathname]);

  const navigation = (mobile = false) => (
    <nav className="console-navigation" aria-label={t("app.nav.open")}>
      {sections.filter((section) => section.items.length).map((section) => (
        <div className="console-nav-group" key={section.label}>
          <span className="console-nav-caption">{section.label}</span>
          {section.items.map(({ to, label, icon: Icon }) => (
            <NavLink key={to} to={to} end={to === "/"} title={label}
              className={({ isActive }) => `console-nav-link${isActive ? " active" : ""}`}
              onClick={() => { if (mobile) setMobileOpen(false); }}>
              <Icon size={18} strokeWidth={1.7} aria-hidden="true" />
              <span>{label}</span>
              <i className="console-nav-indicator" aria-hidden="true" />
            </NavLink>
          ))}
        </div>
      ))}
    </nav>
  );

  return (
    <div className={`console-shell theme-shell theme-${appearance}${collapsed ? " is-collapsed" : ""}`}>
      <div className="console-atmosphere" aria-hidden="true" style={background ? { backgroundImage: `url("${background}")` } : undefined} />
      <Chrome navigation={navigation()} sections={sections} version={version} theme={theme}
        onThemeChange={onThemeChange} onSearch={onSearch} onDisconnect={onDisconnect}
        health={health} tone={tone} update={update} collapsed={collapsed} onCollapse={() => setCollapsed(!collapsed)}
        onOpenNav={() => setMobileOpen(true)} sectionLabel={currentSection?.label ?? t("shell.workspace")}
        pageLabel={current?.label ?? t("shell.workspace")} />
      <div className="console-main">
        <div className={`console-content${entering ? " route-enter" : ""}`} id="console-workspace">{children}</div>
        <footer className="console-page-foot"><span>Meta Gateway</span><span>{t("shell.footer")}</span><span className="console-foot-dot" /></footer>
      </div>
      {mobileOpen ? <Drawer title="Meta Gateway" onClose={() => setMobileOpen(false)} side="left" width={300} className="console-nav-drawer">{navigation(true)}</Drawer> : null}
    </div>
  );
}
