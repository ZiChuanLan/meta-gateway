-- 066_channel_max_concurrent.sql
-- Hard per-channel concurrency ceiling: requests beyond the limit queue
-- FIFO at the proxy instead of being dropped or failed over (0 = unlimited).
ALTER TABLE channels ADD COLUMN max_concurrent INTEGER NOT NULL DEFAULT 0;
