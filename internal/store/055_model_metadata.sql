-- Model metadata library: optional per-model capability annotations shown on
-- the Models page. context_window 0 = unknown; input/output modalities are
-- comma lists (text,image,audio); supports_thinking -1 = unknown, 0 = no,
-- 1 = yes. One row per canonical model name (route model_pattern).
CREATE TABLE IF NOT EXISTS model_metadata (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  model_name TEXT NOT NULL UNIQUE,
  context_window INTEGER NOT NULL DEFAULT 0,
  input_modalities TEXT NOT NULL DEFAULT '',
  output_modalities TEXT NOT NULL DEFAULT '',
  supports_thinking INTEGER NOT NULL DEFAULT -1,
  vendor TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_model_metadata_name ON model_metadata (model_name);
