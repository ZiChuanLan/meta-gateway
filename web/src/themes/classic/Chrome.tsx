import { LogOut, Menu, Moon, Search, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { BrandMark } from "../../components/BrandMark";
import { NavLink, useLocation } from "react-router-dom";
import { LanguageSwitcher, useI18n } from "../../i18n";
import { useHorizontalWheel } from "../../hooks/useHorizontalWheel";
import { useTopBarControls } from "../../lib/topBar";
import { UpdateDialog } from "../../features/UpdateDialog";
import type { ThemeChromeProps } from "../types";

export function ClassicChrome({ sections, version, theme, onThemeChange, onSearch, onDisconnect, health, tone, update, onOpenNav }: ThemeChromeProps) {
  const { t } = useI18n();
  const topBar = useTopBarControls();
  const railRef = useHorizontalWheel<HTMLElement>();
  const { pathname } = useLocation();
  // The pill opens the one-click update dialog instead of linking out to
  // GitHub: what the version changes and the apply button belong behind one
  // click, not on another site.
  const [updateOpen, setUpdateOpen] = useState(false);
  // The rail scrolls, so it also has to show where the route landed: reaching an
  // item past the edge (a plugin page, say) would otherwise leave the highlight
  // off-screen with no sign of it. "nearest" keeps the page itself still.
  useEffect(() => {
    const active = railRef.current?.querySelector<HTMLElement>(".deck-sector.active");
    if (typeof active?.scrollIntoView === "function") {
      active.scrollIntoView({ inline: "nearest", block: "nearest" });
    }
  }, [pathname, railRef]);
  return <header className="gate-console-deck">
    <div className="deck-identity"><div className="brand-mark" aria-hidden="true"><BrandMark size={18} /></div><div className="deck-identity-copy"><strong>META GATEWAY</strong><span>OPERATIONS CONSOLE // {version}</span></div></div>
    <button type="button" className="classic-mobile-trigger" onClick={onOpenNav} aria-label={t("app.nav.open")}><Menu size={19} /></button>
    <nav ref={railRef} className="deck-sector-rail" aria-label={t("app.nav.open")}>{sections.flatMap((section) => section.items).map(({ to, label, icon: Icon }) => <NavLink key={to} to={to} end={to === "/"} className={({ isActive }) => `deck-sector${isActive ? " active" : ""}`}><span className="deck-sector-icon"><Icon size={15} /></span><span className="deck-sector-label">{label}</span><span className="deck-sector-blade" aria-hidden="true" /></NavLink>)}</nav>
    <div className="deck-status-cluster">
      <div className={`deck-telemetry is-${tone}`} title={t("dashboard.healthyChannelsHint")}><span className="deck-telemetry-dot" /><span className="deck-telemetry-read">{health.loading ? "···" : `${health.healthy}/${health.total}`}</span><span className="deck-telemetry-label">{t("dashboard.healthyChannels")}</span></div>
      <span className="deck-divider" />
      {topBar.update && update ? <button type="button" className="deck-update-pill" onClick={() => setUpdateOpen(true)}>{t("app.updateAvailable", { version: update.latest })}</button> : null}
      {topBar.search ? <button type="button" className="deck-palette-btn" onClick={onSearch} aria-label={t("command.placeholder")}><Search size={13} /><span>{t("shell.search")}</span><kbd className="deck-kbd">⌘K</kbd></button> : null}
      {topBar.theme ? <button type="button" className="deck-theme-btn" onClick={onThemeChange} aria-label={t(theme === "dark" ? "app.themeLight" : "app.themeDark")}>{theme === "dark" ? <Sun size={14} /> : <Moon size={14} />}</button> : null}
      {topBar.language ? <LanguageSwitcher className="deck-lang" /> : null}
      <button type="button" className="deck-exit-btn" onClick={onDisconnect} aria-label={t("app.disconnect")}><LogOut size={14} /></button>
    </div>
    {updateOpen && update ? <UpdateDialog update={update} onClose={() => setUpdateOpen(false)} /> : null}
  </header>;
}
