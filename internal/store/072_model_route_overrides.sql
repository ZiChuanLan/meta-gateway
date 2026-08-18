-- 072: per-model relay overrides. NULL means inherit the selected channel
-- default; non-NULL values, including empty/zero/false, are explicit overrides.
ALTER TABLE routes ADD COLUMN max_reasoning_effort TEXT;
ALTER TABLE routes ADD COLUMN max_concurrent INTEGER;
ALTER TABLE routes ADD COLUMN proxy_url TEXT;
ALTER TABLE routes ADD COLUMN header_override TEXT;
ALTER TABLE routes ADD COLUMN system_prompt TEXT;
ALTER TABLE routes ADD COLUMN retry_config TEXT;
ALTER TABLE routes ADD COLUMN payload_rules TEXT;
ALTER TABLE routes ADD COLUMN stable_first INTEGER;
ALTER TABLE routes ADD COLUMN stable_first_denominator INTEGER;
ALTER TABLE routes ADD COLUMN stable_first_promote_requests INTEGER;
ALTER TABLE routes ADD COLUMN stable_first_requests INTEGER NOT NULL DEFAULT 0;
ALTER TABLE routes ADD COLUMN model_group TEXT NOT NULL DEFAULT '';
