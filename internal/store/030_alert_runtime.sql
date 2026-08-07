-- Alert matrix runtime overrides: multi-channel alert config JSON
-- (webhook/bark/serverchan/telegram/smtp + cooldown + daily summary flag)
-- plus sweep / daily-summary intervals. NULL = not overridden (env bootstrap).
ALTER TABLE runtime_settings ADD COLUMN alert_config_json TEXT;
ALTER TABLE runtime_settings ADD COLUMN alert_sweep_interval_seconds INTEGER;
ALTER TABLE runtime_settings ADD COLUMN alert_daily_summary_interval_seconds INTEGER;
