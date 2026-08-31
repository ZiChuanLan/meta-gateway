-- Failed relay attempts persist a truncated excerpt of what the upstream
-- actually returned (error body / transport error text) so the log UI can
-- show the real failure instead of only the error category.
ALTER TABLE proxy_logs ADD COLUMN error_detail TEXT NOT NULL DEFAULT '';
