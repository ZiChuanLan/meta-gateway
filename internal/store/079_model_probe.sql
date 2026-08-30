-- Scheduled and on-demand model probing.
--
-- A probe is a real chat completion sent through the same routing/proxy path as
-- /v1, pinned to one channel, with a tiny max_tokens. It costs a few tokens per
-- (channel, model) pair, so probes are explicit tasks the operator starts and
-- can cancel — never background traffic.

-- One probe run: the selection, the progress, and the tally.
CREATE TABLE IF NOT EXISTS probe_tasks (
    id INTEGER PRIMARY KEY,
    status TEXT NOT NULL,             -- running | done | cancelled
    total INTEGER NOT NULL,
    completed INTEGER NOT NULL DEFAULT 0,
    ok_count INTEGER NOT NULL DEFAULT 0,
    fail_count INTEGER NOT NULL DEFAULT 0,
    max_tokens INTEGER NOT NULL,
    concurrency INTEGER NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_probe_tasks_started ON probe_tasks(started_at DESC);

-- One row per (channel, model) attempt. Kept as history so a flaky model shows
-- its pattern over time rather than only its latest state.
CREATE TABLE IF NOT EXISTS probe_results (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL,
    channel_id INTEGER NOT NULL,
    model TEXT NOT NULL,
    ok INTEGER NOT NULL,
    status_code INTEGER,
    latency_ms INTEGER,
    error TEXT,
    probed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_probe_results_task ON probe_results(task_id);
CREATE INDEX IF NOT EXISTS idx_probe_results_pair ON probe_results(channel_id, model, probed_at DESC);

-- Latest known state per (channel, model). This is the small table routing
-- consults to push a recently-failed pair to the back of the queue, so it is
-- keyed for point lookups and refreshed by every probe.
CREATE TABLE IF NOT EXISTS model_health (
    channel_id INTEGER NOT NULL,
    model TEXT NOT NULL,
    ok INTEGER NOT NULL,
    latency_ms INTEGER,
    probed_at TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'probe',
    PRIMARY KEY (channel_id, model)
);
CREATE INDEX IF NOT EXISTS idx_model_health_probed ON model_health(probed_at);
