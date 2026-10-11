package store

import (
	"fmt"
	"time"
)

func requestKind(kind string) string {
	if kind == "" {
		return "traffic"
	}
	return kind
}

type RouteRequestOutcome struct {
	RequestID                                      string
	RouteID, MemberID, ChannelID                   int64
	Model, UpstreamModel, GroupName, Path, Outcome string
	Status                                         int
}

func (db *DB) RecordRouteRequest(outcome RouteRequestOutcome) error {
	if outcome.RouteID <= 0 {
		return nil
	}
	_, err := db.Exec(`INSERT INTO route_request_outcomes
        (request_id, route_id, member_id, channel_id, model, upstream_model, group_name, path, outcome, status)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, outcome.RequestID, outcome.RouteID, outcome.MemberID,
		outcome.ChannelID, outcome.Model, outcome.UpstreamModel, outcome.GroupName, outcome.Path, outcome.Outcome, outcome.Status)
	if err != nil {
		return fmt.Errorf("record route request: %w", err)
	}
	return nil
}

func (db *DB) PruneRouteOutcomes(retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}
	_, err := db.Exec(`DELETE FROM route_request_outcomes WHERE created_at < ?`, sqliteUTC(time.Now().AddDate(0, 0, -retentionDays)))
	return err
}
