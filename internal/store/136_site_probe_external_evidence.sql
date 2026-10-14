-- What the directory's reading rests on, and when it last looked.
--
-- Measured against the shipped monitoring directory (2026-10-14, 1,984 rows):
-- every row carries requestCount/successCount/failureCount over its own window
-- and a collectedAt that advances every few minutes, while observedAt — the
-- start of the last bucket that had samples — can sit hours behind, and
-- serviceState reads "no_samples" whenever the current bucket is empty. The
-- gateway judged freshness from observedAt alone (anything older than two hours
-- was 已过期) and dropped "no_samples" rows outright, so a pair whose bucket was
-- momentarily empty was hidden in the console while the directory's own
-- dashboard still showed the same reading: measured, 96.9% over 58,466
-- requests, marked 「暂无样本」 next to it.
--
-- These columns let the console print the reading the directory actually
-- publishes, and let the gateway say when it is the *snapshot* that is old
-- instead of blaming the data. `collected_at` is untouched: it stays what it
-- always was — when this gateway wrote the row.
ALTER TABLE site_probe_external ADD COLUMN checked_at TEXT NOT NULL DEFAULT '';
ALTER TABLE site_probe_external ADD COLUMN request_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE site_probe_external ADD COLUMN success_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE site_probe_external ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0;
