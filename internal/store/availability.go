package store

import (
	"fmt"
	"strings"
	"time"
)

type AvailabilityBucket struct {
	At      time.Time `json:"at"`
	Samples int       `json:"samples"`
	OK      int       `json:"ok"`
	Failed  int       `json:"failed"`
	Ignored int       `json:"ignored"`
}
type AvailabilitySeries struct {
	Source  string               `json:"source"`
	Scope   string               `json:"scope"`
	Samples int                  `json:"samples"`
	OK      int                  `json:"ok"`
	Failed  int                  `json:"failed"`
	Ignored int                  `json:"ignored"`
	Ratio   *float64             `json:"ratio"`
	LastAt  *time.Time           `json:"last_at,omitempty"`
	Stale   bool                 `json:"stale"`
	Buckets []AvailabilityBucket `json:"buckets"`
	Paths   []string             `json:"paths"`
}

func NewAvailabilitySeries(source, scope string, since, until time.Time) *AvailabilitySeries {
	out := &AvailabilitySeries{Source: source, Scope: scope, Buckets: []AvailabilityBucket{}, Paths: []string{}}
	width := until.Sub(since) / 24
	for i := 0; i < 24; i++ {
		out.Buckets = append(out.Buckets, AvailabilityBucket{At: since.Add(time.Duration(i) * width)})
	}
	return out
}
func (s *AvailabilitySeries) Observe(at time.Time, outcome, path string, until time.Time) {
	if len(s.Buckets) == 0 || at.Before(s.Buckets[0].At) || !at.Before(until) {
		return
	}
	index := int(at.Sub(s.Buckets[0].At) / (until.Sub(s.Buckets[0].At) / time.Duration(len(s.Buckets))))
	if index < 0 || index >= len(s.Buckets) {
		return
	}
	b := &s.Buckets[index]
	if outcome == "canceled" || outcome == "ignored" {
		b.Ignored++
		s.Ignored++
	} else {
		b.Samples++
		s.Samples++
		if outcome == "ok" {
			b.OK++
			s.OK++
		} else {
			b.Failed++
			s.Failed++
		}
		value := float64(s.OK) / float64(s.Samples)
		s.Ratio = &value
	}
	if s.LastAt == nil || at.After(*s.LastAt) {
		v := at
		s.LastAt = &v
	}
	if path != "" {
		found := false
		for _, p := range s.Paths {
			if path == p {
				found = true
			}
		}
		if !found {
			s.Paths = append(s.Paths, path)
		}
	}
}

// SQL aggregates before returning data: read size follows series × time buckets,
// not the number of requests in a busy deployment.
func (db *DB) AvailabilityTraffic(since, until time.Time, routeIDs []int64, channelID int64) (map[string]*AvailabilitySeries, map[string]*AvailabilitySeries, error) {
	where, args := []string{"created_at>=?", "created_at<?", "request_kind='traffic'"}, []any{sqliteUTC(since), sqliteUTC(until)}
	if len(routeIDs) > 0 {
		marks := []string{}
		for _, id := range routeIDs {
			marks = append(marks, "?")
			args = append(args, id)
		}
		where = append(where, "route_id IN ("+strings.Join(marks, ",")+")")
	}
	if channelID > 0 {
		where = append(where, "channel_id=?")
		args = append(args, channelID)
	}
	seconds := int64(until.Sub(since).Seconds() / 24)
	query := fmt.Sprintf(`SELECT member_id,channel_id,upstream_model,path,
 CAST((CAST(strftime('%%s',created_at) AS INTEGER)-%d)/%d AS INTEGER),
 SUM(CASE WHEN completion_outcome IN ('canceled','pending') THEN 0 ELSE 1 END),
 SUM(CASE WHEN completion_outcome='ok' OR (completion_outcome='' AND status>=200 AND status<300) THEN 1 ELSE 0 END),
 SUM(CASE WHEN completion_outcome IN ('canceled','pending') THEN 1 ELSE 0 END),MAX(created_at)
 FROM proxy_logs WHERE %s AND upstream_model<>'' GROUP BY member_id,channel_id,upstream_model,path,5`, since.Unix(), seconds, strings.Join(where, " AND "))
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	members := map[string]*AvailabilitySeries{}
	channels := map[string]*AvailabilitySeries{}
	for rows.Next() {
		var memberID, ch int64
		var model, path string
		var index, n, ok, ignored int
		var last time.Time
		if err := rows.Scan(&memberID, &ch, &model, &path, &index, &n, &ok, &ignored, scanTime(&last)); err != nil {
			return nil, nil, err
		}
		key := TrafficKey(ch, model)
		series := channels[key]
		if series == nil {
			series = NewAvailabilitySeries("traffic", "channel_model", since, until)
			channels[key] = series
		}
		addAvailabilityBucket(series, index, n, ok, ignored, last, path)
		if memberID > 0 {
			series := members[MemberAvailabilityKey(memberID, model)]
			if series == nil {
				series = NewAvailabilitySeries("traffic", "member", since, until)
				members[MemberAvailabilityKey(memberID, model)] = series
			}
			addAvailabilityBucket(series, index, n, ok, ignored, last, path)
		}
	}
	return members, channels, rows.Err()
}
func addAvailabilityBucket(s *AvailabilitySeries, index, n, ok, ignored int, last time.Time, path string) {
	if index < 0 || index >= len(s.Buckets) {
		return
	}
	b := &s.Buckets[index]
	b.Samples += n
	b.OK += ok
	b.Failed += n - ok
	b.Ignored += ignored
	s.Samples += n
	s.OK += ok
	s.Failed += n - ok
	s.Ignored += ignored
	if s.Samples > 0 {
		r := float64(s.OK) / float64(s.Samples)
		s.Ratio = &r
	}
	if s.LastAt == nil || last.After(*s.LastAt) {
		v := last
		s.LastAt = &v
	}
	if path != "" {
		for _, p := range s.Paths {
			if p == path {
				return
			}
		}
		s.Paths = append(s.Paths, path)
	}
}
func (db *DB) AvailabilityProbes(since, until time.Time, channelID int64) (map[string]*AvailabilitySeries, map[string]*AvailabilitySeries, error) {
	query := `SELECT member_id,channel_id,upstream_model,model,ok,probed_at FROM probe_results WHERE probed_at>=? AND probed_at<?`
	args := []any{sqliteUTC(since), sqliteUTC(until)}
	if channelID > 0 {
		query += ` AND channel_id=?`
		args = append(args, channelID)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	members := map[string]*AvailabilitySeries{}
	channels := map[string]*AvailabilitySeries{}
	for rows.Next() {
		var id, ch int64
		var real, model string
		var ok bool
		var at time.Time
		if err := rows.Scan(&id, &ch, &real, &model, &ok, scanTime(&at)); err != nil {
			return nil, nil, err
		}
		outcome := "failed"
		if ok {
			outcome = "ok"
		}
		if id > 0 && real != "" {
			s := members[MemberAvailabilityKey(id, real)]
			if s == nil {
				s = NewAvailabilitySeries("probe", "member", since, until)
				members[MemberAvailabilityKey(id, real)] = s
			}
			s.Observe(at, outcome, "chat/completions", until)
		}
		// Legacy probes do not record the actual upstream name. Never guess an alias.
		if real != "" {
			key := TrafficKey(ch, real)
			s := channels[key]
			if s == nil {
				s = NewAvailabilitySeries("probe", "channel_model", since, until)
				channels[key] = s
			}
			s.Observe(at, outcome, "chat/completions", until)
		}
	}
	return members, channels, rows.Err()
}
func (db *DB) AvailabilityRoutes(since, until time.Time, routeIDs []int64, group string) (map[int64]*AvailabilitySeries, error) {
	where, args := []string{"created_at>=?", "created_at<?"}, []any{sqliteUTC(since), sqliteUTC(until)}
	if len(routeIDs) > 0 {
		marks := []string{}
		for _, id := range routeIDs {
			marks = append(marks, "?")
			args = append(args, id)
		}
		where = append(where, "route_id IN ("+strings.Join(marks, ",")+")")
	}
	if group != "" {
		where = append(where, "group_name=?")
		args = append(args, group)
	}
	seconds := int64(until.Sub(since).Seconds() / 24)
	q := fmt.Sprintf(`SELECT route_id,CAST((CAST(strftime('%%s',created_at) AS INTEGER)-%d)/%d AS INTEGER),
 SUM(CASE WHEN outcome='canceled' THEN 0 ELSE 1 END),SUM(CASE WHEN outcome='ok' THEN 1 ELSE 0 END),
 SUM(CASE WHEN outcome='canceled' THEN 1 ELSE 0 END),MAX(created_at),path FROM route_request_outcomes WHERE %s GROUP BY route_id,2,path`, since.Unix(), seconds, strings.Join(where, " AND "))
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*AvailabilitySeries{}
	for rows.Next() {
		var id int64
		var index, n, ok, ignored int
		var at time.Time
		var path string
		if err := rows.Scan(&id, &index, &n, &ok, &ignored, scanTime(&at), &path); err != nil {
			return nil, err
		}
		s := out[id]
		if s == nil {
			s = NewAvailabilitySeries("requests", "route", since, until)
			out[id] = s
		}
		addAvailabilityBucket(s, index, n, ok, ignored, at, path)
	}
	return out, rows.Err()
}

func MemberAvailabilityKey(id int64, model string) string { return fmt.Sprintf("%d|%s", id, model) }
