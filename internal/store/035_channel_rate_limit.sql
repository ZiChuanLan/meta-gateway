-- Rate-limit pause: a 429 probe verdict parks the channel until this
-- timestamp (RFC3339Nano); routing excludes parked channels. NULL = not
-- rate limited.
ALTER TABLE channels ADD COLUMN rate_limited_until TEXT;
