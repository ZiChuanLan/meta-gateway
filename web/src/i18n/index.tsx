import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"

import { en } from "./en"
import { zh } from "./zh"
import type { Dict, Locale } from "./types"

export type { Locale } from "./types"

const LOCALE_KEY = "meta-gateway.locale"

const dictionaries: Record<Locale, Dict> = { en, "zh-CN": zh };

export function detectLocale(): Locale {
  try {
    const saved = localStorage.getItem(LOCALE_KEY);
    if (saved === "en" || saved === "zh-CN") return saved;
  } catch {
    /* ignore */
  }
  try {
    const lang = navigator.language || "";
    if (lang.toLowerCase().startsWith("zh")) return "zh-CN";
  } catch {
    /* ignore */
  }
  return "en";
}

export function translate(
  locale: Locale,
  key: string,
  vars?: Record<string, string | number>,
): string {
  const template = dictionaries[locale][key] ?? dictionaries.en[key] ?? key;
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (_, name: string) => {
    const value = vars[name];
    return value === undefined ? `{${name}}` : String(value);
  });
}

function statusLabel(locale: Locale, value: string | boolean): string {
  if (typeof value === "boolean")
    return translate(locale, value ? "status.true" : "status.false");
  const normalized = String(value).toLowerCase();
  const mapped =
    dictionaries[locale][`status.${normalized}`] ??
    dictionaries.en[`status.${normalized}`];
  return mapped ?? String(value);
}

interface I18nValue {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: string, vars?: Record<string, string | number>) => string;
  status: (value: string | boolean) => string;
}

const I18nContext = createContext<I18nValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(() => detectLocale());

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next);
    try {
      localStorage.setItem(LOCALE_KEY, next);
    } catch {
      /* ignore */
    }
  }, []);

  useEffect(() => {
    document.documentElement.lang = locale === "zh-CN" ? "zh-CN" : "en";
  }, [locale]);

  const value = useMemo<I18nValue>(
    () => ({
      locale,
      setLocale,
      t: (key, vars) => translate(locale, key, vars),
      status: (v) => statusLabel(locale, v),
    }),
    [locale, setLocale],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const value = useContext(I18nContext);
  if (!value) throw new Error("useI18n must be used inside I18nProvider");
  return value;
}

export function LanguageSwitcher({ className = "" }: { className?: string }) {
  const { locale, setLocale, t } = useI18n();
  return (
    <label className={`language-switcher ${className}`.trim()}>
      <span className="sr-only">{t("lang.switch")}</span>
      <select
        aria-label={t("lang.switch")}
        value={locale}
        onChange={(e) => setLocale(e.target.value as Locale)}
      >
        <option value="en">{t("lang.en")}</option>
        <option value="zh-CN">{t("lang.zh")}</option>
      </select>
    </label>
  );
}
