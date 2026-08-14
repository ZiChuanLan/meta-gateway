-- Routing decision snapshots: one row per selection decision, capturing the
-- candidate list, scores, eligibility reasons, sticky/stable-first state and
-- the chosen channel, so "why did this request go to channel X" can be
-- answered after the fact. Payload is the routing.Explanation JSON.
CREATE TABLE IF NOT EXISTS decision_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  route_id INTEGER NOT NULL DEFAULT 0,
  selected_channel_id INTEGER NOT NULL DEFAULT 0,
  payload TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_decision_snapshots_request ON decision_snapshots (request_id, id);
CREATE INDEX IF NOT EXISTS idx_decision_snapshots_created ON decision_snapshots (created_at);
