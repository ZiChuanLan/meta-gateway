import { useI18n } from "../../i18n";
import type { ModelChannelMatch, ModelMatchMode } from "../../api/types";

/**
 * The match-scope switch, shared by the add-route dialog and the attach dialog
 * so the two can never disagree about what a scope means.
 *
 * Three scopes, narrowest to widest: the name itself, the pattern's prefix
 * siblings, and any name containing the pattern. Contains is the default
 * because a real catalog prefixes and namespaces — "deepseek" reaches
 * "deepseek-chat", "deepseek-ai/deepseek-v4-flash" and "cn:deepseek-r1" — and
 * "every channel that can serve this family" is the question both dialogs ask.
 * The list underneath shows what the choice would attach, before anything is
 * saved, so widening it is a decision the operator can still see.
 */
export function MatchScopeFieldset({
  radioName,
  pattern,
  value,
  onChange,
}: {
  radioName: string;
  pattern: string;
  value: ModelMatchMode;
  onChange: (mode: ModelMatchMode) => void;
}) {
  const { t } = useI18n();
  const options: Array<{ mode: ModelMatchMode; label: string }> = [
    { mode: "exact", label: t("modelsPage.autoMatch.modeExact") },
    { mode: "related", label: t("modelsPage.autoMatch.modeRelated", { name: pattern }) },
    { mode: "contains", label: t("modelsPage.autoMatch.modeContains", { name: pattern }) },
  ];
  return (
    <fieldset className="match-mode">
      <legend className="ops-panel-context">{t("modelsPage.autoMatch.modeLabel")}</legend>
      {options.map(({ mode, label }) => (
        <label className="check" key={mode}>
          <input
            type="radio"
            name={radioName}
            checked={value === mode}
            onChange={() => onChange(mode)}
          />
          <span>{label}</span>
        </label>
      ))}
      {value !== "exact" ? (
        <p className="ops-panel-context">
          {t("modelsPage.autoMatch.modeRewriteHint", { name: pattern })}
        </p>
      ) : null}
    </fieldset>
  );
}

/** What a match was found in, phrased for the chip beside a channel's name. */
export function matchSourceLabel(
  source: ModelChannelMatch["source"],
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  switch (source) {
    case "routed":
      return t("modelsPage.autoMatch.sourceRouted");
    case "discovered":
      return t("modelsPage.autoMatch.sourceDiscovered");
    default:
      return t("modelsPage.autoMatch.sourceModels");
  }
}
