import { Check, Download, Languages, Moon, RotateCcw, Search, Sun } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "../api/client";
import { Button, Field } from "../components/ui";
import { formatErrorMessage } from "../formatError";
import { setCurrency } from "../lib/format";
import { useOptionalSession } from "../session";
import { APPEARANCES, useAppearance } from "../appearance";
import { useI18n } from "../i18n";
import { PALETTES } from "../palettes";
import { UI_THEMES } from "../themes/registry";
import { CHROME_NAV_ITEMS, canAccessNav } from "../lib/chromeNav";
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
    <button type="button" className="topbar-toggle" aria-pressed={on} onClick={onToggle}>
      <span className="topbar-toggle-icon" aria-hidden="true">
        {icon}
      </span>
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

export function AppearancePanel({ modeHiddenNav = [] }: { modeHiddenNav?: string[] }) {
  const session = useOptionalSession();
  const { t } = useI18n();
  const { appearance, scheme, palette, setAppearance, setScheme, setPalette } = useAppearance();
  const chrome = useChromePrefs();
  const hiddenNav = new Set(chrome.hiddenNav);
  const pinnedNav = new Set(chrome.pinnedNav);
  // Entries the gateway's own state hides (the multi-user area while the mode is
  // personal). The caller knows the mode and passes the resulting list, so this
  // panel needs no session of its own.
  const modeHidden = new Set(modeHiddenNav);
  return (
    <section className="appearance-panel">
      <fieldset className="appearance-section">
        <legend>{t("appearance.styles")}</legend>
        <p className="appearance-description">{t("appearance.description")}</p>
        <div className="appearance-cards">
          {APPEARANCES.map((style) => (
            <label
              key={style}
              className={`appearance-card${appearance === style ? " is-selected" : ""}`}
            >
              <input
                type="radio"
                name="appearance"
                value={style}
                checked={appearance === style}
                onChange={() => setAppearance(style)}
              />
              <span
                className="appearance-preview"
                data-appearance={style}
                data-scheme={scheme}
                aria-hidden="true"
              >
                <span className="appearance-mini-sidebar">
                  <b>MG</b>
                  <i />
                  <i />
                  <i />
                  <i />
                </span>
                <span className="appearance-mini-content">
                  <span className="appearance-mini-header">
                    <b>Meta Gateway</b>
                    <i />
                  </span>
                  <span className="appearance-mini-title" />
                  <span className="appearance-mini-metrics">
                    <i />
                    <i />
                    <i />
                  </span>
                  <span className="appearance-mini-chart">
                    <i />
                    <i />
                    <i />
                    <i />
                    <i />
                    <i />
                  </span>
                </span>
              </span>
              <span className="appearance-card-copy">
                <strong>{t(UI_THEMES[style].nameKey)}</strong>
                <span className="appearance-selected" aria-hidden="true">
                  {appearance === style ? <Check size={15} /> : null}
                </span>
                <span>{t(UI_THEMES[style].descriptionKey)}</span>
                <small>
                  {t("appearance.bundled")} · {UI_THEMES[style].version}
                </small>
              </span>
            </label>
          ))}
        </div>
      </fieldset>
      <fieldset className="appearance-section appearance-mode-section">
        <legend>{t("appearance.mode")}</legend>
        <p className="appearance-description">{t("appearance.modeHint")}</p>
        <div className="appearance-modes">
          <button
            type="button"
            aria-pressed={scheme === "light"}
            onClick={() => setScheme("light")}
          >
            <Sun size={18} />
            {t("appearance.light")}
            {scheme === "light" ? <Check size={14} /> : null}
          </button>
          <button type="button" aria-pressed={scheme === "dark"} onClick={() => setScheme("dark")}>
            <Moon size={18} />
            {t("appearance.dark")}
            {scheme === "dark" ? <Check size={14} /> : null}
          </button>
        </div>
      </fieldset>
      <fieldset className="appearance-section appearance-mode-section">
        <legend>{t("appearance.palette")}</legend>
        <p className="appearance-description">{t("appearance.paletteHint")}</p>
        <div className="palette-picker">
          {PALETTES.map((option) => (
            <button
              key={option.id}
              type="button"
              className="palette-swatch"
              data-palette={option.id}
              aria-pressed={palette === option.id}
              onClick={() => setPalette(option.id)}
            >
              <span className="palette-swatch-chip" aria-hidden="true" />
              <span className="palette-swatch-name">{t(option.nameKey)}</span>
              {palette === option.id ? <Check size={14} /> : null}
            </button>
          ))}
        </div>
      </fieldset>
      <fieldset className="appearance-section">
        <legend>{t("appearance.currency")}</legend>
        <p className="appearance-description">{t("appearance.currencyHint")}</p>
        <CurrencySettings />
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
            {CHROME_NAV_ITEMS.filter((item) => !session || canAccessNav(item, session.role)).map(
              (item) => {
                const Icon = item.icon;
                // An entry can be hidden by gateway state rather than by a
                // preference — the multi-user area while the mode is personal.
                // The switch still works; turning it on pins the entry back.
                const gated = modeHidden.has(item.path);
                const visible = !hiddenNav.has(item.path) && (!gated || pinnedNav.has(item.path));
                return (
                  <ChromeEntryToggle
                    key={item.path}
                    icon={<Icon size={16} />}
                    label={t(item.labelKey)}
                    on={visible}
                    onToggle={() => setNavVisible(item.path, !visible, gated)}
                  />
                );
              },
            )}
          </div>
        </div>
        <button type="button" className="chrome-pref-reset" onClick={resetChromePrefs}>
          <RotateCcw size={14} />
          {t("appearance.chrome.reset")}
        </button>
      </fieldset>
      <p className="appearance-local-note">{t("appearance.localHint")}</p>
    </section>
  );
}

/**
 * Money presentation: the symbol and rate every amount is rendered through.
 *
 * Stored amounts stay in the ledger's unit, so switching currency rewrites
 * nothing — it only changes what the console and the member app print. The
 * preview line shows the effect on a real number instead of asking the
 * operator to imagine it.
 */
function CurrencySettings() {
  const { t } = useI18n();
  const { client } = useOptionalSession() ?? { client: null };
  // The console's API surface is built from the live session client; without
  // one (still connecting, or rendered outside a session) the query stays
  // disabled rather than crashing the panel.
  const service = useMemo(() => (client ? api(client) : null), [client]);
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: ["display-settings"],
    queryFn: ({ signal }) => service!.displaySettings(signal),
    enabled: Boolean(service),
  });
  const [symbol, setSymbol] = useState("");
  const [rate, setRate] = useState("");
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<unknown>(null);

  // Seed the form from the server, and keep the formatter in sync so every
  // amount already on screen re-renders through the new symbol.
  useEffect(() => {
    if (!settings.data) return;
    setSymbol(settings.data.symbol);
    setRate(String(settings.data.rate));
    setCurrency(settings.data);
  }, [settings.data]);

  const save = useMutation({
    mutationFn: () => service!.saveDisplaySettings({ symbol, rate: Number(rate) || 1 }),
    onSuccess: async (next) => {
      if (next) setCurrency(next);
      setSaved(true);
      setError(null);
      // Amounts live in list caches; refetching them re-renders with the new
      // symbol without a page reload.
      await queryClient.invalidateQueries();
    },
    onError: (err) => {
      setError(err);
      setSaved(false);
    },
  });

  return (
    <div className="currency-settings">
      <div className="form-grid">
        <Field label={t("appearance.currencySymbol")} hint={t("appearance.currencySymbolHint")}>
          <input
            value={symbol}
            maxLength={8}
            onChange={(e) => {
              setSymbol(e.target.value);
              setSaved(false);
            }}
            placeholder="$"
          />
        </Field>
        <Field label={t("appearance.currencyRate")} hint={t("appearance.currencyRateHint")}>
          <input
            type="number"
            min={0.0001}
            step="0.01"
            value={rate}
            onChange={(e) => {
              setRate(e.target.value);
              setSaved(false);
            }}
            placeholder="1"
          />
        </Field>
      </div>
      <p className="appearance-description">
        {t("appearance.currencyPreview", {
          sample: `${symbol || "$"}${((Number(rate) || 1) * 0.42).toFixed(2)}`,
        })}
      </p>
      {error ? (
        <p className="currency-note is-error" role="alert">
          {formatErrorMessage(error, t)}
        </p>
      ) : null}
      <div className="currency-actions">
        <Button
          type="button"
          loading={save.isPending}
          disabled={save.isPending || !symbol.trim()}
          onClick={() => save.mutate()}
        >
          {t("appearance.currencySave")}
        </Button>
        {saved ? <span className="currency-note">{t("appearance.currencySaved")}</span> : null}
      </div>
    </div>
  );
}
