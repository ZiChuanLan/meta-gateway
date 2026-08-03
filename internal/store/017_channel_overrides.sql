-- Per-channel request overrides.
-- header_override: JSON object of extra/override upstream request headers.
-- system_prompt: optional system message injected ahead of user messages.
ALTER TABLE channels ADD COLUMN header_override TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN system_prompt TEXT NOT NULL DEFAULT '';
