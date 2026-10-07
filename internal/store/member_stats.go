package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Per-model figures for ONE account, over a window. Two sources, each asked the
// question it can answer exactly:
//
//   - Requests, successes and latency come from team_requests — the completed
//     client requests the member's log page lists. A card that says "249
//     requests" is therefore the same 249 the log page shows for that model,
//     instead of an attempts count that would disagree with the page beside it.
//   - Tokens and money come from the billing records: the bill already carries
//     the price layers and the ratio, and it is the number the account is
//     actually charged for.
//
// Which model a request belongs to is resolved with the same expression the
// member log page uses (billed usage row first, newest upstream attempt second),
// so the two pages cannot drift into two answers for one request. A request that
// never reached an upstream — rejected by a policy, a quota or the rate limiter
// — belongs to no model; it is reported as its own count rather than dropped,
// which would quietly inflate every success rate.
type MemberModelStat struct {
	Model            string  `json:"model"`
	Requests         int     `json:"requests"`
	OK               int     `json:"ok"`
	Failed           int     `json:"failed"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	Cost             float64 `json:"cost"`
	AvgLatencyMS     int     `json:"avg_latency_ms"`
	P50MS            int     `json:"p50_ms"`
	P95MS            int     `json:"p95_ms"`
	LastAt           string  `json:"last_at"`
}

// memberModelAccumulator collects one model's requests while the rows stream in;
// the latencies are kept so the percentiles are exact over the window instead of
// being approximated from a sample.
type memberModelAccumulator struct {
	stat      MemberModelStat
	latencies []int
}

// MemberModelStats aggregates one account's traffic per model. userID is
// mandatory: it can never widen into a global read.
//
// Returns the per-model rows, how many requests in the window resolved to no
// model at all, and an error. Rows are ordered by request count descending, then
// by model, so the caller's default ordering is already the useful one.
func (db *DB) MemberModelStats(userID int64, since, until *time.Time) ([]MemberModelStat, int, error) {
	if userID <= 0 {
		return nil, 0, fmt.Errorf("member model stats requires a user")
	}
	where := []string{"t.user_id = ?"}
	args := []any{userID}
	rangeClauses, rangeArgs := createdRange("t.created_at", since, until)
	where = append(where, rangeClauses...)
	args = append(args, rangeArgs...)

	rows, err := db.Query(`SELECT t.status, t.latency_ms, t.created_at,
		COALESCE((SELECT u.model FROM usage_records u WHERE u.request_id=t.request_id AND u.user_id=t.user_id ORDER BY u.id DESC LIMIT 1),''),
		COALESCE((SELECT p.model FROM proxy_logs p WHERE p.request_id=t.request_id AND p.downstream_key_id=t.key_id ORDER BY p.id DESC LIMIT 1),'')
		FROM team_requests t WHERE `+strings.Join(where, " AND ")+` ORDER BY t.rowid DESC`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("member model stats: %w", err)
	}
	defer rows.Close()

	acc := map[string]*memberModelAccumulator{}
	unassigned := 0
	for rows.Next() {
		var status, latency int
		var createdAt, billedModel, upstreamModel string
		if err := rows.Scan(&status, &latency, &createdAt, &billedModel, &upstreamModel); err != nil {
			return nil, 0, fmt.Errorf("member model stats scan: %w", err)
		}
		model := billedModel
		if model == "" {
			model = upstreamModel
		}
		if model == "" {
			unassigned++
			continue
		}
		entry := acc[model]
		if entry == nil {
			entry = &memberModelAccumulator{stat: MemberModelStat{Model: model}}
			acc[model] = entry
		}
		entry.stat.Requests++
		if status >= 400 {
			entry.stat.Failed++
		} else {
			entry.stat.OK++
		}
		entry.latencies = append(entry.latencies, latency)
		if createdAt > entry.stat.LastAt {
			entry.stat.LastAt = createdAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("member model stats rows: %w", err)
	}

	if err := db.addMemberModelBilling(userID, since, until, acc); err != nil {
		return nil, 0, err
	}

	items := make([]MemberModelStat, 0, len(acc))
	for _, entry := range acc {
		sort.Ints(entry.latencies)
		total := 0
		for _, latency := range entry.latencies {
			total += latency
		}
		if n := len(entry.latencies); n > 0 {
			entry.stat.AvgLatencyMS = (total + n/2) / n
		}
		entry.stat.P50MS = percentile(entry.latencies, 50)
		entry.stat.P95MS = percentile(entry.latencies, 95)
		items = append(items, entry.stat)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Requests != items[j].Requests {
			return items[i].Requests > items[j].Requests
		}
		return items[i].Model < items[j].Model
	})
	return items, unassigned, nil
}

// addMemberModelBilling folds the billing records into the accumulators, and
// adds a row for a model that was billed but whose request rows are outside the
// window (the two sources are read with the same bounds, so this only happens
// when a record was written for a request older than the request table's).
func (db *DB) addMemberModelBilling(userID int64, since, until *time.Time, acc map[string]*memberModelAccumulator) error {
	where := []string{"user_id = ?"}
	args := []any{userID}
	rangeClauses, rangeArgs := createdRange("created_at", since, until)
	where = append(where, rangeClauses...)
	args = append(args, rangeArgs...)

	rows, err := db.Query(`SELECT model,
		COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
		COALESCE(SUM(total_tokens),0), COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cost),0)
		FROM usage_records WHERE `+strings.Join(where, " AND ")+` GROUP BY model`, args...)
	if err != nil {
		return fmt.Errorf("member model billing: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var prompt, completion, total, cache int64
		var cost float64
		if err := rows.Scan(&model, &prompt, &completion, &total, &cache, &cost); err != nil {
			return fmt.Errorf("member model billing scan: %w", err)
		}
		if model == "" {
			continue
		}
		entry := acc[model]
		if entry == nil {
			entry = &memberModelAccumulator{stat: MemberModelStat{Model: model}}
			acc[model] = entry
		}
		entry.stat.PromptTokens = prompt
		entry.stat.CompletionTokens = completion
		entry.stat.TotalTokens = total
		entry.stat.CacheReadTokens = cache
		entry.stat.Cost = cost
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("member model billing rows: %w", err)
	}
	return nil
}

// MemberModelProbe is one enabled (route member, channel) pair behind a model,
// carrying the latest probe state and the probe history of the window.
//
// GrantID is the route_members row the pair hangs off — the id a member policy
// is checked against. That is the same check the catalogue performs, so a
// member's health view can never include a pair they may not call.
type MemberModelProbe struct {
	GrantID      int64   `json:"grant_id"`
	Model        string  `json:"model"`
	ChannelID    int64   `json:"channel_id"`
	HasState     bool    `json:"has_state"`
	OK           bool    `json:"ok"`
	LatencyMS    int     `json:"latency_ms"`
	ProbedAt     string  `json:"probed_at"`
	Source       string  `json:"source"`
	Samples      int     `json:"samples"`
	OKSamples    int     `json:"ok_samples"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
}

// MemberModelProbes lists every enabled pair of every enabled model, joined with
// the probe history since the given time (nil = whatever is stored).
//
// It is deliberately not scoped by account: the caller filters with the member's
// own policy, because that filter is the one place that knows what a member may
// reach. Absence means "nothing has been probed", never "everything is
// healthy" — a pair with no probe row reports HasState false, and the console
// says so rather than assuming the best.
func (db *DB) MemberModelProbes(since *time.Time) ([]MemberModelProbe, error) {
	history := `SELECT channel_id, model, COUNT(*) AS samples, SUM(ok) AS ok_samples, COALESCE(AVG(latency_ms),0) AS avg_latency
		FROM probe_results WHERE 1=1`
	args := []any{}
	if since != nil {
		history += ` AND probed_at >= ?`
		args = append(args, sqliteUTC(*since))
	}
	history += ` GROUP BY channel_id, model`

	rows, err := db.Query(`SELECT rm.id, r.model_pattern, rm.channel_id,
		mh.ok, mh.latency_ms, COALESCE(mh.probed_at,''), COALESCE(mh.source,''),
		COALESCE(hist.samples,0), COALESCE(hist.ok_samples,0), hist.avg_latency
		FROM route_members rm
		JOIN routes r ON r.id = rm.route_id
		JOIN channels c ON c.id = rm.channel_id
		LEFT JOIN model_health mh ON mh.channel_id = rm.channel_id AND mh.model = r.model_pattern
		LEFT JOIN (`+history+`) hist ON hist.channel_id = rm.channel_id AND hist.model = r.model_pattern
		WHERE rm.enabled = 1 AND r.enabled = 1 AND c.status = 'enabled'
		ORDER BY r.model_pattern, rm.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("member model probes: %w", err)
	}
	defer rows.Close()

	var items []MemberModelProbe
	for rows.Next() {
		var item MemberModelProbe
		var ok, latency sql.NullInt64
		var avgLatency sql.NullFloat64
		if err := rows.Scan(&item.GrantID, &item.Model, &item.ChannelID, &ok, &latency,
			&item.ProbedAt, &item.Source, &item.Samples, &item.OKSamples, &avgLatency); err != nil {
			return nil, fmt.Errorf("member model probes scan: %w", err)
		}
		item.HasState = ok.Valid
		item.OK = ok.Valid && ok.Int64 == 1
		if latency.Valid {
			item.LatencyMS = int(latency.Int64)
		}
		if avgLatency.Valid {
			item.AvgLatencyMS = avgLatency.Float64
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("member model probes rows: %w", err)
	}
	return items, nil
}
