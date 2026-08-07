-- Audit observability fields for proxy logs: first-byte latency (streaming)
-- and the coarse client family derived from the User-Agent.
ALTER TABLE proxy_logs ADD COLUMN first_byte_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_logs ADD COLUMN client_family TEXT NOT NULL DEFAULT '';
