ALTER TABLE credentials ADD COLUMN checkin_enabled INTEGER NOT NULL DEFAULT 0;

CREATE TABLE checkin_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    credential_id INTEGER NOT NULL REFERENCES credentials(id) ON DELETE CASCADE,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    category TEXT NOT NULL,
    message TEXT NOT NULL,
    reward TEXT NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    ran_at TEXT NOT NULL
);

CREATE INDEX idx_checkin_logs_ran_at ON checkin_logs(ran_at DESC, id DESC);
CREATE INDEX idx_checkin_logs_credential ON checkin_logs(credential_id, ran_at DESC);
CREATE INDEX idx_checkin_logs_site ON checkin_logs(site_id, ran_at DESC);
