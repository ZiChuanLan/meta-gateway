-- 084: native WebDAV upload (Meta Gateway exchange backup to cloud drive).
ALTER TABLE webdav_settings ADD COLUMN upload_enabled INTEGER NOT NULL DEFAULT 0;
