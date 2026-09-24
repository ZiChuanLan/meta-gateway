import { CalendarCheck, Check, Download, Languages, Moon, Sun } from "lucide-react";
import type { ReactNode } from "react";
import { APPEARANCES, useAppearance } from "../appearance";
import { useI18n } from "../i18n";
import { PALETTES } from "../palettes";
import { UI_THEMES } from "../themes/registry";
import {
  TOP_BAR_ITEMS,
  setTopBarItem,
  useTopBarPrefs,
  type TopBarItemId,
} from "../lib/topBar";

// One glyph per top-bar entry, so the picker reads as a picture of the bar it
// is configuring rather than a list of ids.
const TOP_BAR_ICONS: Record<TopBarItemId, ReactNode> = {
  checkin: <CalendarCheck size={16} />,
  update: <Download size={16} />,
  theme: <Moon size={16} />,
  language: <Languages size={16} />,
};

export function AppearancePanel() {
  const { t } = useI18n();
  const { appearance, scheme, palette, setAppearance, setScheme, setPalette } = useAppearance();
  const topBar = useTopBarPrefs();
  return <section className="appearance-panel">
    <fieldset className="appearance-section">
      <legend>{t("appearance.styles")}</legend>
      <p className="appearance-description">{t("appearance.description")}</p>
      <div className="appearance-cards">
        {APPEARANCES.map((style) => <label key={style} className={`appearance-card${appearance === style ? " is-selected" : ""}`}>
          <input type="radio" name="appearance" value={style} checked={appearance === style} onChange={() => setAppearance(style)} />
          <span className="appearance-preview" data-appearance={style} data-scheme={scheme} aria-hidden="true">
            <span className="appearance-mini-sidebar"><b>MG</b><i /><i /><i /><i /></span>
            <span className="appearance-mini-content"><span className="appearance-mini-header"><b>Meta Gateway</b><i /></span><span className="appearance-mini-title" /><span className="appearance-mini-metrics"><i /><i /><i /></span><span className="appearance-mini-chart"><i /><i /><i /><i /><i /><i /></span></span>
          </span>
          <span className="appearance-card-copy"><strong>{t(UI_THEMES[style].nameKey)}</strong><span className="appearance-selected" aria-hidden="true">{appearance === style ? <Check size={15} /> : null}</span><span>{t(UI_THEMES[style].descriptionKey)}</span><small>{t("appearance.bundled")} · {UI_THEMES[style].version}</small></span>
        </label>)}
      </div>
    </fieldset>
    <fieldset className="appearance-section appearance-mode-section">
      <legend>{t("appearance.mode")}</legend>
      <p className="appearance-description">{t("appearance.modeHint")}</p>
      <div className="appearance-modes">
        <button type="button" aria-pressed={scheme === "light"} onClick={() => setScheme("light")}><Sun size={18} />{t("appearance.light")}{scheme === "light" ? <Check size={14} /> : null}</button>
        <button type="button" aria-pressed={scheme === "dark"} onClick={() => setScheme("dark")}><Moon size={18} />{t("appearance.dark")}{scheme === "dark" ? <Check size={14} /> : null}</button>
      </div>
    </fieldset>
    <fieldset className="appearance-section appearance-mode-section">
      <legend>{t("appearance.palette")}</legend>
      <p className="appearance-description">{t("appearance.paletteHint")}</p>
      <div className="palette-picker">
        {PALETTES.map((option) => <button key={option.id} type="button" className="palette-swatch" data-palette={option.id}
          aria-pressed={palette === option.id} onClick={() => setPalette(option.id)}>
          <span className="palette-swatch-chip" aria-hidden="true" />
          <span className="palette-swatch-name">{t(option.nameKey)}</span>
          {palette === option.id ? <Check size={14} /> : null}
        </button>)}
      </div>
    </fieldset>
    <fieldset className="appearance-section appearance-topbar-section">
      <legend>{t("appearance.topbar")}</legend>
      <p className="appearance-description">{t("appearance.topbarHint")}</p>
      <div className="topbar-picker">
        {TOP_BAR_ITEMS.map((id) => (
          <button
            key={id}
            type="button"
            className="topbar-toggle"
            aria-pressed={topBar[id]}
            onClick={() => setTopBarItem(id, !topBar[id])}
          >
            <span className="topbar-toggle-icon" aria-hidden="true">{TOP_BAR_ICONS[id]}</span>
            <span className="topbar-toggle-copy">
              <strong>{t(`appearance.topbar.${id}`)}</strong>
              <small>{t(`appearance.topbar.${id}Hint`)}</small>
            </span>
            <span className="topbar-toggle-state" aria-hidden="true">
              {topBar[id] ? <Check size={14} /> : null}
            </span>
          </button>
        ))}
      </div>
    </fieldset>
    <p className="appearance-local-note">{t("appearance.localHint")}</p>
  </section>;
}
