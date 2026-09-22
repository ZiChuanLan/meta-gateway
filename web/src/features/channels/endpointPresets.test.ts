import { describe, expect, it } from "vitest";
import {
  ENDPOINT_PRESETS,
  applyEndpointPreset,
} from "./endpointPresets";

/** The TypeSafe preset is a protocol contract: its maps must decode and must
 *  describe the upstream's real request/response shape, or an operator who
 *  clicks it gets a silent no-op at request time. */
describe("endpoint presets", () => {
  const preset = ENDPOINT_PRESETS.find((p) => p.id === "typesafe-systemone")!;

  it("ships a decodable request map that builds a typed question", () => {
    const entries = JSON.parse(preset.requestMap!);
    expect(entries).toContainEqual({ from: "messages.0.content", to: "state" });
    expect(
      entries.some((e: Record<string, unknown>) => e.to === "questions.answer.type"),
    ).toBe(true);
  });

  it("ships a decodable response map that returns an OpenAI completion", () => {
    const entries = JSON.parse(preset.responseMap!);
    const content = entries.find(
      (e: Record<string, unknown>) => e.to === "choices.0.message.content",
    );
    expect(content?.template).toContain("answers.");
    expect(entries).toContainEqual({ to: "object", value: { str: "chat.completion" } });
  });

  it("fills only empty fields", () => {
    const applied = applyEndpointPreset(preset, {
      baseUrl: "",
      override: "",
      requestMap: "",
      responseMap: "",
    });
    expect(applied.baseUrl).toBe("https://api.typesafe.ai");
    expect(applied.override).toBe("systemone");

    const kept = applyEndpointPreset(preset, {
      baseUrl: "https://my-mirror.example",
      override: "my-endpoint",
      requestMap: '[{"from":"a","to":"b"}]',
      responseMap: '[{"from":"c","to":"d"}]',
    });
    expect(kept.baseUrl).toBe("https://my-mirror.example");
    expect(kept.override).toBe("my-endpoint");
    expect(kept.requestMap).toBe('[{"from":"a","to":"b"}]');
    expect(kept.responseMap).toBe('[{"from":"c","to":"d"}]');
  });
});
