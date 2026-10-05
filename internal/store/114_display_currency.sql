-- Display currency for money amounts.
--
-- Amounts are STORED in the ledger's unit (USD, exactly what
-- usage_records.cost holds); the symbol and rate here only decide how the
-- console and the member app RENDER them. Changing them never rewrites
-- history: yesterday's 0.42 is still 0.42, it just prints as ¥3.02 today.
--
-- This lives in its own one-row table rather than in runtime_settings: that
-- row is a fifty-column statement whose every SELECT/Scan/INSERT list is
-- written by hand, and a display concern does not belong in the middle of the
-- runtime overrides it would have to be threaded through four times.
CREATE TABLE IF NOT EXISTS site_display_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  currency_symbol TEXT NOT NULL DEFAULT '$',
  currency_rate REAL NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT OR IGNORE INTO site_display_settings (id, currency_symbol, currency_rate) VALUES (1, '$', 1);
