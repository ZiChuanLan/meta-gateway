-- Persist the global outbound proxy across restarts (the runtime settings
-- row previously gained the Go struct field without a backing column, so a
-- hot-applied proxy vanished on restart).
ALTER TABLE runtime_settings ADD COLUMN proxy_url TEXT NOT NULL DEFAULT '';
