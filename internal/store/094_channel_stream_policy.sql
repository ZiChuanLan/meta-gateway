-- Per-channel stream policy: '' follows the client's stream choice,
-- 'force_stream' aggregates an upstream stream for non-streaming clients,
-- 'force_non_stream' replays a non-stream upstream answer as single-chunk
-- SSE to streaming clients. Empty default keeps current behaviour.
ALTER TABLE channels ADD COLUMN stream_policy TEXT NOT NULL DEFAULT '';
