-- Admin toggle for the GitHub release update check (NULL = follow env).
ALTER TABLE runtime_settings ADD COLUMN update_check_enabled INTEGER;
