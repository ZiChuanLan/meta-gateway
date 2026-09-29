import { ChevronDown, Plus, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { useI18n } from "../../i18n";
import { Field, InfoTip } from "../../components/ui";
import {
  CLIENT_PATH_SUGGESTIONS,
  ENDPOINT_MAP_PRESETS,
  parseEndpointMap,
  serializeFieldMap,
  serializePathMap,
  summarizeEndpoints,
  validateFieldMapRows,
  validatePathMapRows,
  type EndpointMapValue,
  type FieldMapMode,
  type FieldMapRow,
  type PathMapRow,
  type ValueType,
} from "./endpointMap";

const FIELD_MODE_KEYS: Record<FieldMapMode, string> = {
  copy: "channels.mapModeCopy",
  move: "channels.mapModeMove",
  template: "channels.mapModeTemplate",
  value: "channels.mapModeValue",
  keep: "channels.mapModeKeep",
};

const VALUE_TYPE_KEYS: Record<ValueType, string> = {
  str: "channels.mapValueStr",
  num: "channels.mapValueNum",
  bool: "channels.mapValueBool",
  null: "channels.mapValueNull",
};

const PRESET_KEYS: Record<string, string> = {
  keepTopLevel: "channels.mapPresetKeep",
  wrapMessages: "channels.mapPresetWrap",
  pathRename: "channels.mapPresetPath",
};

const emptyFieldRow = (): FieldMapRow => ({
  mode: "copy",
  from: "",
  to: "",
  template: "",
  valueType: "str",
  valueText: "",
  keep: "",
});

/**
 * Structured editor for the four endpoint-mapping columns.
 *
 * The operator edits rows; the component keeps the stored value in the JSON
 * form the backend validator expects (serialize* round-trips byte-identically,
 * sorted keys included). A column whose text does not parse keeps its raw text
 * and falls back to a plain editor for that column only, so nothing an operator
 * typed is ever dropped by opening the dialog.
 */
export function EndpointMapEditor({
  value,
  onChange,
  disabled,
}: {
  value: EndpointMapValue;
  onChange: (next: EndpointMapValue) => void;
  disabled?: boolean;
}) {
  const { t } = useI18n();
  const [mode, setMode] = useState<"visual" | "json">("visual");
  const [presetOpen, setPresetOpen] = useState(false);

  const model = useMemo(() => parseEndpointMap(value), [value]);
  const summary = useMemo(() => summarizeEndpoints(value), [value]);
  const pathIssues = useMemo(() => validatePathMapRows(model.pathMap), [model.pathMap]);
  const requestIssues = useMemo(() => validateFieldMapRows(model.requestMap), [model.requestMap]);
  const responseIssues = useMemo(() => validateFieldMapRows(model.responseMap), [model.responseMap]);

  const overrideIssue = /\s/.test(value.pathOverride.trim()) ? t("channels.mapOverrideWhitespace") : "";
  const brokenColumn = Boolean(model.pathMapError || model.requestMapError || model.responseMapError);

  const updatePathMap = (rows: PathMapRow[]) => onChange({ ...value, pathMap: serializePathMap(rows) });
  const updateFieldMap = (column: "requestMap" | "responseMap", rows: FieldMapRow[]) =>
    onChange({ ...value, [column]: serializeFieldMap(rows) });
  const applyPreset = (id: string) => {
    const preset = ENDPOINT_MAP_PRESETS.find((entry) => entry.id === id);
    if (!preset) return;
    onChange({
      ...value,
      ...(preset.pathMap !== undefined ? { pathMap: preset.pathMap } : {}),
      ...(preset.requestMap !== undefined ? { requestMap: preset.requestMap } : {}),
      ...(preset.responseMap !== undefined ? { responseMap: preset.responseMap } : {}),
    });
    setPresetOpen(false);
  };

  const issueFor = (issues: { index: number; message: string }[], index: number) =>
    issues.find((issue) => issue.index === index)?.message;

  const jsonColumn = (
    labelKey: string,
    hintKey: string,
    key: "pathMap" | "requestMap" | "responseMap",
    error: string | null,
    className: string
  ) => (
    <Field
      label={t(labelKey)}
      hint={t(hintKey)}
      className={error ? "is-invalid" : undefined}
    >
      <textarea
        className={`mono ${className}`}
        value={value[key]}
        onChange={(event) => onChange({ ...value, [key]: event.target.value })}
        disabled={disabled}
        spellCheck={false}
      />
      {error ? <p className="map-row-error">{t("channels.mapJsonInvalid", { message: error })}</p> : null}
    </Field>
  );

  return (
    <div className="map-editor">
      <div className="map-editor-bar">
        <div className="map-editor-modes" role="tablist" aria-label={t("channels.endpointMap")}>
          {(["visual", "json"] as const).map((candidate) => (
            <button
              key={candidate}
              type="button"
              role="tab"
              aria-selected={mode === candidate}
              className={`map-editor-mode${mode === candidate ? " is-active" : ""}`}
              onClick={() => setMode(candidate)}
              disabled={disabled}
            >
              {t(candidate === "visual" ? "channels.mapModeVisual" : "channels.mapModeJson")}
            </button>
          ))}
        </div>
        <div className="map-editor-badges">
          {summary.pathCount > 0 ? (
            <span className="map-badge">{t("channels.mapBadgePaths", { n: summary.pathCount })}</span>
          ) : null}
          {summary.requestCount > 0 ? (
            <span className="map-badge">{t("channels.mapBadgeRequest", { n: summary.requestCount })}</span>
          ) : null}
          {summary.responseCount > 0 ? (
            <span className="map-badge">{t("channels.mapBadgeResponse", { n: summary.responseCount })}</span>
          ) : null}
          {summary.keepCount > 0 ? (
            <span className="map-badge is-key">{t("channels.mapBadgeKeep", { n: summary.keepCount })}</span>
          ) : null}
          {summary.issues + (overrideIssue ? 1 : 0) > 0 ? (
            <span className="map-badge is-invalid">
              {t("channels.mapBadgeIssues", { n: summary.issues + (overrideIssue ? 1 : 0) })}
            </span>
          ) : null}
          {!summary.hasOverride && summary.pathCount + summary.requestCount + summary.responseCount === 0 ? (
            <span className="map-badge is-quiet">{t("channels.mapBadgeNone")}</span>
          ) : null}
        </div>
        <div className="map-editor-preset">
          <button
            type="button"
            className={`advanced-toggle${presetOpen ? " is-open" : ""}`}
            aria-expanded={presetOpen}
            onClick={() => setPresetOpen((open) => !open)}
            disabled={disabled}
          >
            <ChevronDown size={13} />
            {t("channels.mapPreset")}
          </button>
          {presetOpen ? (
            <ul className="map-preset-list">
              {ENDPOINT_MAP_PRESETS.map((preset) => (
                <li key={preset.id}>
                  <button type="button" onClick={() => applyPreset(preset.id)} disabled={disabled}>
                    {t(PRESET_KEYS[preset.id] ?? preset.id)}
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </div>

      <Field
        label={t("channels.pathOverride")}
        hint={t("channels.pathOverrideHint")}
        className={overrideIssue ? "is-invalid" : undefined}
      >
        <input
          value={value.pathOverride}
          onChange={(event) => onChange({ ...value, pathOverride: event.target.value })}
          disabled={disabled}
          placeholder="systemone"
          className="mono"
        />
        {overrideIssue ? <p className="map-row-error">{overrideIssue}</p> : null}
      </Field>

      {mode === "json" ? (
        <div className="form-grid form-grid-single">
          {jsonColumn("channels.pathMap", "channels.pathMapHint", "pathMap", model.pathMapError, "textarea-md")}
          {jsonColumn("channels.requestMap", "channels.requestMapHint", "requestMap", model.requestMapError, "textarea-lg")}
          {jsonColumn("channels.responseMap", "channels.responseMapHint", "responseMap", model.responseMapError, "textarea-lg")}
        </div>
      ) : (
        <>
          <section className="map-section" aria-label={t("channels.pathMapSection")}>
            <div className="map-section-head">
              <h4>{t("channels.pathMapSection")}</h4>
              <InfoTip label={t("channels.pathMapHint")} />
              <button
                type="button"
                className="map-row-add"
                onClick={() => updatePathMap([...model.pathMap, { from: "", to: "" }])}
                disabled={disabled}
              >
                <Plus size={12} />
                {t("channels.mapRowAdd")}
              </button>
            </div>
            {model.pathMapError ? (
              <p className="map-row-error">{t("channels.mapJsonInvalid", { message: model.pathMapError })}</p>
            ) : null}
            {model.pathMap.length === 0 ? (
              <p className="map-section-empty">{t("channels.mapPathEmpty")}</p>
            ) : (
              <ul className="map-rows">
                {model.pathMap.map((row, index) => (
                  <li key={index} className={`map-row${issueFor(pathIssues, index) ? " is-invalid" : ""}`}>
                    <input
                      list="map-client-paths"
                      className="mono"
                      value={row.from}
                      aria-label={t("channels.mapFrom")}
                      placeholder="chat/completions"
                      onChange={(event) =>
                        updatePathMap(
                          model.pathMap.map((entry, i) => (i === index ? { ...entry, from: event.target.value } : entry))
                        )
                      }
                      disabled={disabled}
                    />
                    <span className="map-row-arrow" aria-hidden="true">
                      →
                    </span>
                    <input
                      className="mono"
                      value={row.to}
                      aria-label={t("channels.mapTo")}
                      placeholder="v1/systemone"
                      onChange={(event) =>
                        updatePathMap(
                          model.pathMap.map((entry, i) => (i === index ? { ...entry, to: event.target.value } : entry))
                        )
                      }
                      disabled={disabled}
                    />
                    <button
                      type="button"
                      className="map-row-remove"
                      aria-label={t("channels.mapRowRemove")}
                      onClick={() => updatePathMap(model.pathMap.filter((_, i) => i !== index))}
                      disabled={disabled}
                    >
                      <Trash2 size={13} />
                    </button>
                    {issueFor(pathIssues, index) ? (
                      <p className="map-row-error">{issueFor(pathIssues, index)}</p>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
            <datalist id="map-client-paths">
              {CLIENT_PATH_SUGGESTIONS.map((path) => (
                <option key={path} value={path} />
              ))}
            </datalist>
            <p className="map-section-hint">{t("channels.mapPathRowHint")}</p>
          </section>

          <FieldMapSection
            titleKey="channels.requestMapSection"
            hintKey="channels.requestMapHint"
            rows={model.requestMap}
            issues={requestIssues}
            error={model.requestMapError}
            disabled={disabled}
            onChange={(rows) => updateFieldMap("requestMap", rows)}
          />
          <FieldMapSection
            titleKey="channels.responseMapSection"
            hintKey="channels.responseMapHint"
            rows={model.responseMap}
            issues={responseIssues}
            error={model.responseMapError}
            disabled={disabled}
            onChange={(rows) => updateFieldMap("responseMap", rows)}
          />
        </>
      )}

      {mode === "visual" && brokenColumn ? (
        <p className="map-section-hint">{t("channels.mapJsonFallbackHint")}</p>
      ) : null}
    </div>
  );
}

function FieldMapSection({
  titleKey,
  hintKey,
  rows,
  issues,
  error,
  disabled,
  onChange,
}: {
  titleKey: string;
  hintKey: string;
  rows: FieldMapRow[];
  issues: { index: number; message: string }[];
  error: string | null;
  disabled?: boolean;
  onChange: (rows: FieldMapRow[]) => void;
}) {
  const { t } = useI18n();
  const issueFor = (index: number) => issues.find((issue) => issue.index === index)?.message;
  const patch = (index: number, partial: Partial<FieldMapRow>) =>
    onChange(rows.map((row, i) => (i === index ? { ...row, ...partial } : row)));

  return (
    <section className="map-section" aria-label={t(titleKey)}>
      <div className="map-section-head">
        <h4>{t(titleKey)}</h4>
        <InfoTip label={t(hintKey)} />
        <button
          type="button"
          className="map-row-add"
          onClick={() => onChange([...rows, emptyFieldRow()])}
          disabled={disabled}
        >
          <Plus size={12} />
          {t("channels.mapRowAdd")}
        </button>
      </div>
      {error ? <p className="map-row-error">{t("channels.mapJsonInvalid", { message: error })}</p> : null}
      {rows.length === 0 ? (
        <p className="map-section-empty">{t("channels.mapFieldsEmpty")}</p>
      ) : (
        <ul className="map-rows">
          {rows.map((row, index) => (
            <li key={index} className={`map-row map-row-forward${issueFor(index) ? " is-invalid" : ""}`}>
              <select
                className="map-row-mode"
                value={row.mode}
                aria-label={t("channels.mapRowMode")}
                onChange={(event) => patch(index, { mode: event.target.value as FieldMapMode })}
                disabled={disabled}
              >
                {(Object.keys(FIELD_MODE_KEYS) as FieldMapMode[]).map((mode) => (
                  <option key={mode} value={mode}>
                    {t(FIELD_MODE_KEYS[mode])}
                  </option>
                ))}
              </select>

			  {row.mode === "keep" ? (
				<input
				  className="mono map-row-grow"
				  value={row.keep}
				  aria-label={t("channels.mapKeep")}
				  placeholder="model, state"
				  onChange={(event) => patch(index, { keep: event.target.value })}
				  disabled={disabled}
				/>
			  ) : (
				<>
				  {row.mode === "copy" || row.mode === "move" ? (
					<input
					  className="mono"
					  value={row.from}
					  aria-label={t("channels.mapFrom")}
					  placeholder="messages.0.content"
					  onChange={(event) => patch(index, { from: event.target.value })}
					  disabled={disabled}
					/>
				  ) : null}

				  {row.mode === "template" ? (
					<input
					  className="mono map-row-grow"
					  value={row.template}
					  aria-label={t("channels.mapTemplate")}
					  placeholder={'[{"role":"user","content":"{messages.0.content}"}]'}
					  onChange={(event) => patch(index, { template: event.target.value })}
					  disabled={disabled}
					/>
				  ) : null}

				  {row.mode === "value" ? (
					<>
					  <select
						className="map-row-mode"
						value={row.valueType}
						aria-label={t("channels.mapValueType")}
						onChange={(event) => patch(index, { valueType: event.target.value as ValueType })}
						disabled={disabled}
					  >
						{(Object.keys(VALUE_TYPE_KEYS) as ValueType[]).map((type) => (
						  <option key={type} value={type}>
							{t(VALUE_TYPE_KEYS[type])}
						  </option>
						))}
					  </select>
					  {row.valueType === "bool" ? (
						<select
						  className="map-row-mode"
						  value={row.valueText}
						  aria-label={t("channels.mapValue")}
						  onChange={(event) => patch(index, { valueText: event.target.value })}
						  disabled={disabled}
						>
						  <option value="true">true</option>
						  <option value="false">false</option>
						</select>
					  ) : row.valueType === "null" ? null : (
						<input
						  className="mono"
						  value={row.valueText}
						  aria-label={t("channels.mapValue")}
						  placeholder={row.valueType === "num" ? "8000" : "text"}
						  inputMode={row.valueType === "num" ? "decimal" : undefined}
						  onChange={(event) => patch(index, { valueText: event.target.value })}
						  disabled={disabled}
						/>
					  )}
					</>
				  ) : null}

				  <span className="map-row-arrow" aria-hidden="true">
					→
				  </span>
				  <input
					className="mono"
					value={row.to}
					aria-label={t("channels.mapTo")}
					placeholder={row.mode === "value" ? "questions.ask" : "state"}
					onChange={(event) => patch(index, { to: event.target.value })}
					disabled={disabled}
				  />
				</>
			  )}

              <button
                type="button"
                className="map-row-remove"
                aria-label={t("channels.mapRowRemove")}
                onClick={() => onChange(rows.filter((_, i) => i !== index))}
                disabled={disabled}
              >
                <Trash2 size={13} />
              </button>
              {issueFor(index) ? <p className="map-row-error">{issueFor(index)}</p> : null}
            </li>
          ))}
        </ul>
      )}
      <p className="map-section-hint">{t("channels.mapFieldRowHint")}</p>
    </section>
  );
}
