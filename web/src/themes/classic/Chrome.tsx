import { ArrowUpRight, LogOut, Menu, Moon, Search, Sun } from "lucide-react";
import { BrandMark } from "../../components/BrandMark";
import { NavLink } from "react-router-dom";
import { LanguageSwitcher, useI18n } from "../../i18n";
import type { ThemeChromeProps } from "../types";

export function ClassicChrome({ sections, version, theme, onThemeChange, onSearch, onDisconnect, health, tone, update, onOpenNav }: ThemeChromeProps) {
  const { t } = useI18n();
  return <header className="gate-console-deck">
    <div className="deck-identity"><div className="brand-mark" aria-hidden="true"><BrandMark size={18} /></div><div className="deck-identity-copy"><strong>META GATEWAY</strong><span>OPERATIONS CONSOLE // {version}</span></div></div>
    <button type="button" className="classic-mobile-trigger" onClick={onOpenNav} aria-label={t("app.nav.open")}><Menu size={19} /></button>
    <nav className="deck-sector-rail" aria-label={t("app.nav.open")}>{sections.flatMap((section) => section.items).map(({ to, label, icon: Icon }) => <NavLink key={to} to={to} end={to === "/"} className={({ isActive }) => `deck-sector${isActive ? " active" : ""}`}><span className="deck-sector-icon"><Icon size={15} /></span><span className="deck-sector-label">{label}</span><span className="deck-sector-blade" aria-hidden="true" /></NavLink>)}</nav>
    <div className="deck-status-cluster">
      <div className={`deck-telemetry is-${tone}`} title={t("dashboard.healthyChannelsHint")}><span className="deck-telemetry-dot" /><span className="deck-telemetry-read">{health.loading ? "···" : `${health.healthy}/${health.total}`}</span><span className="deck-telemetry-label">{t("dashboard.healthyChannels")}</span></div>
      <span className="deck-divider" />
      {update ? <a className="deck-update-pill" href={update.release_url} target="_blank" rel="noreferrer"><ArrowUpRight size={12} />{t("app.updateAvailable", { version: update.latest })}</a> : null}
      <button type="button" className="deck-palette-btn" onClick={onSearch} aria-label={t("command.placeholder")}><Search size={13} /><span>{t("shell.search")}</span><kbd className="deck-kbd">⌘K</kbd></button>
      <button type="button" className="deck-theme-btn" onClick={onThemeChange} aria-label={t(theme === "dark" ? "app.themeLight" : "app.themeDark")}>{theme === "dark" ? <Sun size={14} /> : <Moon size={14} />}</button>
      <LanguageSwitcher className="deck-lang" />
      <button type="button" className="deck-exit-btn" onClick={onDisconnect} aria-label={t("app.disconnect")}><LogOut size={14} /></button>
    </div>
  </header>;
}
