package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Probe task states. A task is never deleted — the history is what lets a
// flaky model be distinguished from a dead one.
const (
	ProbeTaskRunning   = "running"
	ProbeTaskDone      = "done"
	ProbeTaskCancelled = "cancelled"
)

// ProbeTask is one probe run over a selection of (channel, model) pairs.
type ProbeTask struct {
	ID          int64  `json:"id"`
	Status      string `json:"status"`
	Total       int    `json:"total"`
	Completed   int    `json:"completed"`
	OKCount     int    `json:"ok_count"`
	FailCount   int    `json:"fail_count"`
	MaxTokens   int    `json:"max_tokens"`
	Concurrency int    `json:"concurrency"`
	// Prompt is what was sent to the upstream. Recorded per task because a run
	// that used "hi" and one that used a long prompt do not measure the same
	// thing, and the history has to say which was which.
	Prompt     string     `json:"prompt"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// ProbeResult is a single probe attempt against one channel for one model.
type ProbeResult struct {
	ID         int64     `json:"id"`
	TaskID     int64     `json:"task_id"`
	ChannelID  int64     `json:"channel_id"`
	Model      string    `json:"model"`
	OK         bool      `json:"ok"`
	StatusCode int       `json:"status_code"`
	LatencyMS  int       `json:"latency_ms"`
	Error      string    `json:"error,omitempty"`
	ProbedAt   time.Time `json:"probed_at"`
}

// ModelHealth is the latest known state of one (channel, model) pair, shown in
// the admin health view. Routing does not consume this table: eligibility and
// ordering run off member/channel state and the proxy's in-memory error and
// latency windows.
type ModelHealth struct {
	ChannelID int64     `json:"channel_id"`
	Model     string    `json:"model"`
	OK        bool      `json:"ok"`
	LatencyMS int       `json:"latency_ms"`
	ProbedAt  time.Time `json:"probed_at"`
	Source    string    `json:"source"`
	// ConsecutiveFailures drives automatic member disabling. It only counts
	// probe failures and resets on the first success.
	ConsecutiveFailures int `json:"consecutive_failures"`
}

func (db *DB) CreateProbeTask(task *ProbeTask) (int64, error) {
	res, err := db.Exec(`INSERT INTO probe_tasks (status, total, completed, ok_count, fail_count, max_tokens, concurrency, prompt, started_at)
		VALUES (?, ?, 0, 0, 0, ?, ?, ?, datetime('now'))`,
		task.Status, task.Total, task.MaxTokens, task.Concurrency, task.Prompt)
	if err != nil {
		return 0, fmt.Errorf("probe: create task: %w", err)
	}
	return res.LastInsertId()
}

func (db *DB) GetProbeTask(id int64) (*ProbeTask, error) {
	var task ProbeTask
	err := db.QueryRow(`SELECT id, status, total, completed, ok_count, fail_count, max_tokens, concurrency, prompt, started_at, finished_at
		FROM probe_tasks WHERE id = ?`, id).
		Scan(&task.ID, &task.Status, &task.Total, &task.Completed, &task.OKCount, &task.FailCount,
			&task.MaxTokens, &task.Concurrency, &task.Prompt,
			scanTime(&task.StartedAt), scanNullTime(&task.FinishedAt))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("probe: get task: %w", err)
	}
	return &task, nil
}

func (db *DB) ListProbeTasks(limit int) ([]ProbeTask, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.Query(`SELECT id, status, total, completed, ok_count, fail_count, max_tokens, concurrency, prompt, started_at, finished_at
		FROM probe_tasks ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("probe: list tasks: %w", err)
	}
	defer rows.Close()
	var tasks []ProbeTask
	for rows.Next() {
		var task ProbeTask
		if err := rows.Scan(&task.ID, &task.Status, &task.Total, &task.Completed, &task.OKCount, &task.FailCount,
			&task.MaxTokens, &task.Concurrency, &task.Prompt,
			scanTime(&task.StartedAt), scanNullTime(&task.FinishedAt)); err != nil {
			return nil, fmt.Errorf("probe: scan task: %w", err)
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// UpdateProbeProgress bumps the counters. Called once per finished probe; the
// probe itself takes far longer than this write, so there is no need to batch.
func (db *DB) UpdateProbeProgress(id int64, completed, okCount, failCount int) error {
	if _, err := db.Exec(`UPDATE probe_tasks SET completed = ?, ok_count = ?, fail_count = ? WHERE id = ?`,
		completed, okCount, failCount, id); err != nil {
		return fmt.Errorf("probe: update progress: %w", err)
	}
	return nil
}

// FinishProbeTask marks the task terminal and persists the final tally in
// the same statement: a done/cancelled row must never be readable with a
// stale completed count (the progress writes race the drain on busy CI).
func (db *DB) FinishProbeTask(id int64, status string, completed, okCount, failCount int) error {
	if _, err := db.Exec(`UPDATE probe_tasks SET status = ?, completed = ?, ok_count = ?, fail_count = ?, finished_at = datetime('now') WHERE id = ?`,
		status, completed, okCount, failCount, id); err != nil {
		return fmt.Errorf("probe: finish task: %w", err)
	}
	return nil
}

// CancelProbeTask marks a task cancelled while it is still running. The worker
// notices on its next iteration and stops issuing probes.
func (db *DB) CancelProbeTask(id int64) error {
	if _, err := db.Exec(`UPDATE probe_tasks SET status = ? WHERE id = ? AND status = ?`,
		ProbeTaskCancelled, id, ProbeTaskRunning); err != nil {
		return fmt.Errorf("probe: cancel task: %w", err)
	}
	return nil
}

func (db *DB) InsertProbeResult(result *ProbeResult) error {
	var statusCode any
	if result.StatusCode > 0 {
		statusCode = result.StatusCode
	}
	if _, err := db.Exec(`INSERT INTO probe_results (task_id, channel_id, model, ok, status_code, latency_ms, error, probed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		result.TaskID, result.ChannelID, result.Model, boolInt(result.OK), statusCode, result.LatencyMS, result.Error); err != nil {
		return fmt.Errorf("probe: insert result: %w", err)
	}
	return nil
}

func (db *DB) ListProbeResults(taskID int64, limit int) ([]ProbeResult, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := db.Query(`SELECT id, task_id, channel_id, model, ok, status_code, latency_ms, error, probed_at
		FROM probe_results WHERE task_id = ? ORDER BY id LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("probe: list results: %w", err)
	}
	defer rows.Close()
	var results []ProbeResult
	for rows.Next() {
		var result ProbeResult
		var statusCode, latency sql.NullInt64
		if err := rows.Scan(&result.ID, &result.TaskID, &result.ChannelID, &result.Model,
			&result.OK, &statusCode, &latency, &result.Error, scanTime(&result.ProbedAt)); err != nil {
			return nil, fmt.Errorf("probe: scan result: %w", err)
		}
		if statusCode.Valid {
			result.StatusCode = int(statusCode.Int64)
		}
		if latency.Valid {
			result.LatencyMS = int(latency.Int64)
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// UpsertModelHealth records the latest state of a pair and returns the
// resulting consecutive-failure count. Every probe writes here so the admin
// health view has a cheap point lookup.
//
// The counter is advanced inside the upsert rather than read-then-written from
// Go: two probes for the same pair can land close together, and a read-modify-
// write in application code would let one of them overwrite the other.
func (db *DB) UpsertModelHealth(health *ModelHealth) (int, error) {
	source := health.Source
	if source == "" {
		source = "probe"
	}
	ok := boolInt(health.OK)
	var failures int
	err := db.QueryRow(`INSERT INTO model_health (channel_id, model, ok, latency_ms, probed_at, source, consecutive_failures)
		VALUES (?, ?, ?, ?, datetime('now'), ?, CASE WHEN ? = 1 THEN 0 ELSE 1 END)
		ON CONFLICT(channel_id, model) DO UPDATE SET
			ok = excluded.ok,
			latency_ms = excluded.latency_ms,
			probed_at = excluded.probed_at,
			source = excluded.source,
			consecutive_failures = CASE WHEN excluded.ok = 1 THEN 0 ELSE model_health.consecutive_failures + 1 END
		RETURNING consecutive_failures`,
		health.ChannelID, health.Model, ok, health.LatencyMS, source, ok,
	).Scan(&failures)
	if err != nil {
		return 0, fmt.Errorf("probe: upsert health: %w", err)
	}
	return failures, nil
}

func (db *DB) ListModelHealth() ([]ModelHealth, error) {
	rows, err := db.Query(`SELECT channel_id, model, ok, latency_ms, probed_at, source, consecutive_failures
		FROM model_health ORDER BY ok ASC, probed_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("probe: list health: %w", err)
	}
	defer rows.Close()
	var items []ModelHealth
	for rows.Next() {
		var item ModelHealth
		var latency sql.NullInt64
		if err := rows.Scan(&item.ChannelID, &item.Model, &item.OK, &latency,
			scanTime(&item.ProbedAt), &item.Source, &item.ConsecutiveFailures); err != nil {
			return nil, fmt.Errorf("probe: scan health: %w", err)
		}
		if latency.Valid {
			item.LatencyMS = int(latency.Int64)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
