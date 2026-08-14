-- Scheduled database maintenance: a five-field cron expression (same format
-- as checkin_cron; empty = disabled) that runs the orphan-row GC + SQLite
-- VACUUM pass on schedule. Default '0 4 * * *' = daily 04:00.
ALTER TABLE runtime_settings ADD COLUMN db_gc_cron TEXT NOT NULL DEFAULT '';
