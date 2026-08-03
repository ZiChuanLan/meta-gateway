-- Channel-level consecutive failure counter for auto-disable.
-- Incremented on every failed relay attempt (independent of member cooldown),
-- reset on success or recovery. When it reaches CHANNEL_AUTO_DISABLE_THRESHOLD
-- the channel is marked auto_disabled.
ALTER TABLE channels ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
