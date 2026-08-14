-- Per-component pricing: cache-read tokens get their own unit price.
-- 0 = fall back to the prompt price (current behavior). Cache-read is billed
-- on (prompt - cache_read) at prompt price + cache_read at this price.
ALTER TABLE downstream_keys ADD COLUMN price_cache_per_1k REAL NOT NULL DEFAULT 0;
