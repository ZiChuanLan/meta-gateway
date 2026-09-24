import { CalendarCheck, ChevronRight, Command, LogOut, Menu, Moon, PanelLeftClose, PanelLeftOpen, Search, Sun } from "lucide-react";
import { useState } from "react";
import { NavLink } from "react-router-dom";
import { BrandMark } from "../../components/BrandMark";
import { LanguageSwitcher, useI18n } from "../../i18n";
import { IconButton } from "../../components/ui";
import { useTopBarPrefs } from "../../lib/topBar";
import { UpdateDialog } from "../../features/UpdateDialog";
import type { ThemeChromeProps } from "../types";

export function ModernChrome({ navigation, version, theme, onThemeChange, onSearch, onDisconnect, health, tone, update, collapsed, onCollapse, onOpenNav, sectionLabel, pageLabel }: ThemeChromeProps) {
  const { t } = useI18n();
  // Which entries the operator keeps in the top bar (Settings → Appearance).
  const topBar = useTopBarPrefs();
  // Same as the classic deck: the pill opens the one-click update dialog, not
  // the GitHub release page.
  const [updateOpen, setUpdateOpen] = useState(false);
  return <>
    <aside className="console-sidebar">
      <div className="console-brand"><span className="console-brand-mark" aria-hidden="true"><BrandMark size={23} /></span><span className="console-brand-copy"><strong>Meta Gateway</strong><small>{t("shell.workspace")}</small></span></div>
      <button className="console-search" onClick={onSearch} type="button" aria-label={t("command.placeholder")}><Search size={16} aria-hidden="true" /><span>{t("shell.search")}</span><kbd><Command size={11} /> K</kbd></button>
      {navigation}
      <div className="console-sidebar-foot"><div className={`console-health is-${tone}`} title={t("dashboard.healthyChannelsHint")}><span className="console-health-dot" /><div><span>{t("shell.gatewayStatus")}</span><strong>{health.loading ? "—" : `${health.healthy} / ${health.total}`} <small>{t("dashboard.healthyChannels")}</small></strong></div></div><div className="console-sidebar-meta"><span>{version}</span><IconButton label={t(collapsed ? "shell.expandNav" : "shell.collapseNav")} onClick={onCollapse}>{collapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}</IconButton></div></div>
    </aside>
    <header className="console-topbar">
      <IconButton className="console-mobile-trigger" label={t("app.nav.open")} onClick={onOpenNav}><Menu size={20} /></IconButton>
      <div className="console-breadcrumb"><span>{sectionLabel}</span><ChevronRight size={13} aria-hidden="true" /><strong>{pageLabel}</strong></div>
      <div className="console-global-actions">
        {topBar.update && update ? <button type="button" className="console-update" onClick={() => setUpdateOpen(true)}>{t("app.updateAvailable", { version: update.latest })}</button> : null}
        <IconButton className="console-mobile-search" label={t("command.placeholder")} onClick={onSearch}><Search size={17} /></IconButton>
        {topBar.checkin ? <NavLink className="icon-button console-checkin" to="/checkins" aria-label={t("app.nav.checkins")} title={t("app.nav.checkins")}><CalendarCheck size={17} /></NavLink> : null}
        {topBar.theme ? <IconButton label={t(theme === "dark" ? "app.themeLight" : "app.themeDark")} onClick={onThemeChange}>{theme === "dark" ? <Sun size={17} /> : <Moon size={17} />}</IconButton> : null}
        {topBar.language ? <LanguageSwitcher className="console-language" /> : null}
        <span className="console-action-divider" />
        <IconButton label={t("app.disconnect")} onClick={onDisconnect}><LogOut size={16} /></IconButton>
      </div>
    </header>
    {updateOpen && update ? <UpdateDialog update={update} onClose={() => setUpdateOpen(false)} /> : null}
  </>;
}
