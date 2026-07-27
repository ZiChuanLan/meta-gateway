-- Site ownership for channels is hard: deleting a site removes its channels.
-- Previously ON DELETE SET NULL left orphan channels with models/routes still live.
-- Route members cascade via channel_id; discovered_models cascade via channel_id.

PRAGMA foreign_keys = OFF;

CREATE TABLE channels_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id INTEGER REFERENCES sites(id) ON DELETE CASCADE,
    credential_id INTEGER REFERENCES credentials(id) ON DELETE SET NULL,
    name TEXT NOT NULL DEFAULT '',
    base_url TEXT NOT NULL DEFAULT '',
    models_csv TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT 'default',
    priority INTEGER NOT NULL DEFAULT 0,
    weight INTEGER NOT NULL DEFAULT 100,
    status TEXT NOT NULL DEFAULT 'enabled',
    type_hint TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Keep only channels still attached to an existing site.
INSERT INTO channels_new (
    id, site_id, credential_id, name, base_url, models_csv, group_name,
    priority, weight, status, type_hint, created_at, updated_at
)
SELECT
    id, site_id, credential_id, name, base_url, models_csv, group_name,
    priority, weight, status, type_hint, created_at, updated_at
FROM channels
WHERE site_id IS NOT NULL
  AND site_id IN (SELECT id FROM sites);

-- Drop dependents of orphan (NULL site_id) channels before dropping old table.
DELETE FROM route_members
WHERE channel_id NOT IN (SELECT id FROM channels_new);
DELETE FROM discovered_models
WHERE channel_id NOT IN (SELECT id FROM channels_new);

DROP TABLE channels;
ALTER TABLE channels_new RENAME TO channels;

CREATE INDEX IF NOT EXISTS idx_channels_status ON channels(status);
CREATE INDEX IF NOT EXISTS idx_channels_credential ON channels(credential_id);
CREATE INDEX IF NOT EXISTS idx_channels_site ON channels(site_id);

-- Routes that lost every member after orphan cleanup should not keep ghost models.
DELETE FROM routes
WHERE id NOT IN (SELECT DISTINCT route_id FROM route_members);

PRAGMA foreign_keys = ON;
