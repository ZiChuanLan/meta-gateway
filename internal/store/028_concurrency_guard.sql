-- In-flight burst guard for routing: per-channel concurrency ceiling that
-- nearly skips saturated channels so a sudden spike spreads across the fleet.
-- NULL = not overridden yet (falls back to env bootstrap).
ALTER TABLE runtime_settings ADD COLUMN routing_concurrency_enabled INTEGER;
ALTER TABLE runtime_settings ADD COLUMN routing_concurrency_limit INTEGER;
