import { expect, it } from "vitest";
import { mappingRealName, memberRealName, serializeMemberMapping } from "./alias";
import { originModelOf } from "../features/models/routingPolicy";
import type { RouteMember } from "../api/types";

it("normalizes valid aliases consistently across readers", () => {
  const mapping_json = '{"real":"  upstream-model  "}';
  expect(mappingRealName(mapping_json)).toBe("upstream-model");
  expect(memberRealName({ mapping_json })).toBe("upstream-model");
  expect(originModelOf({ mapping_json } as RouteMember)).toBe("upstream-model");
  expect(serializeMemberMapping(" upstream-model ")).toBe('{"real":"upstream-model"}');
});

it.each([undefined, "", "{", "null", "[]", '{"real":7}', '{"real":{}}', '{"real":" "}'])(
  "does not invent an alias from %s", (raw) => {
    expect(mappingRealName(raw)).toBe("");
    expect(memberRealName({ mapping_json: raw })).toBe("");
  },
);

it("keeps the legacy route fallback only when no member mapping is configured", () => {
  const route = { mapping_json: '{"real":"legacy"}' };
  expect(originModelOf({ mapping_json: "" } as RouteMember, route)).toBe("legacy");
  expect(originModelOf({ mapping_json: '{"real":"member"}' } as RouteMember, route)).toBe("member");
  expect(originModelOf({ mapping_json: '{"real":123}' } as RouteMember, route)).toBe("");
});
