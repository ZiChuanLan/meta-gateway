-- 086: give each WebDAV direction its own connection. url/username/
-- password_enc/backup_password_enc now serve the import direction only; the
-- upload_* columns are the native backup target. Existing rows are copied so a
-- deployed bidirectional setup keeps working on the same drive until the
-- operator points the two directions apart on purpose.
ALTER TABLE webdav_settings ADD COLUMN upload_url TEXT NOT NULL DEFAULT '';
ALTER TABLE webdav_settings ADD COLUMN upload_username TEXT NOT NULL DEFAULT '';
ALTER TABLE webdav_settings ADD COLUMN upload_password_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE webdav_settings ADD COLUMN upload_backup_password_enc TEXT NOT NULL DEFAULT '';
UPDATE webdav_settings
SET upload_url = url,
    upload_username = username,
    upload_password_enc = password_enc,
    upload_backup_password_enc = backup_password_enc
WHERE url <> '';
