-- A check-in schedule belongs to a connection, but the credential it runs with
-- (and the cookie/token inside it) lives on the SITE. Deleting a connection used
-- to leave that credential with checkin_enabled=1, so the daily job kept calling
-- an upstream the operator had just removed: the check-in page grew one
-- "could not reach upstream check-in endpoint" row per day, forever, for a
-- connection that no longer existed in the console.
--
-- The delete path now turns the schedule off when a site's last channel goes
-- (ChannelStore.Delete). This backfills the same rule for data written before
-- that fix.
--
-- Two things this deliberately does NOT do:
--   * delete the site, its credentials or its check-in history — the history is
--     the only record of what those connections did, and re-adding a connection
--     to the same base URL reuses the site and its stored token;
--   * touch the external check-in family — those sites never have a channel by
--     design, so "no channel" is their normal state, not an orphan.
UPDATE credentials
   SET checkin_enabled = 0,
       updated_at = datetime('now')
 WHERE checkin_enabled = 1
   AND site_id NOT IN (SELECT site_id FROM channels WHERE site_id IS NOT NULL)
   AND site_id IN (SELECT id FROM sites WHERE platform <> 'external-checkin');
