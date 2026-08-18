const GROUP_RULES: Array<[string, RegExp]> = [
  ["DeepSeek", /deepseek/i],
  ["GPT", /(^|[-_/])gpt(?:[-_/]|$)|openai/i],
  ["Grok", /grok|xai/i],
  ["Claude", /claude|anthropic/i],
  ["Gemini", /gemini|google/i],
  ["Qwen", /qwen|通义|千问/i],
  ["Llama", /llama|meta/i],
  ["Mistral", /mistral/i],
  ["Yi", /(^|[-_/])yi(?:[-_/]|$)|零一万物/i],
];

export function autoModelGroup(model: string, vendor?: string): string {
  const source = `${vendor ?? ""} ${model}`;
  return (
    GROUP_RULES.find(([, pattern]) => pattern.test(source))?.[0] ?? "Other"
  );
}

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
