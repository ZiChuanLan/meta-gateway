-- Separate account authentication mode and optional browser-session cookie.
-- Existing credentials remain bearer-compatible by default.
ALTER TABLE credentials ADD COLUMN auth_mode TEXT NOT NULL DEFAULT 'access_token';
ALTER TABLE credentials ADD COLUMN cookie_enc TEXT NOT NULL DEFAULT '';
