-- Model-not-found blacklist: a channel × model combination that the upstream
-- reported as unknown (model_not_found / no such model / unknown model).
-- Routing skips these combinations instead of burning a failover attempt on
-- every request. Admins can clear entries manually.
CREATE TABLE IF NOT EXISTS channel_model_blocks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id INTEGER NOT NULL,
  model TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE (channel_id, model)
);
CREATE INDEX IF NOT EXISTS idx_channel_model_blocks_channel ON channel_model_blocks (channel_id);
CREATE INDEX IF NOT EXISTS idx_channel_model_blocks_model ON channel_model_blocks (model);
