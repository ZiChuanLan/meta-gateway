-- A separate marker distinguishes a successful empty snapshot from no snapshot.
CREATE TABLE discovery_snapshots (
    channel_id INTEGER PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL DEFAULT 1
);
INSERT INTO discovery_snapshots(channel_id) SELECT DISTINCT channel_id FROM discovered_models;
CREATE TABLE model_changes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    model_name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('added','removed')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','ignored','applied','resolved')),
    detected_at TEXT NOT NULL,
    candidates_json TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX idx_model_changes_channel_status ON model_changes(channel_id, status);
-- Prevent automatic adoption from recreating a source binding after an
-- explicit cross-channel replacement. Removed with the member itself.
CREATE TABLE model_change_remaps (
    member_id INTEGER NOT NULL REFERENCES route_members(id) ON DELETE CASCADE,
    source_channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    PRIMARY KEY(member_id, source_channel_id)
);
