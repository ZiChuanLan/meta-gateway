-- Which same-key re-send this attempt is (0 = the first try on that key).
--
-- CHANNEL_RETRY_TIMES asks the operator a question they cannot answer: the
-- console exposed the setting but nothing recorded whether the re-sends it
-- authorises ever rescued a request. With this column the log says it directly:
-- a 200 with key_retries > 0 is a request that only survived because of the
-- setting; a column of zeros means the setting is paying latency for nothing.
-- (The cross-channel attempt counter stays in `attempt`; the two are different
-- budgets and are counted separately.)
ALTER TABLE proxy_logs ADD COLUMN key_retries INTEGER NOT NULL DEFAULT 0;
