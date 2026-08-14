-- Error passthrough rules: match upstream error responses (status code +
-- error-body keyword) and override the default 4xx failover behavior.
-- action: passthrough = return the upstream error to the client without
-- failover or cooldown; rewrite = passthrough with a rewritten status code;
-- ignore_monitor = keep failover but skip breaker/cooldown/failure counters.
-- Rules are read live on every request (no cache) so edits apply instantly.
CREATE TABLE IF NOT EXISTS error_passthrough_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  status_code INTEGER NOT NULL DEFAULT 0,
  keyword TEXT NOT NULL DEFAULT '',
  model_glob TEXT NOT NULL DEFAULT '',
  channel_id INTEGER NOT NULL DEFAULT 0,
  action TEXT NOT NULL DEFAULT 'passthrough',
  rewrite_to INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_error_rules_enabled ON error_passthrough_rules (enabled);
