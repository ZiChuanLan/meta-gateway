import { describe, expect, it } from "vitest";
import { PROVIDER_BASE_URLS } from "./connectionTypes";

/**
 * The provider presets are the actual product here: picking "智谱 GLM" must land
 * on the endpoint the vendor documents, or the operator is forced back into
 * hand-writing endpoint overrides. These assertions lock the documented values so
 * an edit cannot silently move a provider's root — the exact failure that made
 * every cn-provider preset unusable before (base `/api/paas/v4` + an injected
 * `/v1` produced `/api/paas/v4/v1/chat/completions`).
 */
describe("provider base URL presets", () => {
  const documented: Record<string, string> = {
    // Verified against each vendor's own OpenAI-compatibility guide.
    zhipu: "https://open.bigmodel.cn/api/paas/v4",
    doubao: "https://ark.cn-beijing.volces.com/api/v3",
    qianfan: "https://qianfan.baidubce.com/v2",
    deepseek: "https://api.deepseek.com/v1",
    moonshot: "https://api.moonshot.cn/v1",
    qwen: "https://dashscope.aliyuncs.com/compatible-mode/v1",
    siliconflow: "https://api.siliconflow.cn/v1",
    minimax: "https://api.minimaxi.com/v1",
    stepfun: "https://api.stepfun.com/v1",
    lingyiwanwu: "https://api.lingyiwanwu.com/v1",
    spark: "https://spark-api-open.xf-yun.com/v1",
    hunyuan: "https://api.hunyuan.cloud.tencent.com/v1",
    openrouter: "https://openrouter.ai/api/v1",
    groq: "https://api.groq.com/openai/v1",
    xai: "https://api.x.ai/v1",
    mistral: "https://api.mistral.ai/v1",
    anthropic: "https://api.anthropic.com",
    gemini: "https://generativelanguage.googleapis.com/v1beta",
  };

  it("keeps every documented provider root", () => {
    for (const [provider, base] of Object.entries(documented)) {
      expect(PROVIDER_BASE_URLS[provider], provider).toBe(base);
    }
  });

  it("ships each preset as an absolute URL with no trailing slash", () => {
    for (const [provider, base] of Object.entries(PROVIDER_BASE_URLS)) {
      if (!base) continue; // Relays (new-api/one-api/…) legitimately ship empty.
      expect(base, provider).toMatch(/^https?:\/\/[^\s]+$/);
      expect(base.endsWith("/"), provider).toBe(false);
    }
  });

  it("gives Perplexity its documented endpoint, because its base carries no /v1", () => {
    // Measured against the live API: /chat/completions answers 401 (exists) and
    // /v1/chat/completions answers 404. Shipping the bare host would therefore
    // make the gateway call a path that does not exist.
    expect(PROVIDER_BASE_URLS.perplexity).toBe(
      "https://api.perplexity.ai/chat/completions",
    );
  });
});
