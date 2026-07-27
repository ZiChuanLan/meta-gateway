-- Indexes for Admin proxy-log list filters (channel_id, model, site via channel).
CREATE INDEX IF NOT EXISTS idx_proxy_logs_channel_id ON proxy_logs(channel_id);
CREATE INDEX IF NOT EXISTS idx_proxy_logs_model ON proxy_logs(model);
CREATE INDEX IF NOT EXISTS idx_proxy_logs_id ON proxy_logs(id);
