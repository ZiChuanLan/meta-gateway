-- Persist last discovery/probe outcome on channels so Admin health badges
-- reflect failed tests even when historical models remain in the inventory.
ALTER TABLE channels ADD COLUMN last_probe_at TEXT;
ALTER TABLE channels ADD COLUMN last_probe_ok INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN last_probe_error TEXT NOT NULL DEFAULT '';
