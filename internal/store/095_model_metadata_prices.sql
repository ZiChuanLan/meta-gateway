-- Per-model self-set pricing on the metadata library: unit prices per 1k
-- tokens used for cost estimation and billing. 0 = fall back to the
-- downstream key's per-key prices (legacy behaviour).
ALTER TABLE model_metadata ADD COLUMN price_prompt_per_1k REAL NOT NULL DEFAULT 0;
ALTER TABLE model_metadata ADD COLUMN price_completion_per_1k REAL NOT NULL DEFAULT 0;
ALTER TABLE model_metadata ADD COLUMN price_cache_per_1k REAL NOT NULL DEFAULT 0;
