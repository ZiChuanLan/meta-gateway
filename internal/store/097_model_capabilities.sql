-- Model capability registry: the *protocol* layer that model_metadata does not
-- cover. model_metadata answers "what is this model" (context window, vendor,
-- price); model_capabilities answers "how do I talk to it" (which endpoint,
-- which request encoding, how many input images, is it async).
--
-- This table is what the image workbench and the chat->edit shim read to decide
-- whether a model can be driven through /v1/images/edits, and whether that
-- endpoint wants JSON or multipart/form-data.
--
-- source: builtin (shipped rules) | discovery (auto-tagged on model discovery)
--         | manual (operator override, always wins and is never re-tagged)
CREATE TABLE IF NOT EXISTS model_capabilities (
  model TEXT PRIMARY KEY,
  kind TEXT NOT NULL DEFAULT 'chat',
  provider TEXT NOT NULL DEFAULT '',
  endpoints TEXT NOT NULL DEFAULT '',
  input_formats TEXT NOT NULL DEFAULT 'json',
  input_modalities TEXT NOT NULL DEFAULT 'text',
  output_modalities TEXT NOT NULL DEFAULT 'text',
  max_input_images INTEGER NOT NULL DEFAULT 0,
  supports_stream INTEGER NOT NULL DEFAULT 1,
  supports_tools INTEGER NOT NULL DEFAULT 0,
  supports_json_mode INTEGER NOT NULL DEFAULT 0,
  async_task INTEGER NOT NULL DEFAULT 0,
  size_options TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'builtin',
  notes TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_model_capabilities_kind ON model_capabilities (kind);
