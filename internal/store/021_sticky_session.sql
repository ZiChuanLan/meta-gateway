-- Sticky session routing: record the session key that produced each relay
-- attempt so operators (and the e2e runner) can verify affinity behavior.
ALTER TABLE proxy_logs ADD COLUMN session_key TEXT NOT NULL DEFAULT '';
