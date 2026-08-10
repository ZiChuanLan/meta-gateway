-- Per-key model allowlist: a key only serves these models (comma-separated,
-- "*" suffix wildcards allowed). Empty = serves every model.
ALTER TABLE credentials ADD COLUMN models_csv TEXT NOT NULL DEFAULT '';
