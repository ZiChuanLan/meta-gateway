package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrModelChangeConflict = errors.New("model change selection is stale or incompatible")
var ErrModelChangeInvalid = errors.New("invalid model change request")

func recordModelChanges(tx sqlExecutor, input ReconcileInput, old []string) error {
	var baseline int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM discovery_snapshots WHERE channel_id=?`, input.ChannelID).Scan(&baseline); err != nil {
		return err
	}
	before, after := map[string]bool{}, map[string]bool{}
	for _, m := range old {
		before[m] = true
	}
	for _, m := range input.Models {
		after[m] = true
	}
	additions := []string{}
	for m := range after {
		if !before[m] {
			additions = append(additions, m)
		}
	}
	sort.Strings(additions)
	candidates, _ := json.Marshal(additions)
	if baseline > 0 || len(old) > 0 {
		names := []string{}
		for m := range before {
			if !after[m] {
				names = append(names, m)
			}
		}
		names = append(names, additions...)
		sort.Strings(names)
		for _, m := range names {
			kind := "removed"
			if after[m] {
				kind = "added"
			}
			if _, err := tx.Exec(`UPDATE model_changes SET status='resolved' WHERE channel_id=? AND model_name=? AND status='pending'`, input.ChannelID, m); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO model_changes(channel_id,model_name,kind,detected_at,candidates_json) VALUES(?,?,?,?,?)`, input.ChannelID, m, kind, input.CheckedAt.UTC().Format(time.RFC3339Nano), string(candidates)); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(`INSERT INTO discovery_snapshots(channel_id,revision) VALUES(?,1) ON CONFLICT(channel_id) DO UPDATE SET revision=revision+1`, input.ChannelID)
	return err
}

type ModelChangeMember struct {
	MemberID      int64  `json:"member_id"`
	RouteID       int64  `json:"route_id"`
	RouteName     string `json:"route_name"`
	ModelPattern  string `json:"model_pattern"`
	ChannelID     int64  `json:"channel_id"`
	UpstreamModel string `json:"upstream_model"`
	GroupName     string `json:"group_name"`
	mapping       string
	routeMapping  string
}
type ModelChange struct {
	ID          int64               `json:"id"`
	ChannelID   int64               `json:"channel_id"`
	ChannelName string              `json:"channel_name"`
	ModelName   string              `json:"model_name"`
	Kind        string              `json:"kind"`
	Status      string              `json:"status"`
	DetectedAt  string              `json:"detected_at"`
	Candidates  []string            `json:"candidates"`
	Members     []ModelChangeMember `json:"members"`
}
type ModelChanges struct {
	Items   []ModelChange `json:"items"`
	Summary struct {
		Added          int `json:"added"`
		Removed        int `json:"removed"`
		AffectedRoutes int `json:"affected_routes"`
	} `json:"summary"`
}

// A wildcard without a constant rewrite serves many models. Replacing that
// entire binding for one removed model would redirect unrelated traffic.
func changeMembers(q sqlExecutor) ([]ModelChangeMember, error) {
	rows, err := q.Query(`SELECT m.id,m.route_id,r.model_pattern,m.channel_id,m.group_name,m.mapping_json,r.mapping_json FROM route_members m JOIN routes r ON r.id=m.route_id ORDER BY m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelChangeMember{}
	for rows.Next() {
		var m ModelChangeMember
		if err := rows.Scan(&m.MemberID, &m.RouteID, &m.ModelPattern, &m.ChannelID, &m.GroupName, &m.mapping, &m.routeMapping); err != nil {
			return nil, err
		}
		m.RouteName = m.ModelPattern
		mapping := strings.TrimSpace(m.mapping)
		if mapping == "" {
			mapping = m.routeMapping
		}
		var alias struct {
			Real string `json:"real"`
		}
		_ = json.Unmarshal([]byte(mapping), &alias)
		m.UpstreamModel = alias.Real
		if m.UpstreamModel == "" && !strings.ContainsAny(m.ModelPattern, "*?[") {
			m.UpstreamModel = m.ModelPattern
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func listModelChanges(q sqlExecutor) (ModelChanges, error) {
	out := ModelChanges{Items: []ModelChange{}}
	rows, err := q.Query(`SELECT x.id,x.channel_id,c.name,x.model_name,x.kind,x.status,x.detected_at,x.candidates_json FROM model_changes x JOIN channels c ON c.id=x.channel_id ORDER BY x.id DESC`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var x ModelChange
		var candidates string
		if err := rows.Scan(&x.ID, &x.ChannelID, &x.ChannelName, &x.ModelName, &x.Kind, &x.Status, &x.DetectedAt, &candidates); err != nil {
			rows.Close()
			return out, err
		}
		x.Candidates = []string{}
		x.Members = []ModelChangeMember{}
		if err := json.Unmarshal([]byte(candidates), &x.Candidates); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	members, err := changeMembers(q)
	if err != nil {
		return out, err
	}
	routes := map[int64]bool{}
	for i := range out.Items {
		x := &out.Items[i]
		if x.Kind == "removed" {
			for _, m := range members {
				if m.ChannelID != x.ChannelID {
					continue
				}
				if m.UpstreamModel == x.ModelName || (m.UpstreamModel == "" && matchModelPattern(m.ModelPattern, x.ModelName)) {
					m.UpstreamModel = x.ModelName
					x.Members = append(x.Members, m)
					if x.Status == "pending" {
						routes[m.RouteID] = true
					}
				}
			}
		}
		if x.Status == "pending" {
			if x.Kind == "added" {
				out.Summary.Added++
			} else {
				out.Summary.Removed++
			}
		}
	}
	out.Summary.AffectedRoutes = len(routes)
	return out, nil
}
func (s *DB) ModelChanges() (ModelChanges, error) {
	tx, err := s.Begin()
	if err != nil {
		return ModelChanges{}, err
	}
	defer tx.Rollback()
	return listModelChanges(tx)
}
func validChangeIDs(ids []int64) bool {
	if len(ids) == 0 {
		return false
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (s *DB) IgnoreModelChanges(ids []int64) error {
	if !validChangeIDs(ids) {
		return ErrModelChangeInvalid
	}
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		res, err := tx.Exec(`UPDATE model_changes SET status='ignored' WHERE id=? AND status='pending'`, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrModelChangeConflict
		}
	}
	return tx.Commit()
}

type ModelChangeRequest struct {
	ChangeIDs       []int64 `json:"change_ids"`
	MemberIDs       []int64 `json:"member_ids"`
	TargetChannelID int64   `json:"target_channel_id"`
	TargetModel     string  `json:"target_model"`
	PreviewToken    string  `json:"preview_token,omitempty"`
}
type ModelChangePreviewItem struct {
	MemberID        int64  `json:"member_id"`
	RouteID         int64  `json:"route_id"`
	RouteName       string `json:"route_name"`
	ModelPattern    string `json:"model_pattern"`
	SourceChannelID int64  `json:"source_channel_id"`
	SourceModel     string `json:"source_model"`
	TargetChannelID int64  `json:"target_channel_id"`
	TargetModel     string `json:"target_model"`
}
type ModelChangePreview struct {
	Items        []ModelChangePreviewItem `json:"items"`
	PreviewToken string                   `json:"preview_token"`
}

func previewModelChanges(q sqlExecutor, req ModelChangeRequest) (ModelChangePreview, error) {
	out := ModelChangePreview{Items: []ModelChangePreviewItem{}}
	if !validChangeIDs(req.ChangeIDs) || !validChangeIDs(req.MemberIDs) || req.TargetChannelID <= 0 || strings.TrimSpace(req.TargetModel) == "" {
		return out, ErrModelChangeInvalid
	}
	var revision int64
	var credential int64
	var site int64
	err := q.QueryRow(`SELECT s.revision,COALESCE(c.credential_id,0),COALESCE(c.site_id,0) FROM channels c JOIN discovery_snapshots s ON s.channel_id=c.id JOIN discovered_models d ON d.channel_id=c.id AND d.model_name=? WHERE c.id=? AND c.status='enabled'`, req.TargetModel, req.TargetChannelID).Scan(&revision, &credential, &site)
	if err != nil {
		return out, ErrModelChangeConflict
	}
	// Credentials are channel-scoped, not member-scoped. Never move onto a
	// channel whose explicit credential belongs to a different site or is off.
	if credential != 0 {
		var usable int
		if err := q.QueryRow(`SELECT COUNT(*) FROM credentials WHERE id=? AND status='enabled' AND site_id=?`, credential, site).Scan(&usable); err != nil {
			return out, err
		}
		if usable != 1 {
			return out, ErrModelChangeConflict
		}
	}
	changes, err := listModelChanges(q)
	if err != nil {
		return out, err
	}
	selected := map[int64]bool{}
	for _, id := range req.ChangeIDs {
		selected[id] = true
	}
	eligible := map[int64]ModelChangeMember{}
	var source int64
	found := 0
	for _, x := range changes.Items {
		if !selected[x.ID] {
			continue
		}
		found++
		if x.Status != "pending" || x.Kind != "removed" || (source != 0 && source != x.ChannelID) {
			return out, ErrModelChangeConflict
		}
		source = x.ChannelID
		for _, m := range x.Members {
			eligible[m.MemberID] = m
		}
	}
	if found != len(selected) {
		return out, ErrModelChangeConflict
	}
	all, err := changeMembers(q)
	if err != nil {
		return out, err
	}
	ids := append([]int64(nil), req.MemberIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	// Hash all route/member state and channel state as well as the snapshot
	// revision: even an equivalent-looking replacement after a refresh is stale.
	fingerprint := []any{revision, credential, site, req.TargetChannelID, req.TargetModel}
	changeIDs := append([]int64(nil), req.ChangeIDs...)
	sort.Slice(changeIDs, func(i, j int) bool { return changeIDs[i] < changeIDs[j] })
	fingerprint = append(fingerprint, changeIDs)
	collisions := map[string]bool{}
	for _, id := range ids {
		m, ok := eligible[id]
		if !ok {
			return out, ErrModelChangeConflict
		}
		// A wildcard impact is visible, but changing an unconstrained wildcard
		// would also redirect every unaffected model. Require a constant rewrite.
		if strings.ContainsAny(m.ModelPattern, "*?[") {
			mapping := strings.TrimSpace(m.mapping)
			if mapping == "" {
				mapping = m.routeMapping
			}
			var alias struct {
				Real string `json:"real"`
			}
			_ = json.Unmarshal([]byte(mapping), &alias)
			if alias.Real == "" {
				return out, ErrModelChangeConflict
			}
		}
		if strings.TrimSpace(m.mapping) != "" {
			var mapping map[string]any
			if err := json.Unmarshal([]byte(m.mapping), &mapping); err != nil || mapping == nil {
				return out, ErrModelChangeConflict
			}
		}
		key := fmt.Sprintf("%d/%s", m.RouteID, m.GroupName)
		if collisions[key] {
			return out, ErrModelChangeConflict
		}
		collisions[key] = true
		for _, other := range all {
			if other.MemberID != id && other.RouteID == m.RouteID && other.GroupName == m.GroupName && other.ChannelID == req.TargetChannelID && other.UpstreamModel == req.TargetModel {
				return out, ErrModelChangeConflict
			}
		}
		item := ModelChangePreviewItem{m.MemberID, m.RouteID, m.RouteName, m.ModelPattern, m.ChannelID, m.UpstreamModel, req.TargetChannelID, req.TargetModel}
		out.Items = append(out.Items, item)
		fingerprint = append(fingerprint, item, m.mapping, m.routeMapping, m.GroupName)
		// Serialize every persisted setting without depending on schema column
		// order; this also catches changed credentials and route pins.
		for _, tableID := range []struct {
			table string
			id    int64
		}{{"route_members", id}, {"routes", m.RouteID}, {"channels", m.ChannelID}, {"channels", req.TargetChannelID}} {
			rows, err := q.Query(`SELECT * FROM `+tableID.table+` WHERE id=?`, tableID.id)
			if err != nil {
				return out, err
			}
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				return out, err
			}
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if !rows.Next() {
				rows.Close()
				return out, ErrModelChangeConflict
			}
			err = rows.Scan(ptrs...)
			rows.Close()
			if err != nil {
				return out, err
			}
			fingerprint = append(fingerprint, values)
		}
	}
	raw, err := json.Marshal(fingerprint)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(raw)
	out.PreviewToken = hex.EncodeToString(sum[:])
	return out, nil
}
func (s *DB) PreviewModelChanges(req ModelChangeRequest) (ModelChangePreview, error) {
	tx, err := s.Begin()
	if err != nil {
		return ModelChangePreview{}, err
	}
	defer tx.Rollback()
	return previewModelChanges(tx, req)
}
func (s *DB) ApplyModelChanges(req ModelChangeRequest) (int, error) {
	if req.PreviewToken == "" {
		return 0, ErrModelChangeConflict
	}
	tx, err := s.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	preview, err := previewModelChanges(tx, req)
	if err != nil {
		return 0, err
	}
	if preview.PreviewToken != req.PreviewToken {
		return 0, ErrModelChangeConflict
	}
	for _, item := range preview.Items {
		var raw string
		if err := tx.QueryRow(`SELECT mapping_json FROM route_members WHERE id=?`, item.MemberID).Scan(&raw); err != nil {
			return 0, err
		}
		mapping := map[string]any{}
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &mapping); err != nil || mapping == nil {
				return 0, ErrModelChangeConflict
			}
		}
		mapping["real"] = req.TargetModel
		encoded, err := json.Marshal(mapping)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO model_change_remaps(member_id,source_channel_id) VALUES(?,?)`, item.MemberID, item.SourceChannelID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE route_members SET channel_id=?,mapping_json=? WHERE id=?`, req.TargetChannelID, string(encoded), item.MemberID); err != nil {
			return 0, err
		}
	}
	remaining, err := listModelChanges(tx)
	if err != nil {
		return 0, err
	}
	for _, id := range req.ChangeIDs {
		for _, x := range remaining.Items {
			if x.ID == id && len(x.Members) == 0 {
				if _, err := tx.Exec(`UPDATE model_changes SET status='applied' WHERE id=? AND status='pending'`, id); err != nil {
					return 0, err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(preview.Items), nil
}
