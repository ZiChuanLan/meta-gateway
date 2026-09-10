-- Per-credential discovered model visibility. A key bound to a specific
-- upstream group (e.g. a New API token scoped to one group) sees only that
-- group's models; recording which key serves which model lets routing pick a
-- key capable of serving the requested model without manual allowlists.
CREATE TABLE IF NOT EXISTS credential_models (
    credential_id INTEGER NOT NULL REFERENCES credentials(id) ON DELETE CASCADE,
    model_name TEXT NOT NULL,
    checked_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (credential_id, model_name)
);
CREATE INDEX idx_credential_models_model ON credential_models(model_name);