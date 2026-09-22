/**
 * One-click endpoint presets for upstreams that are not OpenAI-shaped.
 *
 * A preset exists because these fields are a protocol contract, not a
 * preference: getting a field map wrong fails silently at request time (an
 * absent source is skipped by design), which is exactly the "configured but
 * nothing happened" trap the endpoint section is meant to remove.
 *
 * A preset only fills what it can know. The request map must reference the
 * operator's OWN question semantics, so the shipped TypeSafe preset carries one
 * generic yes/no question as a working starting point — the UI says so, and the
 * operator edits the question text and the answer field to match their setup.
 *
 * Presets are additive: they never overwrite a field the operator has filled,
 * so clicking one cannot lose hand-tuned work.
 */
export type EndpointPreset = {
  id: string;
  label: string;
  /** Upstream root; the operator still supplies the API key. */
  baseUrl?: string;
  /** Endpoint path override, e.g. "systemone" → /v1/systemone. */
  override?: string;
  requestMap?: string;
  responseMap?: string;
};

/**
 * TypeSafe System One (`POST /v1/systemone`): `{state, model, questions}` in,
 * `{model, answers, usage}` out. A field map's `value` is a JSON scalar
 * (str/num/bool/null), so a typed question is built one leaf at a time.
 */
const TYPESAFE_REQUEST_MAP = JSON.stringify(
  [
    { from: "messages.0.content", to: "state" },
    { to: "questions.answer.type", value: { str: "noul" } },
    {
      to: "questions.answer.instructions",
      value: { str: "Respond to the state." },
    },
  ],
  null,
  2,
);

const TYPESAFE_RESPONSE_MAP = JSON.stringify(
  [
    { to: "object", value: { str: "chat.completion" } },
    { to: "choices.0.message.role", value: { str: "assistant" } },
    // The noul answer is a number; a template renders it as text, which is what
    // OpenAI clients expect in `content`. Point it at your own question id and
    // answer field (noul / choice / score / legend).
    { to: "choices.0.message.content", template: "{answers.answer.noul}" },
    { to: "choices.0.finish_reason", value: { str: "stop" } },
    // Usage maps so the gateway meters real upstream tokens instead of
    // estimating from bytes.
    { from: "usage.input_tokens", to: "usage.prompt_tokens" },
    { from: "usage.output_tokens", to: "usage.completion_tokens" },
  ],
  null,
  2,
);

export const ENDPOINT_PRESETS: EndpointPreset[] = [
  {
    id: "typesafe-systemone",
    label: "TypeSafe System One",
    baseUrl: "https://api.typesafe.ai",
    override: "systemone",
    requestMap: TYPESAFE_REQUEST_MAP,
    responseMap: TYPESAFE_RESPONSE_MAP,
  },
];

/** Applies a preset to a draft, leaving every non-empty field alone. */
export function applyEndpointPreset(
  preset: EndpointPreset,
  current: {
    baseUrl?: string;
    override: string;
    requestMap: string;
    responseMap: string;
  },
) {
  const keep = (value: string | undefined) => Boolean(value && value.trim());
  return {
    baseUrl: keep(current.baseUrl) ? current.baseUrl : preset.baseUrl,
    override: keep(current.override) ? current.override : (preset.override ?? current.override),
    requestMap: keep(current.requestMap) ? current.requestMap : (preset.requestMap ?? current.requestMap),
    responseMap: keep(current.responseMap) ? current.responseMap : (preset.responseMap ?? current.responseMap),
  };
}
