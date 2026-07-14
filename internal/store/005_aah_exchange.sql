ALTER TABLE credentials ADD COLUMN import_fingerprint TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_credentials_import_fingerprint
    ON credentials(import_fingerprint)
    WHERE import_fingerprint IS NOT NULL AND import_fingerprint <> '';

CREATE INDEX IF NOT EXISTS idx_sites_base_url ON sites(base_url);
CREATE INDEX IF NOT EXISTS idx_channels_base_url ON channels(base_url);
