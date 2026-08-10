-- Connectivity ping results (network-layer reachability, separate from
-- model/auth probing).
ALTER TABLE channels ADD COLUMN last_ping_at TEXT;
ALTER TABLE channels ADD COLUMN last_ping_ok INTEGER;
ALTER TABLE channels ADD COLUMN last_ping_error TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN last_ping_ms INTEGER NOT NULL DEFAULT 0;
