-- Decision snapshots are written once per failover attempt; the attempt
-- number ties each snapshot to its proxy_logs row so the log UI can show the
-- decision that produced THAT attempt instead of only the last one.
ALTER TABLE decision_snapshots ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
