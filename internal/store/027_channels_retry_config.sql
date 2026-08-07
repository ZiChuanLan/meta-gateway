-- Per-channel retry policy (AxonHub-style retryable status codes + error
-- text patterns). Empty string = global defaults (429 + 5xx) only.
ALTER TABLE channels ADD COLUMN retry_config TEXT NOT NULL DEFAULT '';
