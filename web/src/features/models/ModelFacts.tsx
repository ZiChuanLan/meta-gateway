import { useI18n } from "../../i18n";
import { formatTokens } from "../../lib/format";

/** Public capabilities are the same facts for all roles; unknown is not false. */
export function ModelFacts({
  contextWindow,
  input,
  output,
  thinking,
  compact = false,
}: {
  compact?: boolean;
  contextWindow?: number;
  input?: string;
  output?: string;
  thinking?: number;
}) {
  const { t } = useI18n();
  if (compact) {
    const hasContext = Boolean(contextWindow && contextWindow > 0);
    const hasOutput = Boolean(output && output !== input);
    const hasThinking = thinking === 1;
    if (!hasContext && !hasOutput && !hasThinking) return null;
    return (
      <dl className="model-facts model-facts-compact">
        {hasContext ? (
          <div>
            <dt>{t("modelsPage.metaCtx")}</dt>
            <dd>{formatTokens(contextWindow!)}</dd>
          </div>
        ) : null}
        {hasOutput ? (
          <div>
            <dt>{t("modelsPage.metaOutput")}</dt>
            <dd>{output}</dd>
          </div>
        ) : null}
        {hasThinking ? (
          <div>
            <dt>{t("modelsPage.metaThinking")}</dt>
            <dd>{t("modelsPage.metaThinkingYes")}</dd>
          </div>
        ) : null}
      </dl>
    );
  }
  return (
    <dl className="model-price-grid model-facts">
      <div>
        <dt>{t("modelsPage.metaCtx")}</dt>
        <dd>{contextWindow && contextWindow > 0 ? formatTokens(contextWindow) : "—"}</dd>
      </div>
      <div>
        <dt>{t("modelsPage.metaInput")}</dt>
        <dd>{input || "—"}</dd>
      </div>
      <div>
        <dt>{t("modelsPage.metaOutput")}</dt>
        <dd>{output || "—"}</dd>
      </div>
      <div>
        <dt>{t("modelsPage.metaThinking")}</dt>
        <dd>
          {t(
            thinking === 1
              ? "modelsPage.metaThinkingYes"
              : thinking === 0
                ? "modelsPage.metaThinkingNo"
                : "modelsPage.metaThinkingUnknown",
          )}
        </dd>
      </div>
    </dl>
  );
}
