-- 085: split WebDAV sync into two independent directions, each with its own
-- schedule: AAH/exchange import (enabled) and native backup upload
-- (upload_enabled). Seed both crons from the previous shared cron_expr so the
-- deployed behavior is preserved; the literal "off" disarms one direction.
ALTER TABLE webdav_settings ADD COLUMN download_cron TEXT NOT NULL DEFAULT '';
ALTER TABLE webdav_settings ADD COLUMN upload_cron TEXT NOT NULL DEFAULT '';
UPDATE webdav_settings
SET download_cron = CASE WHEN cron_expr <> '' THEN cron_expr ELSE '0 */6 * * *' END,
    upload_cron = CASE WHEN cron_expr <> '' THEN cron_expr ELSE '0 */6 * * *' END;
