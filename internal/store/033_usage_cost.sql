-- Billing: persist per-relay cost on usage_records so bills can be aggregated
-- by key/model without recomputation, and add a model-ratio table for
-- per-model markup. cost = prompt_tokens/1k * key.price_prompt_per_1k *
-- model_ratio + completion_tokens/1k * key.price_completion_per_1k *
-- model_ratio. A missing ratio row means 1.0 (no markup).
ALTER TABLE usage_records ADD COLUMN cost REAL NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS model_ratios (
    model TEXT PRIMARY KEY,
    ratio REAL NOT NULL DEFAULT 1.0 CHECK (ratio >= 0),
    updated_at TEXT NOT NULL
);
