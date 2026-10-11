package store

import (
	"fmt"
	"time"
)

type SiteHeartbeat struct {
	SiteID                                    int64
	SourceKey, MonitorID, RawModel, GroupName string
	At                                        time.Time
	Status                                    int
	PingMS                                    *int
}

func (db *DB) InsertSiteHeartbeats(beats []SiteHeartbeat) error {
	if len(beats) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO site_probe_heartbeats(site_id,source_key,monitor_id,raw_model,group_name,probed_at,status,ping_ms)
 VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(site_id,source_key,monitor_id,probed_at) DO UPDATE SET status=excluded.status,ping_ms=excluded.ping_ms`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, beat := range beats {
		var ping any
		if beat.PingMS != nil {
			ping = *beat.PingMS
		}
		if _, err := stmt.Exec(beat.SiteID, beat.SourceKey, beat.MonitorID, beat.RawModel, beat.GroupName, formatProbeTime(beat.At), beat.Status, ping); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Only the last successful snapshot supplies the catalogue. Failed collections
// retain that snapshot; models removed from a new successful one disappear.
func (db *DB) LatestSiteObservations() (map[int64][]SiteProbeSample, error) {
	rows, err := db.Query(`SELECT ` + siteProbeSampleColumns + ` FROM site_probe_samples WHERE run_id IN
 (SELECT MAX(id) FROM site_probe_runs WHERE status='ok' GROUP BY site_id) ORDER BY site_id,raw_model,group_name,monitor_id`)
	if err != nil {
		return nil, fmt.Errorf("latest site observations: %w", err)
	}
	defer rows.Close()
	out := map[int64][]SiteProbeSample{}
	for rows.Next() {
		s, err := scanSiteProbeSample(rows)
		if err != nil {
			return nil, err
		}
		out[s.SiteID] = append(out[s.SiteID], s)
	}
	return out, rows.Err()
}

func (db *DB) SiteHeartbeats(siteID int64, sourceKey string, since time.Time) ([]SiteHeartbeat, error) {
	rows, err := db.Query(`SELECT monitor_id,raw_model,group_name,probed_at,status,ping_ms FROM site_probe_heartbeats
 WHERE site_id=? AND source_key=? AND probed_at>=? ORDER BY probed_at`, siteID, sourceKey, formatProbeTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SiteHeartbeat{}
	for rows.Next() {
		var b SiteHeartbeat
		var ping *int
		b.SiteID = siteID
		b.SourceKey = sourceKey
		if err := rows.Scan(&b.MonitorID, &b.RawModel, &b.GroupName, scanTime(&b.At), &b.Status, &ping); err != nil {
			return nil, err
		}
		b.PingMS = ping
		out = append(out, b)
	}
	return out, rows.Err()
}
