import { describe, expect, it } from "vitest";
import { autoModelGroup, modelGroup, modelPatternMatches } from "./modelGroups";

describe("autoModelGroup", () => {
  it("classifies well-known families", () => {
    expect(autoModelGroup("deepseek-v4-flash")).toBe("DeepSeek");
    expect(autoModelGroup("deepseek-ai/deepseek-v4-pro")).toBe("DeepSeek");
    expect(autoModelGroup("gpt-4o")).toBe("GPT");
    expect(autoModelGroup("openai/gpt-oss-120b")).toBe("GPT");
    expect(autoModelGroup("o3-mini")).toBe("GPT");
    expect(autoModelGroup("grok-4.5")).toBe("Grok");
    expect(autoModelGroup("claude-opus-4")).toBe("Claude");
    expect(autoModelGroup("gemini-2.5-flash")).toBe("Gemini");
    expect(autoModelGroup("qwen3-next-80b-a3b-instruct")).toBe("Qwen");
    expect(autoModelGroup("llama-3.1-70b")).toBe("Llama");
    expect(autoModelGroup("meta-llama/Llama-3")).toBe("Llama");
    expect(autoModelGroup("mistral-large")).toBe("Mistral");
    expect(autoModelGroup("yi-34b-chat")).toBe("Yi");
  });

  it("does not misclassify substrings", () => {
    // "xai" inside "minimaxai" must not read as Grok.
    expect(autoModelGroup("minimaxai/minimax-m2.7")).toBe("MiniMax");
    expect(autoModelGroup("minimaxai/minimax-m3")).toBe("MiniMax");
    // "google/" prefix must not swallow Gemma into Gemini.
    expect(autoModelGroup("google/gemma-4-31b-it")).toBe("Gemma");
    // "phi" inside unrelated words must not match.
    expect(autoModelGroup("graphics-model")).toBe("Other");
  });

  it("classifies previously-uncovered families", () => {
    expect(autoModelGroup("moonshotai/kimi-k2.6")).toBe("Kimi");
    expect(autoModelGroup("glm-5.2")).toBe("GLM");
    expect(autoModelGroup("z-ai/glm-5.2")).toBe("GLM");
    expect(autoModelGroup("zai-glm-5-2")).toBe("GLM");
    expect(autoModelGroup("abab6.5s-chat")).toBe("MiniMax");
    expect(autoModelGroup("doubao-pro-32k")).toBe("Doubao");
    expect(autoModelGroup("hunyuan-large")).toBe("Hunyuan");
    expect(autoModelGroup("baichuan4")).toBe("Baichuan");
    expect(autoModelGroup("step-2-16k")).toBe("Step");
    expect(autoModelGroup("command-r-plus")).toBe("Command");
    expect(autoModelGroup("ernie-4.0")).toBe("ERNIE");
    expect(autoModelGroup("internlm2-chat")).toBe("InternLM");
    expect(autoModelGroup("phi-4-mini")).toBe("Phi");
  });

  it("prefers the manual group when provided", () => {
    expect(modelGroup("some-model", " Custom ", "vendor")).toBe("Custom");
    expect(modelGroup("minimaxai/minimax-m3", " ", undefined)).toBe("MiniMax");
  });

  it("falls back to Other for unknown models", () => {
    expect(autoModelGroup("mystery-model-9000")).toBe("Other");
  });
});

describe("modelPatternMatches", () => {
  it("matches wildcards and question marks", () => {
    expect(modelPatternMatches("gpt-*", "gpt-4o")).toBe(true);
    expect(modelPatternMatches("gpt-?", "gpt-4")).toBe(true);
    expect(modelPatternMatches("gpt-*", "claude-3")).toBe(false);
    expect(modelPatternMatches("*", "anything")).toBe(true);
  });
});
