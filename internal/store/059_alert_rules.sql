-- Alert rules: metric/operator/threshold/window/sustained → webhook alert.
-- Evaluated on a fixed tick (60s); a rule fires when the metric crosses the
-- threshold for sustained consecutive ticks, then enters a cooldown so the
-- same condition does not re-notify every tick.
CREATE TABLE IF NOT EXISTS alert_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  metric TEXT NOT NULL,
  operator TEXT NOT NULL,
  threshold REAL NOT NULL,
  window_seconds INTEGER NOT NULL DEFAULT 3600,
  sustained_seconds INTEGER NOT NULL DEFAULT 300,
  cooldown_seconds INTEGER NOT NULL DEFAULT 900,
  level TEXT NOT NULL DEFAULT 'warning',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
