-- Reasoning effort of the client request (OpenAI-style reasoning_effort,
-- e.g. low / medium / high / max / xhigh) when present in the request body.
ALTER TABLE proxy_logs ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT '';
