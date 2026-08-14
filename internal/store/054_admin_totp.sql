-- Admin TOTP 2FA: single-row table holding the (encrypted) TOTP secret and
-- the enabled flag. Empty secret_encrypted = not set up.
CREATE TABLE IF NOT EXISTS admin_totp (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  secret_encrypted TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);
INSERT OR IGNORE INTO admin_totp (id, secret_encrypted, enabled, updated_at) VALUES (1, '', 0, '');
