-- New observations retain their actual identity; legacy rows remain unassigned.
ALTER TABLE proxy_logs ADD COLUMN completion_outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE proxy_logs ADD COLUMN member_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_logs ADD COLUMN request_kind TEXT NOT NULL DEFAULT 'traffic';
ALTER TABLE probe_results ADD COLUMN member_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE probe_results ADD COLUMN upstream_model TEXT NOT NULL DEFAULT '';
ALTER TABLE site_probe_samples ADD COLUMN source_key TEXT NOT NULL DEFAULT '';
ALTER TABLE site_probe_samples ADD COLUMN ratio_known INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_proxy_logs_member_time ON proxy_logs(member_id, created_at);
CREATE INDEX idx_probe_results_member_time ON probe_results(member_id, probed_at);
CREATE TABLE site_probe_heartbeats (
    site_id INTEGER NOT NULL,
    source_key TEXT NOT NULL,
    monitor_id TEXT NOT NULL,
    raw_model TEXT NOT NULL,
    group_name TEXT NOT NULL DEFAULT '',
    probed_at TEXT NOT NULL,
    status INTEGER NOT NULL,
    ping_ms INTEGER,
    PRIMARY KEY(site_id, source_key, monitor_id, probed_at)
);
CREATE INDEX idx_site_beats_time ON site_probe_heartbeats(probed_at);
CREATE TABLE route_request_outcomes (
    id INTEGER PRIMARY KEY,
    request_id TEXT NOT NULL,
    route_id INTEGER NOT NULL,
    member_id INTEGER NOT NULL DEFAULT 0,
    channel_id INTEGER NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    upstream_model TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL,
    status INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_route_outcomes_route_time ON route_request_outcomes(route_id, created_at);
CREATE INDEX idx_probe_results_time ON probe_results(probed_at);
CREATE INDEX idx_route_outcomes_time ON route_request_outcomes(created_at);
