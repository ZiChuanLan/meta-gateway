const GROUP_RULES: Array<[string, RegExp]> = [
  ["DeepSeek", /deepseek/i],
  ["GPT", /(^|[-_/])gpt(?:[-_/]|$)|openai|chatgpt|(^|[-_/])o[134](?:[-_/.]|$)/i],
  ["Grok", /grok|(^|[-_/])xai([-_/]|$)/i],
  ["Claude", /claude|anthropic/i],
  ["Gemma", /gemma/i],
  ["Gemini", /gemini|(^|[-_/])google([-_/]|$)|palm/i],
  ["Qwen", /qwen|tongyi|通义|千问/i],
  ["Kimi", /kimi|moonshot/i],
  ["GLM", /glm|zhipu|chatglm|(^|[-_/])zai([-_/]|$)/i],
  ["MiniMax", /minimax|(^|[-_/])abab/i],
  ["Llama", /llama|(^|[-_/])meta([-_/]|$)/i],
  ["Mistral", /mistral|mixtral|codestral/i],
  ["Phi", /(^|[-_/])phi(?:[-_/]|$)/i],
  ["Doubao", /doubao|豆包/i],
  // Tencent Hunyuan, including the short "hy<generation>"/"hy-" alias.
  // Enumerated rather than written as a bare "hy", the same way the backend's
  // provider rule does it, so hyperclova/hyperbolic cannot be swallowed.
  ["Hunyuan", /hunyuan|混元|(^|[-_/])hy(?:-|[34](?:[-_/]|$))/i],
  ["Baichuan", /baichuan|百川/i],
  ["Step", /stepfun|阶跃|(^|[-_/])step[-_/]?\d/i],
  ["Command", /(^|[-_/])command[-_/]|cohere/i],
  ["ERNIE", /ernie|文心|qianfan/i],
  ["InternLM", /internlm/i],
  ["Yi", /(^|[-_/])yi(?:[-_/]|$)|零一万物/i],
  // Appended rather than slotted in beside their neighbours: GROUP_RULES is
  // first-match-wins and MODEL_GROUP_ORDER is derived from its order, so
  // appending cannot reorder any group a user has already learnt.
  //
  // Xiaomi's MiMo. The vendor half of the haystack is why "xiaomi/mimo-v2.5"
  // groups correctly even though the model half is bare.
  ["MiMo", /xiaomi|(^|[-_/])mimo(?:[-_/]|$)/i],
  ["NVIDIA", /nvidia|nemotron/i],
  ["InclusionAI", /inclusionai|(^|[-_/])ling-/i],
  ["LongCat", /meituan|longcat/i],
];

export function autoModelGroup(model: string, vendor?: string): string {
  const source = `${vendor ?? ""}/${model.trim()}`;
  return (
    GROUP_RULES.find(([, pattern]) => pattern.test(source))?.[0] ?? "Other"
  );
}

/** Stable display order for auto groups; groups outside the list go last. */
export const MODEL_GROUP_ORDER: string[] = [
  ...GROUP_RULES.map(([name]) => name),
  "Other",
];

export function modelGroup(
  model: string,
  manual?: string,
  vendor?: string,
): string {
  return manual?.trim() || autoModelGroup(model, vendor);
}

/** Match the same `*` / `?` model patterns used by route patterns. */
export function modelPatternMatches(pattern: string, model: string): boolean {
  const source = pattern.trim();
  const value = model.trim();
  let patternIndex = 0;
  let valueIndex = 0;
  let starIndex = -1;
  let starValueIndex = -1;

  while (valueIndex < value.length) {
    if (
      patternIndex < source.length &&
      (source[patternIndex] === "?" ||
        source[patternIndex] === value[valueIndex])
    ) {
      patternIndex += 1;
      valueIndex += 1;
    } else if (patternIndex < source.length && source[patternIndex] === "*") {
      starIndex = patternIndex;
      starValueIndex = valueIndex;
      patternIndex += 1;
    } else if (starIndex >= 0) {
      patternIndex = starIndex + 1;
      starValueIndex += 1;
      valueIndex = starValueIndex;
    } else {
      return false;
    }
  }

  while (patternIndex < source.length && source[patternIndex] === "*") {
    patternIndex += 1;
  }
  return patternIndex === source.length;
}
