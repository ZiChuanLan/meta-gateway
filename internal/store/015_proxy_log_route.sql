ALTER TABLE proxy_logs ADD COLUMN route_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_proxy_logs_route_id ON proxy_logs (route_id);
