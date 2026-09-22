-- Per-channel custom endpoint / field mapping. Three columns that together let
-- an operator reach an upstream the OpenAI-shaped forward adapters cannot:
--
--   upstream_path_override  replace the endpoint path outright
--                           ("chat/completions" -> "systemone"). This is what a
--                           provider serving a non-OpenAI protocol needs.
--   upstream_path_map       JSON object {openAIPath: upstreamPath}; a key may
--                           end in "*" to match a prefix, values may use
--                           "{path}" / "{model}". This is what a provider whose
--                           API root is NOT /v1 needs (the passthrough adapter
--                           otherwise inserts a /v1 segment: base
--                           https://open.bigmodel.cn/api/paas/v4 became the
--                           non-existent /api/paas/v4/v1/models). Example:
--                           {"models":"models","chat/completions":"chat/completions"}
--   upstream_request_map    JSON array of field maps applied to the outbound
--   upstream_response_map   body, and to the inbound body respectively. Field
--                           maps reuse the payload_rules path language
--                           ("messages.0.content", "answers.#.choice"):
--                             {"from":"messages","to":"state"}
--                             {"from":"x","to":"y","move":true}
--                             {"to":"questions.q","value":{"str":"…"}}
--                             {"to":"state","template":"…{messages.0.content}…"}
--
-- All three fail open: an empty or malformed value forwards the request
-- untouched, so this feature can never be the reason a relay breaks.
--
-- No backfill: '' / '{}' / '[]' already mean "no mapping", so an upgrade is
-- behaviour-preserving for every existing channel.
ALTER TABLE channels ADD COLUMN upstream_path_override TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN upstream_path_map TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN upstream_request_map TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN upstream_response_map TEXT NOT NULL DEFAULT '';
