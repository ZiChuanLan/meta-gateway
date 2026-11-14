-- Aggregate models ("聚合模型"): one public model name that fans out to several
-- existing models. The aggregation itself is an ordinary route + members with
-- per-member mapping_json ({"real":"…"}); these tables only carry the console's
-- metadata, so neither the routes/route_members projections nor their Scan
-- orders grow a column.
--
-- model_aggregates: one row per aggregate name. The route is deleted through
-- the normal route API, so ON DELETE CASCADE of a route that no longer exists
-- is a no-op; the row is removed by the aggregate delete endpoint, not by the
-- route delete (an aggregate name and a manual route can coexist).
--
-- console_pins: the shared "pin to top" choice for console boards. kind names
-- the board, ref is the id (or other stable key) of the row pinned. Multiple
-- admins share one list — that is the point of putting it in the database.
CREATE TABLE IF NOT EXISTS model_aggregates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    route_id INTEGER REFERENCES routes(id) ON DELETE SET NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_model_aggregates_name ON model_aggregates(name);

CREATE TABLE IF NOT EXISTS console_pins (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    ref TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(kind, ref)
);
