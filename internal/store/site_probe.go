package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// Site probe run states. A run is one collection round against a site's public
// probe source; only "ok" runs contribute samples to a verdict.
const (
	SiteProbeRunRunning = "running"
	SiteProbeRunOK      = "ok"
	SiteProbeRunFailed  = "failed"
)

// SiteProbeRun is one collection round against a site's public probe source.
type SiteProbeRun struct {
	ID           int64      `json:"id"`
	SiteID       int64      `json:"site_id"`
	SourceKind   string     `json:"source_kind"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Status       string     `json:"status"`
	MonitorCount int        `json:"monitor_count"`
	Error        string     `json:"error,omitempty"`
}

// SiteProbePrice is a price observation attached to a sample: the amounts a
// site publishes, plus what the shared normalizer made of them.
//
// The amounts are in `Currency`, which is whatever the site declares for its
// own price table (New-API's quota_display_type) — NOT necessarily USD. Nothing
// here is converted: the gateway shows what the site published and refuses to
// invent an exchange rate.
type SiteProbePrice struct {
	// Mode is "" (no price), "token" (per 1M tokens) or "fixed" (per request).
	Mode string `json:"mode,omitempty"`
	// Currency is the ISO code the site declares; Symbol is what it prints
	// ("$", "¥", "¤" when the site configured a custom one).
	Currency            string  `json:"currency,omitempty"`
	CurrencySymbol      string  `json:"currency_symbol,omitempty"`
	InputPerMillion     float64 `json:"input_per_million,omitempty"`
	OutputPerMillion    float64 `json:"output_per_million,omitempty"`
	CacheReadPerMillion float64 `json:"cache_read_per_million,omitempty"`
	PerRequest          float64 `json:"per_request,omitempty"`
	GroupRatio          float64 `json:"group_ratio,omitempty"`
	// Raw is what the site published (e.g. the billing expression), kept so an
	// unparsed quote can be shown instead of a guessed number.
	Raw string `json:"raw,omitempty"`
	// Unparsed marks a quote we could not normalize; the number fields are then
	// meaningless and must not be shown as a price.
	Unparsed bool `json:"unparsed,omitempty"`
}

// SiteProbeSample is one monitor's reading for one round.
type SiteProbeSample struct {
	ID           int64     `json:"id"`
	RunID        int64     `json:"run_id"`
	SiteID       int64     `json:"site_id"`
	MonitorID    string    `json:"monitor_id,omitempty"`
	MonitorName  string    `json:"monitor_name"`
	MonitorType  string    `json:"monitor_type,omitempty"`
	GroupName    string    `json:"group_name,omitempty"`
	RawModel     string    `json:"raw_model"`
	ObservedAt   time.Time `json:"observed_at"`
	Samples      int       `json:"samples"`
	UpCount      int       `json:"up_count"`
	Ratio        float64   `json:"ratio"`
	AvgPingMS    *int      `json:"avg_ping_ms,omitempty"`
	WeakEvidence bool      `json:"weak_evidence,omitempty"`
	Price        SiteProbePrice
}

// probeTimeFormat matches the datetime() text format used by the probe tables.
const probeTimeFormat = "2006-01-02 15:04:05"

func formatProbeTime(t time.Time) string {
	return t.UTC().Format(probeTimeFormat)
}

// CreateSiteProbeRun opens a collection round.
func (db *DB) CreateSiteProbeRun(siteID int64, sourceKind string, startedAt time.Time) (int64, error) {
	res, err := db.Exec(`INSERT INTO site_probe_runs (site_id, source_kind, started_at, status) VALUES (?, ?, ?, ?)`,
		siteID, sourceKind, formatProbeTime(startedAt), SiteProbeRunRunning)
	if err != nil {
		return 0, fmt.Errorf("site probe: create run: %w", err)
	}
	return res.LastInsertId()
}

// FinishSiteProbeRun closes a round. A failed round stores no samples, which is
// what makes collection failures fail-open: no data means no verdict.
func (db *DB) FinishSiteProbeRun(id int64, status string, monitorCount int, errMsg string) error {
	if _, err := db.Exec(`UPDATE site_probe_runs SET status=?, monitor_count=?, error=?, finished_at=datetime('now') WHERE id=?`,
		status, monitorCount, errMsg, id); err != nil {
		return fmt.Errorf("site probe: finish run: %w", err)
	}
	return nil
}

// InsertSiteProbeSamples appends one round's readings in a single transaction.
func (db *DB) InsertSiteProbeSamples(samples []SiteProbeSample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("site probe: insert samples begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT INTO site_probe_samples
		(run_id, site_id, monitor_id, monitor_name, monitor_type, group_name, raw_model, observed_at,
		 samples, up_count, ratio, avg_ping_ms, weak_evidence,
		 price_mode, price_currency, price_currency_symbol, price_input_per_million, price_output_per_million,
		 price_cache_read_per_million, price_per_request, price_group_ratio, price_raw, price_unparsed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("site probe: prepare sample insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, s := range samples {
		var ping any
		if s.AvgPingMS != nil {
			ping = *s.AvgPingMS
		}
		if _, err := stmt.Exec(s.RunID, s.SiteID, s.MonitorID, s.MonitorName, s.MonitorType, s.GroupName, s.RawModel,
			formatProbeTime(s.ObservedAt), s.Samples, s.UpCount, s.Ratio, ping, boolInt(s.WeakEvidence),
			s.Price.Mode, s.Price.Currency, s.Price.CurrencySymbol, s.Price.InputPerMillion, s.Price.OutputPerMillion,
			s.Price.CacheReadPerMillion, s.Price.PerRequest, s.Price.GroupRatio, s.Price.Raw, boolInt(s.Price.Unparsed)); err != nil {
			return fmt.Errorf("site probe: insert sample: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("site probe: insert samples commit: %w", err)
	}
	return nil
}

const siteProbeRunColumns = `id, site_id, source_kind, started_at, finished_at, status, monitor_count, error`

func scanSiteProbeRun(row interface {
	Scan(dest ...any) error
}) (SiteProbeRun, error) {
	var r SiteProbeRun
	var monitorCount sql.NullInt64
	if err := row.Scan(&r.ID, &r.SiteID, &r.SourceKind, scanTime(&r.StartedAt), scanNullTime(&r.FinishedAt),
		&r.Status, &monitorCount, &r.Error); err != nil {
		return r, err
	}
	if monitorCount.Valid {
		r.MonitorCount = int(monitorCount.Int64)
	}
	return r, nil
}

// ListSiteProbeRuns returns a site's rounds, newest first.
func (db *DB) ListSiteProbeRuns(siteID int64, limit int) ([]SiteProbeRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := db.Query(`SELECT `+siteProbeRunColumns+` FROM site_probe_runs WHERE site_id = ? ORDER BY id DESC LIMIT ?`, siteID, limit)
	if err != nil {
		return nil, fmt.Errorf("site probe: list runs: %w", err)
	}
	defer rows.Close()
	var out []SiteProbeRun
	for rows.Next() {
		run, err := scanSiteProbeRun(rows)
		if err != nil {
			return nil, fmt.Errorf("site probe: scan run: %w", err)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// LatestSiteProbeRun returns the newest round of any status.
func (db *DB) LatestSiteProbeRun(siteID int64) (*SiteProbeRun, error) {
	row := db.QueryRow(`SELECT `+siteProbeRunColumns+` FROM site_probe_runs WHERE site_id = ? ORDER BY id DESC LIMIT 1`, siteID)
	run, err := scanSiteProbeRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("site probe: latest run: %w", err)
	}
	return &run, nil
}

const siteProbeSampleColumns = `id, run_id, site_id, monitor_id, monitor_name, monitor_type, group_name, raw_model, observed_at,
	samples, up_count, ratio, avg_ping_ms, weak_evidence,
	price_mode, price_currency, price_currency_symbol, price_input_per_million, price_output_per_million,
	price_cache_read_per_million, price_per_request, price_group_ratio, price_raw, price_unparsed`

func scanSiteProbeSample(rows *sql.Rows) (SiteProbeSample, error) {
	var s SiteProbeSample
	var ping sql.NullInt64
	var weak, unparsed sql.NullInt64
	if err := rows.Scan(&s.ID, &s.RunID, &s.SiteID, &s.MonitorID, &s.MonitorName, &s.MonitorType, &s.GroupName, &s.RawModel,
		scanTime(&s.ObservedAt), &s.Samples, &s.UpCount, &s.Ratio, &ping, &weak,
		&s.Price.Mode, &s.Price.Currency, &s.Price.CurrencySymbol, &s.Price.InputPerMillion, &s.Price.OutputPerMillion,
		&s.Price.CacheReadPerMillion, &s.Price.PerRequest, &s.Price.GroupRatio, &s.Price.Raw, &unparsed); err != nil {
		return s, err
	}
	if ping.Valid {
		v := int(ping.Int64)
		s.AvgPingMS = &v
	}
	s.WeakEvidence = weak.Int64 != 0
	s.Price.Unparsed = unparsed.Int64 != 0
	return s, nil
}

// ListSiteProbeSamples returns every sample of one round.
func (db *DB) ListSiteProbeSamples(runID int64) ([]SiteProbeSample, error) {
	rows, err := db.Query(`SELECT `+siteProbeSampleColumns+` FROM site_probe_samples WHERE run_id = ? ORDER BY raw_model`, runID)
	if err != nil {
		return nil, fmt.Errorf("site probe: list samples: %w", err)
	}
	defer rows.Close()
	var out []SiteProbeSample
	for rows.Next() {
		s, err := scanSiteProbeSample(rows)
		if err != nil {
			return nil, fmt.Errorf("site probe: scan sample: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListRecentSiteProbeSamples returns the samples of a site's most recent `rounds`
// successful rounds, newest round first. Verdicts are computed from these rows
// rather than from a stored counter, so re-running the evaluator is idempotent.
func (db *DB) ListRecentSiteProbeSamples(siteID int64, rounds int) ([]SiteProbeSample, error) {
	if rounds <= 0 {
		rounds = 3
	}
	rows, err := db.Query(`SELECT `+siteProbeSampleColumns+` FROM site_probe_samples
		WHERE site_id = ? AND run_id IN (
			SELECT id FROM site_probe_runs WHERE site_id = ? AND status = ? ORDER BY id DESC LIMIT ?
		) ORDER BY run_id DESC, raw_model`, siteID, siteID, SiteProbeRunOK, rounds)
	if err != nil {
		return nil, fmt.Errorf("site probe: recent samples: %w", err)
	}
	defer rows.Close()
	var out []SiteProbeSample
	for rows.Next() {
		s, err := scanSiteProbeSample(rows)
		if err != nil {
			return nil, fmt.Errorf("site probe: scan recent sample: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListRecentSamplesBySite returns the last `rounds` successful rounds of every
// monitored model on every site, keyed by site id — the model-page tool needs
// "this model across all sites" and one query per site would be a fan-out.
// The window partitions by (site, model): partitioning by site alone would let
// one round's other monitors push a model's older rounds out of the window,
// which is exactly the history the consecutive-rounds verdict needs.
func (db *DB) ListRecentSamplesBySite(rounds int) (map[int64][]SiteProbeSample, error) {
	if rounds <= 0 {
		rounds = 3
	}
	rows, err := db.Query(`SELECT `+siteProbeSampleColumns+` FROM (
			SELECT s.id, s.run_id, s.site_id, s.monitor_id, s.monitor_name, s.monitor_type, s.group_name, s.raw_model, s.observed_at,
				s.samples, s.up_count, s.ratio, s.avg_ping_ms, s.weak_evidence,
				s.price_mode, s.price_currency, s.price_currency_symbol, s.price_input_per_million, s.price_output_per_million,
				s.price_cache_read_per_million, s.price_per_request, s.price_group_ratio, s.price_raw, s.price_unparsed,
				ROW_NUMBER() OVER (PARTITION BY s.site_id, s.raw_model ORDER BY s.run_id DESC) AS rn
			FROM site_probe_samples s
			JOIN site_probe_runs r ON r.id = s.run_id AND r.status = ?
		) WHERE rn <= ? ORDER BY site_id, raw_model, run_id DESC`, SiteProbeRunOK, rounds)
	if err != nil {
		return nil, fmt.Errorf("site probe: recent samples by site: %w", err)
	}
	defer rows.Close()
	out := make(map[int64][]SiteProbeSample)
	for rows.Next() {
		s, err := scanSiteProbeSample(rows)
		if err != nil {
			return nil, fmt.Errorf("site probe: scan sample: %w", err)
		}
		out[s.SiteID] = append(out[s.SiteID], s)
	}
	return out, rows.Err()
}

// ListProbeSites returns every site, used by the catalog importer to match a
// directory entry onto a site we already have.
func (db *DB) ListProbeSites() ([]domain.Site, error) {
	return db.Site.List()
}

// ListUnusedSites returns sites nothing routes through: no channels, and no
// credentials either (a credential with no channel is still operator data, so a
// site holding one is not abandoned).
func (db *DB) ListUnusedSites() ([]domain.Site, error) {
	sites, err := db.Site.List()
	if err != nil {
		return nil, err
	}
	out := make([]domain.Site, 0, len(sites))
	for _, site := range sites {
		var channels, credentials int
		if err := db.QueryRow(`SELECT COUNT(*) FROM channels WHERE site_id = ?`, site.ID).Scan(&channels); err != nil {
			return nil, fmt.Errorf("site probe: count channels: %w", err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM credentials WHERE site_id = ?`, site.ID).Scan(&credentials); err != nil {
			return nil, fmt.Errorf("site probe: count credentials: %w", err)
		}
		if channels == 0 && credentials == 0 {
			out = append(out, site)
		}
	}
	return out, nil
}

// DeleteUnusedSite removes a site nothing routes through. The check is repeated
// inside the statement rather than trusted from the caller: a channel created
// between the listing and the delete would otherwise be silently cascaded away
// (the site_id foreign keys are ON DELETE CASCADE).
func (db *DB) DeleteUnusedSite(siteID int64) (bool, error) {
	res, err := db.Exec(`DELETE FROM sites WHERE id = ?
		AND NOT EXISTS (SELECT 1 FROM channels WHERE site_id = ?)
		AND NOT EXISTS (SELECT 1 FROM credentials WHERE site_id = ?)`, siteID, siteID, siteID)
	if err != nil {
		return false, fmt.Errorf("site probe: delete unused site: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, nil
	}
	if affected > 0 {
		db.Site.invalidate(siteID)
	}
	return affected > 0, nil
}

// SiteProbeExternal is one third-party reading: an availability aggregate a
// monitoring directory publishes for a (site, model) pair.
//
// It is deliberately NOT first-hand evidence — we did not measure it, and
// neither did the site — so it is labelled as third-party everywhere it shows
// up and is only consulted when neither the site nor our own relay has anything
// to say about that pair.
type SiteProbeExternal struct {
	Source           string  `json:"source"`
	SiteID           int64   `json:"site_id"`
	SiteName         string  `json:"site_name,omitempty"`
	SiteHost         string  `json:"site_host,omitempty"`
	RawModel         string  `json:"raw_model"`
	GroupName        string  `json:"group_name,omitempty"`
	Ratio            float64 `json:"ratio"`
	AvgLatencyMS     int     `json:"avg_latency_ms,omitempty"`
	FirstTokenMS     int     `json:"first_token_ms,omitempty"`
	TokensPerSecond  float64 `json:"tokens_per_second,omitempty"`
	ServiceState     string  `json:"service_state,omitempty"`
	AcquisitionState string  `json:"acquisition_state,omitempty"`
	ObservedAt       string  `json:"observed_at,omitempty"`
}

// ReplaceSiteProbeExternal swaps the whole third-party snapshot in one
// transaction. A snapshot is not a history: the readings that matter (ours and
// the sites') live in the probe tables, and keeping stale third-party rows
// around would let a directory that stopped reporting keep influencing verdicts.
func (db *DB) ReplaceSiteProbeExternal(rows []SiteProbeExternal) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("site probe: external snapshot begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM site_probe_external`); err != nil {
		return fmt.Errorf("site probe: clear external snapshot: %w", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO site_probe_external
		(source, site_id, site_name, site_host, raw_model, group_name, ratio, avg_latency_ms,
		 first_token_ms, tokens_per_second, service_state, acquisition_state, observed_at, collected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`)
	if err != nil {
		return fmt.Errorf("site probe: prepare external insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, row := range rows {
		if _, err := stmt.Exec(row.Source, row.SiteID, row.SiteName, row.SiteHost, row.RawModel, row.GroupName,
			row.Ratio, row.AvgLatencyMS, row.FirstTokenMS, row.TokensPerSecond, row.ServiceState,
			row.AcquisitionState, row.ObservedAt); err != nil {
			return fmt.Errorf("site probe: insert external reading: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("site probe: external snapshot commit: %w", err)
	}
	return nil
}

// ListSiteProbeExternal returns the current third-party snapshot.
func (db *DB) ListSiteProbeExternal() ([]SiteProbeExternal, error) {
	rows, err := db.Query(`SELECT source, site_id, site_name, site_host, raw_model, group_name, ratio,
			avg_latency_ms, first_token_ms, tokens_per_second, service_state, acquisition_state, observed_at
		FROM site_probe_external`)
	if err != nil {
		return nil, fmt.Errorf("site probe: list external: %w", err)
	}
	defer rows.Close()
	var out []SiteProbeExternal
	for rows.Next() {
		var row SiteProbeExternal
		if err := rows.Scan(&row.Source, &row.SiteID, &row.SiteName, &row.SiteHost, &row.RawModel, &row.GroupName,
			&row.Ratio, &row.AvgLatencyMS, &row.FirstTokenMS, &row.TokensPerSecond, &row.ServiceState,
			&row.AcquisitionState, &row.ObservedAt); err != nil {
			return nil, fmt.Errorf("site probe: scan external: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// MarkSiteProbeOutcome records the result of a round on the site row so the
// admin UI can show "last collected / last error" without joining the run table.
// Only the one row is evicted from the site cache: clearing the whole cache on
// every round would thrash the relay hot path.
func (db *DB) MarkSiteProbeOutcome(siteID int64, at time.Time, errMsg string) error {
	if _, err := db.Exec(`UPDATE sites SET probe_last_run_at = ?, probe_last_error = ? WHERE id = ?`,
		formatProbeTime(at), errMsg, siteID); err != nil {
		return fmt.Errorf("site probe: mark outcome: %w", err)
	}
	db.Site.invalidate(siteID)
	return nil
}

// UpdateSiteProbeSource writes the probe-source fields of one site. The site
// cache is dropped here rather than by the caller: sites are cached for the
// relay hot path, and a forgotten invalidation would serve a stale source URL
// to the collector (or, worse, a hole where one used to be).
func (db *DB) UpdateSiteProbeSource(siteID int64, kind, url, config string, enabled bool) error {
	return db.UpdateSiteProbeSourceWithAuto(siteID, kind, url, config, enabled, false)
}

// UpdateSiteProbeSourceWithAuto additionally writes the auto flag. An explicit
// source (a hand-typed URL) always turns auto off — the two are mutually
// exclusive by definition, and letting both stick would make "which source ran"
// ambiguous.
func (db *DB) UpdateSiteProbeSourceWithAuto(siteID int64, kind, url, config string, enabled, auto bool) error {
	if strings.TrimSpace(url) != "" {
		auto = false
	}
	if _, err := db.Exec(`UPDATE sites SET probe_source_kind=?, probe_source_url=?, probe_auto=?, probe_source_config=?, probe_source_enabled=?, updated_at=datetime('now') WHERE id=?`,
		strings.TrimSpace(kind), strings.TrimSpace(url), boolInt(auto), config, boolInt(enabled), siteID); err != nil {
		return fmt.Errorf("site probe: update source: %w", err)
	}
	db.Site.invalidate(siteID)
	return nil
}

// PruneSiteProbe drops rounds older than the retention window, together with
// their samples, and reports how many rows went.
//
// Samples are deleted first: the two tables share no foreign key (samples carry
// run_id and site_id as plain columns), so deleting the runs alone would leave
// their samples behind — counted forever by the model page and impossible to
// attribute to a round. Retention matters here more than anywhere else in the
// schema: one round is one row per monitored model per site, so a fleet-wide
// install writes hundreds of rows every fifteen minutes.
func (db *DB) PruneSiteProbe(retentionDays int) (runs int64, samples int64, err error) {
	if retentionDays <= 0 {
		return 0, 0, nil
	}
	cutoff := formatProbeTime(time.Now().UTC().AddDate(0, 0, -retentionDays))
	tx, err := db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("site probe: prune begin: %w", err)
	}
	//nolint:errcheck
	defer tx.Rollback()
	sampleResult, err := tx.Exec(`DELETE FROM site_probe_samples WHERE run_id IN (SELECT id FROM site_probe_runs WHERE started_at < ?)`, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("site probe: prune samples: %w", err)
	}
	samples, err = sampleResult.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("site probe: prune samples rows: %w", err)
	}
	runResult, err := tx.Exec(`DELETE FROM site_probe_runs WHERE started_at < ?`, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("site probe: prune runs: %w", err)
	}
	runs, err = runResult.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("site probe: prune runs rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("site probe: prune commit: %w", err)
	}
	return runs, samples, nil
}
