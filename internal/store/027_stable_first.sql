-- Stable-first grayscale pool: channels marked stable_first receive a small
-- 1/N fraction of traffic (N = runtime setting) until they earn promotion
-- (stable_first_requests >= promote threshold with no consecutive failures).
ALTER TABLE channels ADD COLUMN stable_first INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN stable_first_requests INTEGER NOT NULL DEFAULT 0;

-- Runtime overrides for the grayscale pool.
ALTER TABLE runtime_settings ADD COLUMN stable_first_enabled INTEGER;
ALTER TABLE runtime_settings ADD COLUMN stable_first_denominator INTEGER;
ALTER TABLE runtime_settings ADD COLUMN stable_first_promote_requests INTEGER;
