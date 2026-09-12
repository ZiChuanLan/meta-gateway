-- Per-channel cap for non-streaming upstream attempts (request + full body
-- read), in seconds. 0 keeps the global default (5 minutes). Streaming
-- requests are exempt; slow deep-reasoning upstreams raise this.
ALTER TABLE channels ADD COLUMN non_stream_timeout_seconds INTEGER NOT NULL DEFAULT 0;
