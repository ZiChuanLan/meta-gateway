-- Request-ID lookup index: token backfill and audit filters resolve the
-- newest proxy_log row per request; without an index every UPDATE rescans
-- the table.
CREATE INDEX IF NOT EXISTS idx_proxy_logs_request_id ON proxy_logs(request_id);
