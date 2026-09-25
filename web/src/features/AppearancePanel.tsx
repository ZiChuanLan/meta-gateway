import { Check, Download, Languages, Moon, RotateCcw, Search, Sun } from "lucide-react";
import type { ReactNode } from "react";
import { APPEARANCES, useAppearance } from "../appearance";
import { useI18n } from "../i18n";
import { PALETTES } from "../palettes";
import { UI_THEMES } from "../themes/registry";
import { CHROME_NAV_ITEMS } from "../lib/chromeNav";
import {
  TOP_BAR_CONTROLS,
  resetChromePrefs,
  setNavVisible,
  setTopBarControl,
  useChromePrefs,
  type TopBarControlId,
} from "../lib/topBar";

// One glyph per entry, so the picker reads as a picture of the chrome it is
// configuring rather than a list of ids.
const CONTROL_ICONS: Record<TopBarControlId, ReactNode> = {
  search: <Search size={16} />,
  update: <Download size={16} />,
  theme: <Moon size={16} />,
  language: <Languages size={16} />,
};

function ChromeEntryToggle({
  icon,
  label,
  hint,
  on,
  onToggle,
}: {
  icon: ReactNode;
  label: string;
  hint?: string;
  on: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      className="topbar-toggle"
      aria-pressed={on}
      onClick={onToggle}
    >
      <span className="topbar-toggle-icon" aria-hidden="true">{icon}</span>
      <span className="topbar-toggle-copy">
        <strong>{label}</strong>
        {hint ? <small>{hint}</small> : null}
      </span>
      <span className="topbar-toggle-state" aria-hidden="true">
        {on ? <Check size={14} /> : null}
      </span>
    </button>
  );
}

export function AppearancePanel() {
  const { t } = useI18n();
  const { appearance, scheme, palette, setAppearance, setScheme, setPalette } = useAppearance();
  const chrome = useChromePrefs();
  const hiddenNav = new Set(chrome.hiddenNav);
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
			<legend>{t("appearance.chrome.title")}</legend>
			<p className="appearance-description">{t("appearance.chrome.hint")}</p>
			<div className="chrome-pref-group">
				<h4 className="chrome-pref-heading">{t("appearance.chrome.groupBar")}</h4>
				<div className="topbar-picker">
					{TOP_BAR_CONTROLS.map((id) => (
						<ChromeEntryToggle
							key={id}
							icon={CONTROL_ICONS[id]}
							label={t(`appearance.chrome.${id}`)}
							hint={t(`appearance.chrome.${id}Hint`)}
							on={chrome.controls[id]}
							onToggle={() => setTopBarControl(id, !chrome.controls[id])}
						/>
					))}
				</div>
			</div>
			<div className="chrome-pref-group">
				<h4 className="chrome-pref-heading">{t("appearance.chrome.groupNav")}</h4>
				<p className="chrome-pref-hint">{t("appearance.chrome.navHint")}</p>
				<div className="topbar-picker">
					{CHROME_NAV_ITEMS.map((item) => {
						const Icon = item.icon;
						const visible = !hiddenNav.has(item.path);
						return (
							<ChromeEntryToggle
								key={item.path}
								icon={<Icon size={16} />}
								label={t(item.labelKey)}
								on={visible}
								onToggle={() => setNavVisible(item.path, !visible)}
							/>
						);
					})}
				</div>
			</div>
			<button type="button" className="chrome-pref-reset" onClick={resetChromePrefs}>
				<RotateCcw size={14} />
				{t("appearance.chrome.reset")}
			</button>
		</fieldset>
    <p className="appearance-local-note">{t("appearance.localHint")}</p>
  </section>;
}
