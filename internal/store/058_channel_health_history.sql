-- Channel health probe history: every health-sweep/manual probe result is
-- appended here (channel_id, ok flag, latency, verdict) for availability
-- curves and per-channel reliability stats. Daily aggregate availability is
-- computed on the fly from this table; rows are pruned by retention (default
-- 90 days, runtime-configurable).
CREATE TABLE IF NOT EXISTS channel_health_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id INTEGER NOT NULL,
  ok INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  verdict TEXT NOT NULL DEFAULT '',
  probed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_health_history_channel ON channel_health_history (channel_id, probed_at);
CREATE INDEX IF NOT EXISTS idx_health_history_at ON channel_health_history (probed_at);
