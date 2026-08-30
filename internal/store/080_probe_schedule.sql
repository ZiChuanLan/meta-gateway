-- Scheduled probing, custom probe prompts, and probe-driven member disabling.
--
-- The probe prompt is recorded per task so a run's history says what was
-- actually sent: a task that used "hi" and one that used a longer prompt are
-- not measuring the same thing.
--
-- auto_disabled is the marker that keeps automatic disabling from clobbering
-- human decisions: recovery only re-enables members that a probe itself turned
-- off, never one an operator disabled by hand.

ALTER TABLE probe_tasks ADD COLUMN prompt TEXT NOT NULL DEFAULT '';

-- Consecutive failures, maintained by UpsertModelHealth. Probing is preventive
-- (not reactive), so a single flaky answer must not disable anything.
ALTER TABLE model_health ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;

ALTER TABLE route_members ADD COLUMN auto_disabled INTEGER NOT NULL DEFAULT 0;

-- Scheduled probe configuration. probe_cron empty = schedule off.
-- probe_auto_disable is the consecutive-failure threshold; 0 disables the
-- feature entirely. probe_channels / probe_models hold JSON arrays and an
-- empty array (or a non-array value) means "everything".
ALTER TABLE runtime_settings ADD COLUMN probe_cron TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_settings ADD COLUMN probe_prompt TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_settings ADD COLUMN probe_max_tokens INTEGER NOT NULL DEFAULT 1;
ALTER TABLE runtime_settings ADD COLUMN probe_concurrency INTEGER NOT NULL DEFAULT 4;
ALTER TABLE runtime_settings ADD COLUMN probe_auto_disable INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_settings ADD COLUMN probe_channels TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_settings ADD COLUMN probe_models TEXT NOT NULL DEFAULT '';
