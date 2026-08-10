ALTER TABLE proxy_logs ADD COLUMN upstream_request_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_proxy_logs_upstream_request_id ON proxy_logs (upstream_request_id);
