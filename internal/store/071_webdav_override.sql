-- 071: distinguish an explicit Admin WebDAV setting from the bootstrap row.
-- Existing non-empty rows were operator input in pre-071 versions; preserve
-- that intent while allowing a future explicit disabled/empty save to win over
-- environment variables.
ALTER TABLE webdav_settings ADD COLUMN has_override INTEGER NOT NULL DEFAULT 0;
UPDATE webdav_settings
SET has_override = CASE
    WHEN enabled <> 0 OR url <> '' OR username <> '' OR password_enc <> '' OR backup_password_enc <> '' THEN 1
    ELSE 0
END;
