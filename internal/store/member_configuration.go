package store

import (
	"github.com/lan/meta-gateway/internal/domain"
	"strings"
)

// UpdateConfiguration is the full configuration form, with an explicit flag
// for an intentional enabled change. Neither form can write relay health.
//
// The HTTP layer deliberately does NOT use it: PATCH builds the field set from
// the keys the client actually sent (admin_routes.go), so an omitted field is
// left alone instead of being reset to Go's zero value. This entry point
// remains for callers that hold a complete member — a seeded import or a test —
// and it is what the patch path falls back to for "every configuration column".
func (s *RouteMemberStore) UpdateConfiguration(r *domain.RouteMember, enabledChanged bool) error {
	fields := map[string]bool{}
	for _, field := range memberConfigurationFields(r) {
		fields[field.name] = true
	}
	fields["enabled"] = enabledChanged
	return s.PatchConfiguration(r, fields)
}

type memberConfigurationField struct {
	name  string
	value any
}

func memberConfigurationFields(r *domain.RouteMember) []memberConfigurationField {
	return []memberConfigurationField{
		{"priority", r.Priority}, {"weight", r.Weight}, {"enabled", boolInt(r.Enabled)},
		{"auto", boolInt(r.Auto)}, {"manual_override", boolInt(r.ManualOverride)},
		{"mapping_json", r.MappingJSON}, {"group_name", NormalizeMemberGroup(r.GroupName)},
		{"price_prompt_per_1k", r.PricePromptPer1k}, {"price_completion_per_1k", r.PriceCompletionPer1k},
		{"price_cache_per_1k", r.PriceCachePer1k}, {"price_per_request", r.PricePerRequest},
		{"price_tiers", r.PriceTiers}, {"price_schedule", r.PriceSchedule},
	}
}

// PatchConfiguration updates only supplied configuration fields in one SQL
// statement. Column names come from this closed list, never request data.
func (s *RouteMemberStore) PatchConfiguration(r *domain.RouteMember, fields map[string]bool) error {
	var updates []string
	var args []any
	for _, field := range memberConfigurationFields(r) {
		if fields[field.name] {
			updates = append(updates, field.name+"=?")
			args = append(args, field.value)
		}
	}
	if len(updates) == 0 {
		return nil
	}
	if fields["enabled"] {
		updates = append(updates, "auto_disabled=0")
		if !r.Enabled {
			updates = append(updates, "fail_count=0", "cooldown_until=NULL", "last_error=''")
		}
	}
	updates = append(updates, "updated_at=datetime('now')")
	args = append(args, r.ID)
	_, err := s.db.Exec("UPDATE route_members SET "+strings.Join(updates, ",")+" WHERE id=?", args...)
	return err
}
