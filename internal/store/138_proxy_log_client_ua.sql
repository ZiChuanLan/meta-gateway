-- The caller's raw User-Agent, kept beside the coarse client_family bucket.
--
-- The bucket is a hardcoded list and can only ever name the clients somebody
-- thought of: pi sends `pi (win32 …; x64)`, Claude Code `claude-cli/2.1.207
-- (external, sdk-cli)`, opencode `opencode/1.18.34 ai-sdk/…` — and every one of
-- them landed in the same bucket ("browser"), which is both wrong and
-- unfalsifiable after the fact. The bucket alone cannot be repaired
-- retrospectively; the string is what lets an operator recognize a client the
-- classifier does not know yet.
ALTER TABLE proxy_logs ADD COLUMN client_ua TEXT NOT NULL DEFAULT '';
