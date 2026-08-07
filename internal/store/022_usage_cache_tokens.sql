-- Cache-token accounting for usage metering.
-- Records upstream prompt-cache detail: Anthropic cache_read/cache_creation
-- input tokens, OpenAI prompt_tokens_details.cached_tokens, and Gemini
-- usageMetadata.cachedContentTokenCount. Existing rows default to 0.
ALTER TABLE proxy_logs ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_logs ADD COLUMN cache_creation_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN cache_creation_tokens INTEGER NOT NULL DEFAULT 0;
