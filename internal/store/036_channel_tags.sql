-- Channel tags: a free-form comma-separated tag list for bulk operations
-- (priority/weight/status/mapping updates by tag).
ALTER TABLE channels ADD COLUMN tags TEXT NOT NULL DEFAULT '';
