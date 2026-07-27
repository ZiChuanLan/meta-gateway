-- Admin-editable WebDAV pull settings (single row). Secrets stored encrypted by app layer.
CREATE TABLE IF NOT EXISTS webdav_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0,
    url TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    password_enc TEXT NOT NULL DEFAULT '',
    backup_password_enc TEXT NOT NULL DEFAULT '',
    cron_expr TEXT NOT NULL DEFAULT '0 */6 * * *',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT OR IGNORE INTO webdav_settings (id, enabled, url, username, password_enc, backup_password_enc, cron_expr)
VALUES (1, 0, '', '', '', '', '0 */6 * * *');
