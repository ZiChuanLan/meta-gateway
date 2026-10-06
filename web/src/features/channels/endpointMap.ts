/**
 * Endpoint-mapping model for the channel editor.
 *
 * The four mapping columns (upstream_path_override / upstream_path_map /
 * upstream_request_map / upstream_response_map) are stored as JSON and
 * validated server-side in internal/proxy/upstream_map_validate.go. This module
 * is the console's mirror of that validator: it turns the stored JSON into
 * editable rows, serializes rows back into the same canonical JSON (sorted path
 * keys, trimmed values — byte-identical to what the backend stores), and
 * reports the same rules inline so a typo is visible before saving instead of
 * silently doing nothing at request time.
 *
 * A malformed value never disappears: parseEndpointMap keeps the raw text and
 * flags the error, and the editor falls back to its JSON mode for that column.
 */

export const FIELD_MAP_MODES = ["copy", "move", "template", "value", "keep"] as const;
export type FieldMapMode = (typeof FIELD_MAP_MODES)[number];

export type PathMapRow = {
  from: string;
  to: string;
};

export type ValueType = "str" | "num" | "bool" | "null";

export type FieldMapRow = {
  mode: FieldMapMode;
  from: string;
  to: string;
  template: string;
  valueType: ValueType;
  /** Raw text for str/num/bool editing; ignored for null. */
  valueText: string;
  /** Comma-separated top-level keys for the keep mode. */
  keep: string;
};

export type EndpointMapValue = {
  pathOverride: string;
  pathMap: string;
  requestMap: string;
  responseMap: string;
};

export type ParseResult<T> = {
  rows: T;
  /** Present when the stored text could not be read as the expected shape. */
  error: string | null;
};

/** Common client-side paths, offered as suggestions (values are free text). */
export const CLIENT_PATH_SUGGESTIONS = [
  "chat/completions",
  "responses",
  "messages",
  "completions",
  "embeddings",
  "models",
  "images/generations",
  "images/edits",
  "audio/transcriptions",
  "rerank",
];

/**
 * validateJSONPath mirrors ValidateJSONPath: balanced brackets, no empty
 * segment, no unbracketed space. Returns null when the path is well formed.
 */
export function validateJSONPath(path: string): string | null {
  const trimmed = path.trim();
  if (!trimmed) return "path is required";
  let depth = 0;
  let segment = "";
  let error: string | null = null;
  const flush = () => {
    if (segment === "") error = `path "${trimmed}" has an empty segment`;
    segment = "";
  };
  for (const char of trimmed) {
    if (char === ".") {
      if (depth > 0) {
        segment += char;
        continue;
      }
      flush();
    } else if (char === "[") {
      if (depth > 0) return `path "${trimmed}" nests brackets`;
      flush();
      depth += 1;
    } else if (char === "]") {
      if (depth === 0) return `path "${trimmed}" closes a bracket that was never opened`;
      depth -= 1;
    } else if (char === " ") {
      if (depth === 0) return `path "${trimmed}" contains a space`;
      segment += char;
    } else {
      segment += char;
    }
    if (error) return error;
  }
  if (depth !== 0) return `path "${trimmed}" has an unclosed bracket`;
  if (segment === "" && !trimmed.endsWith("]")) {
    return `path "${trimmed}" ends with an empty segment`;
  }
  return null;
}

/** normalizePathKey trims a path-map key the way the backend stores it. */
export function normalizePathKey(key: string): string {
  return key.trim().replace(/^\/+/, "").replace(/\/+$/, "");
}

// ---- endpoint map (all four columns) ----

export type EndpointMapModel = {
  pathOverride: string;
  pathMap: PathMapRow[];
  pathMapError: string | null;
  requestMap: FieldMapRow[];
  requestMapError: string | null;
  responseMap: FieldMapRow[];
  responseMapError: string | null;
};

export function parseEndpointMap(value: EndpointMapValue): EndpointMapModel {
  const pathMap = parsePathMap(value.pathMap);
  const requestMap = parseFieldMap(value.requestMap);
  const responseMap = parseFieldMap(value.responseMap);
  return {
    pathOverride: value.pathOverride,
    pathMap: pathMap.rows,
    pathMapError: pathMap.error,
    requestMap: requestMap.rows,
    requestMapError: requestMap.error,
    responseMap: responseMap.rows,
    responseMapError: responseMap.error,
  };
}

export function parsePathMap(raw: string): ParseResult<PathMapRow[]> {
  const trimmed = raw.trim();
  if (trimmed === "" || trimmed === "{}") return { rows: [], error: null };
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch (err) {
    return { rows: [], error: `invalid JSON: ${(err as Error).message}` };
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    return { rows: [], error: 'must be an object of {"<client path>":"<upstream path>"}' };
  }
  const rows: PathMapRow[] = Object.entries(parsed as Record<string, unknown>).map(
    ([from, to]) => ({
      from: normalizePathKey(from),
      to: typeof to === "string" ? to : String(to ?? ""),
    }),
  );
  return { rows, error: null };
}

/** serializePathMap mirrors the backend: sorted keys, trimmed values. */
export function serializePathMap(rows: PathMapRow[]): string {
  const entries = rows
    .map((row) => [normalizePathKey(row.from), row.to.trim()] as const)
    .filter(([key, value]) => key !== "" || value !== "");
  if (entries.length === 0) return "";
  entries.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
  const object: Record<string, string> = {};
  for (const [key, value] of entries) object[key] = value;
  return JSON.stringify(object, null, 2);
}

export function parseFieldMap(raw: string): ParseResult<FieldMapRow[]> {
  const trimmed = raw.trim();
  if (trimmed === "" || trimmed === "[]") return { rows: [], error: null };
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch (err) {
    return { rows: [], error: `invalid JSON: ${(err as Error).message}` };
  }
  if (!Array.isArray(parsed)) return { rows: [], error: "must be a JSON array of field maps" };
  const rows: FieldMapRow[] = [];
  for (const entry of parsed) {
    if (entry === null || typeof entry !== "object" || Array.isArray(entry)) {
      return { rows: [], error: "every entry must be an object" };
    }
    rows.push(fieldMapRowFromJSON(entry as Record<string, unknown>));
  }
  return { rows, error: null };
}

function fieldMapRowFromJSON(entry: Record<string, unknown>): FieldMapRow {
  const keepRaw = entry.keep;
  if (Array.isArray(keepRaw) || keepRaw !== undefined) {
    return {
      mode: "keep",
      from: "",
      to: "",
      template: "",
      valueType: "str",
      valueText: "",
      keep: Array.isArray(keepRaw) ? keepRaw.map((key) => String(key)).join(", ") : "",
    };
  }
  const from = typeof entry.from === "string" ? entry.from : "";
  const to = typeof entry.to === "string" ? entry.to : "";
  const template = typeof entry.template === "string" ? entry.template : "";
  const value = entry.value as Record<string, unknown> | undefined;
  if (value && typeof value === "object") {
    if (typeof value.str === "string") {
      return {
        mode: "value",
        from,
        to,
        template: "",
        valueType: "str",
        valueText: value.str,
        keep: "",
      };
    }
    if (typeof value.num === "number") {
      return {
        mode: "value",
        from,
        to,
        template: "",
        valueType: "num",
        valueText: String(value.num),
        keep: "",
      };
    }
    if (typeof value.bool === "boolean") {
      return {
        mode: "value",
        from,
        to,
        template: "",
        valueType: "bool",
        valueText: String(value.bool),
        keep: "",
      };
    }
    return { mode: "value", from, to, template: "", valueType: "null", valueText: "", keep: "" };
  }
  if (template)
    return { mode: "template", from, to, template, valueType: "str", valueText: "", keep: "" };
  return {
    mode: entry.move === true ? "move" : "copy",
    from,
    to,
    template: "",
    valueType: "str",
    valueText: "",
    keep: "",
  };
}

/** serializeFieldMap writes the same shapes the backend validator accepts. */
export function serializeFieldMap(rows: FieldMapRow[]): string {
  const entries = rows
    .map(fieldMapEntryFromRow)
    .filter((entry): entry is Record<string, unknown> => entry !== null);
  if (entries.length === 0) return "";
  return JSON.stringify(entries, null, 2);
}

function fieldMapEntryFromRow(row: FieldMapRow): Record<string, unknown> | null {
  const from = row.from.trim();
  const to = row.to.trim();
  if (row.mode === "keep") {
    const keys = row.keep
      .split(",")
      .map((key) => key.trim())
      .filter(Boolean);
    if (keys.length === 0) return null;
    return { keep: keys };
  }
  if (row.mode === "template") {
    // The mode promises a template: an empty one carries nothing.
    const template = row.template.trim();
    if (!template) return null;
    return to ? { to, template } : { template };
  }
  if (row.mode === "value") {
    const hasValue = row.valueType === "null" || row.valueText.trim() !== "";
    if (!to && !hasValue) return null;
    return to ? { to, value: valueLiteral(row) } : { value: valueLiteral(row) };
  }
  // copy / move: keep whatever the operator typed, drop only a blank row.
  if (!from && !to) return null;
  const entry: Record<string, unknown> = {};
  if (from) entry.from = from;
  if (to) entry.to = to;
  if (row.mode === "move") entry.move = true;
  return entry;
}

function valueLiteral(row: FieldMapRow): Record<string, unknown> {
  switch (row.valueType) {
    case "num": {
      const parsed = Number(row.valueText);
      return { num: Number.isFinite(parsed) ? parsed : 0 };
    }
    case "bool":
      return { bool: row.valueText.trim() === "true" };
    case "null":
      return { null: true };
    default:
      return { str: row.valueText };
  }
}

// ---- validation (mirrors the save-time rules) ----

export type RowIssue = { index: number; message: string };

export function validatePathOverride(value: string): string | null {
  if (/\s/.test(value.trim())) return "must not contain whitespace";
  return null;
}

export function validatePathMapRows(rows: PathMapRow[]): RowIssue[] {
  const issues: RowIssue[] = [];
  const seen = new Set<string>();
  rows.forEach((row, index) => {
    const key = normalizePathKey(row.from);
    const target = row.to.trim();
    if (key === "" && target === "") return; // an untouched blank row is not an error
    if (key === "") {
      issues.push({ index, message: "客户端路径不能为空" });
      return;
    }
    // Record the key before any early return: a later row repeating a key whose
    // first occurrence was rejected is still a duplicate.
    const duplicate = seen.has(key);
    seen.add(key);
    if (target === "") {
      issues.push({ index, message: "上游路径不能为空" });
      return;
    }
    if (/[?#]/.test(target)) {
      issues.push({ index, message: "上游路径不能带 query 或 fragment" });
      return;
    }
    if (duplicate) {
      issues.push({ index, message: "客户端路径重复" });
    }
  });
  return issues;
}

export function validateFieldMapRows(rows: FieldMapRow[]): RowIssue[] {
  const issues: RowIssue[] = [];
  rows.forEach((row, index) => {
    if (row.mode === "keep") {
      const keys = row.keep
        .split(",")
        .map((key) => key.trim())
        .filter(Boolean);
      if (keys.length === 0) {
        issues.push({ index, message: "keep 至少需要一个顶层键" });
        return;
      }
      const nested = keys.find((key) => key.includes(".") || key.includes("["));
      if (nested) {
        issues.push({ index, message: `keep 只接受顶层键（${nested}）` });
      }
      return;
    }
    const from = row.from.trim();
    const to = row.to.trim();
    if (row.mode === "template" && row.template.trim() === "") {
      issues.push({ index, message: "模板不能为空" });
      return;
    }
    if (row.mode !== "template" && !from && row.mode !== "value") {
      issues.push({ index, message: "源路径不能为空（或改用固定值 / 模板 / keep）" });
      return;
    }
    if (row.mode === "value" && !to) {
      issues.push({ index, message: "固定值需要目标路径" });
      return;
    }
    if (from) {
      const fromIssue = validateJSONPath(from);
      if (fromIssue) {
        issues.push({ index, message: `from: ${fromIssue}` });
        return;
      }
    }
    if (to) {
      const toIssue = validateJSONPath(to);
      if (toIssue) {
        issues.push({ index, message: `to: ${toIssue}` });
      }
    }
  });
  return issues;
}

export type EndpointMapSummary = {
  pathCount: number;
  requestCount: number;
  responseCount: number;
  keepCount: number;
  hasOverride: boolean;
  issues: number;
};

/** summarizeEndpoints drives the section header badges. */
export function summarizeEndpoints(value: EndpointMapValue): EndpointMapSummary {
  const pathMap = parsePathMap(value.pathMap);
  const requestMap = parseFieldMap(value.requestMap);
  const responseMap = parseFieldMap(value.responseMap);
  const issues =
    (pathMap.error ? 1 : 0) +
    (requestMap.error ? 1 : 0) +
    (responseMap.error ? 1 : 0) +
    validatePathMapRows(pathMap.rows).length +
    validateFieldMapRows(requestMap.rows).length +
    validateFieldMapRows(responseMap.rows).length +
    (validatePathOverride(value.pathOverride) ? 1 : 0);
  const keepCount = [...requestMap.rows, ...responseMap.rows].filter(
    (row) => row.mode === "keep",
  ).length;
  return {
    pathCount: pathMap.rows.filter((row) => normalizePathKey(row.from) !== "").length,
    requestCount: requestMap.rows.length,
    responseCount: responseMap.rows.length,
    keepCount,
    hasOverride: value.pathOverride.trim() !== "",
    issues,
  };
}

// ---- presets ----

export type EndpointMapPreset = {
  id: string;
  requestMap?: string;
  responseMap?: string;
  pathMap?: string;
};

/**
 * Presets cover the two shapes operators hand-write most often. They only touch
 * the column they are named for so applying one cannot silently rewrite an
 * unrelated mapping.
 */
export const ENDPOINT_MAP_PRESETS: EndpointMapPreset[] = [
  {
    id: "keepTopLevel",
    requestMap: serializeFieldMap([
      {
        mode: "copy",
        from: "messages.0.content",
        to: "state",
        template: "",
        valueType: "str",
        valueText: "",
        keep: "",
      },
      {
        mode: "keep",
        from: "",
        to: "",
        template: "",
        valueType: "str",
        valueText: "",
        keep: "model, state",
      },
    ]),
  },
  {
    id: "wrapMessages",
    requestMap: serializeFieldMap([
      {
        mode: "template",
        from: "",
        to: "questions.0.ask",
        template: "{messages.0.content}",
        valueType: "str",
        valueText: "",
        keep: "",
      },
    ]),
    responseMap: serializeFieldMap([
      {
        mode: "copy",
        from: "answers.0.choice",
        to: "choices.0.message.content",
        template: "",
        valueType: "str",
        valueText: "",
        keep: "",
      },
    ]),
  },
  {
    id: "pathRename",
    pathMap: serializePathMap([
      { from: "chat/completions", to: "v1/systemone" },
      { from: "models", to: "v1/models" },
    ]),
  },
];
