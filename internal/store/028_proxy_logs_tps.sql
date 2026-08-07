-- Stream throughput metric (AxonHub-style TPS): completion tokens per second
-- over the effective latency (total minus first-byte), filled on token update.
ALTER TABLE proxy_logs ADD COLUMN tokens_per_second REAL NOT NULL DEFAULT 0;
