-- Announcements: one line of the deployment's own voice, written by the operator
-- and shown to the people using this gateway.
--
-- Kept deliberately small: a title, an optional body, a tone (info/warn), pin and
-- enable flags. The operator writes one when something changes (a channel is down
-- for maintenance, a price changed, a new model arrived); members read the newest
-- enabled one at the top of their pages.
CREATE TABLE IF NOT EXISTS announcements (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  tone TEXT NOT NULL DEFAULT 'info',
  pinned INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_announcements_order
  ON announcements(enabled, pinned DESC, id DESC);
