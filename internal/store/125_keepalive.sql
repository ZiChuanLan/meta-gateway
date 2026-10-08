-- Keepalive, and the call policy that decides what an automated call looks like.
--
-- Why idle-driven and not a timer: the ban these sites enforce is "no call for
-- N days", and N is a property of the site (15 here, 30 there). A fixed daily
-- ping wastes calls on a 30-day window and arrives too late on a 15-day one.
-- What is recorded is therefore the last successful real call, and a round
-- fires when the remaining days drop to the safety margin.
--
-- Two columns carry the policy:
--
--   call_policy   how this site wants automated traffic to look.
--                   ''              inherit (sites: allow_probe)
--                   allow_probe     a minimal one-token probe is fine
--                   real_calls_only the site bans probing, so every automated
--                                   dialog call takes the shape of a real small
--                                   request (normal token budget, a real
--                                   question, a system message)
--                 Nothing is ever blocked, because reads (model list, balance)
--                 are not calls at any site we know of, and an operator who
--                 wants a site left alone turns the switch off. Unknown values
--                 normalise to allow_probe: the default must not start spending
--                 tokens on its own.
--
--   last_real_call_at
--                 when the upstream last *received* a chat request on this
--                 channel. A column rather than a log query because log retention
--                 (PROXY_LOG_RETENTION_DAYS) can be shorter than a 15-day ban
--                 window, and a "not found" that only means "the rows were
--                 vacuumed" would either miss the call or fire it every tick.
--
-- The counting unit is the credential — one key is one ban window — while the
-- sending unit stays channel x model, so two channels sharing a key cannot
-- double-call the same account. That resolution is done at read time
-- (ChannelStore.KeepaliveTargets), not stored here.
ALTER TABLE sites ADD COLUMN call_policy TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN keepalive_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN keepalive_idle_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN keepalive_safety_margin_days INTEGER NOT NULL DEFAULT 2;
ALTER TABLE sites ADD COLUMN keepalive_model TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN keepalive_prompt TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN keepalive_max_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN keepalive_daily_cap INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN keepalive_quiet_hours TEXT NOT NULL DEFAULT '';

-- A channel inherits the site's policy and window; the overrides exist because
-- one site can hold an account with a shorter window, or one that must be left
-- alone entirely. keepalive_enabled is NULLable on purpose: NULL = inherit the
-- site, 1/0 = an explicit decision for this channel.
ALTER TABLE channels ADD COLUMN call_policy TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN keepalive_enabled INTEGER;
ALTER TABLE channels ADD COLUMN keepalive_idle_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN last_real_call_at TEXT;

-- The global layer: a kill switch that stops every round, the cadence of the
-- check itself, and the window used by a site that has not been given one.
ALTER TABLE runtime_settings ADD COLUMN keepalive_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_settings ADD COLUMN keepalive_check_interval_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_settings ADD COLUMN keepalive_default_idle_days INTEGER NOT NULL DEFAULT 0;

-- Every keepalive call leaves a row. It answers three questions that no other
-- table can: how many calls this credential already had today (the daily cap),
-- what the gateway said when a site asked why it was called (purpose + the idle
-- reasoning), and whether a site that bans probing was called in the shape it
-- asked for (form).
CREATE TABLE IF NOT EXISTS keepalive_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  credential_id INTEGER NOT NULL DEFAULT 0,
  site_id INTEGER NOT NULL DEFAULT 0,
  channel_id INTEGER NOT NULL,
  channel_name TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  form TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  ok INTEGER NOT NULL DEFAULT 0,
  status_code INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_keepalive_events_credential
  ON keepalive_events(credential_id, created_at);
CREATE INDEX IF NOT EXISTS idx_keepalive_events_channel
  ON keepalive_events(channel_id, created_at);

-- The shape each probe was sent in. Recorded so "we called that site in the form
-- it asked for" is checkable from the console rather than taken on faith: the
-- policy only exists if its effect is visible.
ALTER TABLE probe_results ADD COLUMN form TEXT NOT NULL DEFAULT '';
