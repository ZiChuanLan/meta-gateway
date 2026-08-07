-- Per-route routing mode: 'auto' (follow global latency switch),
-- 'latency' (force latency-aware picking), or 'weighted' (force weight picking).
ALTER TABLE routes ADD COLUMN routing_mode TEXT NOT NULL DEFAULT 'auto';
