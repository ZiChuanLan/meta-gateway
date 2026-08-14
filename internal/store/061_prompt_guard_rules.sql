-- Sensitive prompt guard rules: a regex pattern applied to every string
-- value in the request body. action:
--   mask   → matched text is replaced with `replacement` (default [REDACTED])
--   reject → the request is refused with 400 (content policy)
--   exclude → matched requests skip the channels in `exclude_channels`
--            (comma-separated ids) and fail over to the remaining ones
-- channel_scope: 0 = all channels, otherwise the rule only applies when the
-- selected candidate is this channel.
CREATE TABLE IF NOT EXISTS prompt_guard_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  pattern TEXT NOT NULL,
  action TEXT NOT NULL DEFAULT 'mask',
  replacement TEXT NOT NULL DEFAULT '[REDACTED]',
  exclude_channels TEXT NOT NULL DEFAULT '',
  channel_scope INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
