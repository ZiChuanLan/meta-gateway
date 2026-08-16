-- 068_downstream_key_token_enc.sql
-- Store the encrypted plaintext token so operators can re-view/copy a
-- downstream key after creation (like New-API). token_enc holds a
-- MASTER_KEY-encrypted "v2:..." ciphertext; empty for keys created before
-- this migration (those can only be rotated to obtain a new plaintext).
ALTER TABLE downstream_keys ADD COLUMN token_enc TEXT NOT NULL DEFAULT '';
