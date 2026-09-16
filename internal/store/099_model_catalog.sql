-- Records the outcome of the last model-catalog sync so the console can show
-- when the registry was last refreshed and what it changed. Single row by
-- construction (id = 1); the table is a status board, not a history.
CREATE TABLE model_catalog_sync (
  id              INTEGER PRIMARY KEY CHECK (id = 1),
  synced_at       TEXT NOT NULL,
  requested       INTEGER NOT NULL DEFAULT 0,
  matched         INTEGER NOT NULL DEFAULT 0,
  capabilities    INTEGER NOT NULL DEFAULT 0,
  metadata        INTEGER NOT NULL DEFAULT 0,
  prices          INTEGER NOT NULL DEFAULT 0,
  skipped_manual  INTEGER NOT NULL DEFAULT 0,
  missing         INTEGER NOT NULL DEFAULT 0,
  sources         TEXT NOT NULL DEFAULT '',
  errors          TEXT NOT NULL DEFAULT ''
);
