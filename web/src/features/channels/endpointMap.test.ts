import { describe, expect, it } from "vitest";
import {
  parseEndpointMap,
  parseFieldMap,
  parsePathMap,
  serializeFieldMap,
  serializePathMap,
  summarizeEndpoints,
  validateFieldMapRows,
  validateJSONPath,
  validatePathMapRows,
  type EndpointMapValue,
  type FieldMapRow,
} from "./endpointMap";

// The editor stores exactly what the backend validator accepts, so these cases
// are the console-side mirror of internal/proxy/upstream_map_validate.go.

const fieldRow = (partial: Partial<FieldMapRow>): FieldMapRow => ({
  mode: "copy",
  from: "",
  to: "",
  template: "",
  valueType: "str",
  valueText: "",
  keep: "",
  ...partial,
});

describe("validateJSONPath", () => {
  it("accepts the paths the engine resolves", () => {
    for (const path of ["messages.0.content", "choices[0].message", "messages.#.image_url", "a[0]", "model"]) {
      expect(validateJSONPath(path)).toBeNull();
    }
  });

  it("rejects the typos that would silently match nothing", () => {
    expect(validateJSONPath("")).not.toBeNull();
    expect(validateJSONPath("a..b")).not.toBeNull();
    expect(validateJSONPath(".a")).not.toBeNull();
    expect(validateJSONPath("a.")).not.toBeNull();
    expect(validateJSONPath("a.[0]")).not.toBeNull();
    expect(validateJSONPath("a[0")).not.toBeNull();
    expect(validateJSONPath("a]0[")).not.toBeNull();
    expect(validateJSONPath("a b")).not.toBeNull();
    expect(validateJSONPath("a[0[1]]")).not.toBeNull();
  });
});

describe("path map", () => {
  it("serializes sorted, trimmed keys (the stored canonical form)", () => {
    const json = serializePathMap([
      { from: "/models/", to: " v1/models " },
      { from: "/chat/completions", to: "v1/systemone" },
    ]);
    expect(json).toBe(
      JSON.stringify({ "chat/completions": "v1/systemone", models: "v1/models" }, null, 2)
    );
  });

  it("round-trips and keeps a wildcard key", () => {
    const rows = [{ from: "chat/*", to: "upstream/{path}" }];
    const parsed = parsePathMap(serializePathMap(rows));
    expect(parsed.error).toBeNull();
    expect(parsed.rows).toEqual(rows);
  });

  it("treats the empty forms as no mapping", () => {
    expect(parsePathMap("{}").rows).toEqual([]);
    expect(parsePathMap("  ").rows).toEqual([]);
    expect(serializePathMap([])).toBe("");
  });

  it("reports malformed JSON instead of dropping it", () => {
    expect(parsePathMap("{oops").error).toContain("invalid JSON");
    expect(parsePathMap("[]").error).toContain("object");
  });

  it("flags empty, duplicated and query-carrying rows", () => {
    const issues = validatePathMapRows([
      { from: "", to: "v1/x" },
      { from: "models", to: "" },
      { from: "models", to: "v1/models" },
      { from: "models", to: "v1/models?q=1" },
      { from: "chat/completions", to: "v1/systemone" },
    ]);
    expect(issues.map((issue) => issue.index)).toEqual([0, 1, 2, 3]);
  });

  it("ignores a blank row the operator has not filled in yet", () => {
    expect(validatePathMapRows([{ from: "", to: "" }])).toEqual([]);
  });
});

describe("field maps", () => {
  it("reads every supported shape", () => {
    const raw = JSON.stringify([
      { from: "messages.0.content", to: "state" },
      { from: "a", to: "b", move: true },
      { to: "q", template: "{messages.0.content}" },
      { to: "n", value: { num: 8 } },
      { keep: ["model", "state"] },
    ]);
    const parsed = parseFieldMap(raw);
    expect(parsed.error).toBeNull();
		expect(parsed.rows.map((row) => row.mode)).toEqual(["copy", "move", "template", "value", "keep"]);
		expect(parsed.rows.map((row) => row.valueType)[3]).toBe("num");
		expect(parsed.rows.map((row) => row.keep)[4]).toBe("model, state");
  });

  it("remembers the literal type of a value node", () => {
    const parsed = parseFieldMap(
      JSON.stringify([{ to: "a", value: { str: "x" } }, { to: "b", value: { bool: false } }, { to: "c", value: { null: true } }])
    );
    expect(parsed.rows.map((row) => row.valueType)).toEqual(["str", "bool", "null"]);
  });

  it("re-serializes to the shapes the backend validator accepts", () => {
    const rows = [
      fieldRow({ mode: "copy", from: "messages.0.content", to: "state" }),
      fieldRow({ mode: "move", from: "a", to: "b" }),
      fieldRow({ mode: "template", to: "q", template: "{messages.0.content}" }),
      fieldRow({ mode: "value", to: "n", valueType: "num", valueText: "8" }),
      fieldRow({ mode: "keep", keep: "model,state" }),
    ];
    const json = serializeFieldMap(rows);
    expect(JSON.parse(json)).toEqual([
      { from: "messages.0.content", to: "state" },
      { from: "a", to: "b", move: true },
      { to: "q", template: "{messages.0.content}" },
      { to: "n", value: { num: 8 } },
      { keep: ["model", "state"] },
    ]);
    expect(parseFieldMap(json).rows.map((row) => row.mode)).toEqual(rows.map((row) => row.mode));
  });

  it("drops only rows that carry nothing at all", () => {
    expect(serializeFieldMap([fieldRow({})])).toBe("");
    expect(serializeFieldMap([fieldRow({ mode: "keep", keep: " , " })])).toBe("");
    expect(serializeFieldMap([fieldRow({ mode: "copy", from: "a" })])).toBe(
      JSON.stringify([{ from: "a" }], null, 2)
    );
  });

  it("flags the mutually-exclusive and empty-row mistakes", () => {
    const issues = validateFieldMapRows([
      fieldRow({ mode: "keep", keep: "messages.0.content" }),
      fieldRow({ mode: "keep", keep: "" }),
      fieldRow({ mode: "template", to: "q", template: "" }),
      fieldRow({ mode: "copy", from: "" }),
      fieldRow({ mode: "value", to: "", valueType: "str", valueText: "x" }),
      fieldRow({ mode: "copy", from: "a..b", to: "c" }),
      fieldRow({ mode: "copy", from: "a", to: "c." }),
    ]);
    expect(issues.map((issue) => issue.index)).toEqual([0, 1, 2, 3, 4, 5, 6]);
  });

  it("accepts a well-formed chain", () => {
    expect(
      validateFieldMapRows([
        fieldRow({ mode: "copy", from: "messages.0.content", to: "state" }),
        fieldRow({ mode: "keep", keep: "model, state" }),
      ])
    ).toEqual([]);
  });
});

describe("summarizeEndpoints", () => {
  const value: EndpointMapValue = {
    pathOverride: "systemone",
    pathMap: serializePathMap([{ from: "models", to: "v1/models" }]),
    requestMap: serializeFieldMap([
      fieldRow({ mode: "copy", from: "messages.0.content", to: "state" }),
      fieldRow({ mode: "keep", keep: "model" }),
    ]),
    responseMap: "",
  };

  it("counts what the section header shows", () => {
    const summary = summarizeEndpoints(value);
    expect(summary).toMatchObject({
      pathCount: 1,
      requestCount: 2,
      responseCount: 0,
      keepCount: 1,
      hasOverride: true,
      issues: 0,
    });
  });

  it("counts issues from every column, including the override", () => {
    expect(
      summarizeEndpoints({ ...value, pathOverride: "bad path", pathMap: "{oops" }).issues
    ).toBe(2);
  });

  it("treats an empty mapping as no mapping", () => {
    const summary = summarizeEndpoints({ pathOverride: "", pathMap: "", requestMap: "", responseMap: "" });
    expect(summary).toMatchObject({ pathCount: 0, requestCount: 0, responseCount: 0, issues: 0 });
    expect(parseEndpointMap({ pathOverride: "", pathMap: "", requestMap: "", responseMap: "" }).pathMapError).toBeNull();
  });
});
