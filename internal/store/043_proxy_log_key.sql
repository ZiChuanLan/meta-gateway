-- Record which upstream key served a request (sha256 fingerprint, never plaintext).
ALTER TABLE proxy_logs ADD COLUMN key_fingerprint TEXT NOT NULL DEFAULT '';
