CREATE TABLE IF NOT EXISTS balance_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id INTEGER NOT NULL,
  channel_name TEXT NOT NULL DEFAULT '',
  balance INTEGER NOT NULL DEFAULT 0,
  probed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_balance_history_channel ON balance_history (channel_id, probed_at);
CREATE INDEX IF NOT EXISTS idx_balance_history_probed ON balance_history (probed_at);
