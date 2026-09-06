package httpapi

import (
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestNormalizeUnifyKey(t *testing.T) {
	cases := map[string]string{
		"[A]GEMINI-3.6-FLASH":   "gemini-3.6-flash",
		"[b]GEMINI-3.6-FLASH":   "gemini-3.6-flash",
		"【次】GPT-4o":             "gpt-4o",
		"  [a] Claude-Sonnet  ": "claude-sonnet",
		"gemini-3.6-flash":      "gemini-3.6-flash",
		"unterminated[prefix":   "unterminated[prefix",
		"[A]":                   "[a]",
	}
	for input, want := range cases {
		if got := normalizeUnifyKey(input); got != want {
			t.Errorf("normalizeUnifyKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLooseUnifyKey(t *testing.T) {
	cases := map[string]string{
		"5.6-sol-1":                        "5.6-sol",
		"5.6-sol_2":                        "5.6-sol",
		"gpt-4o-2024-08-13":                "gpt-4o",
		"claude-3":                         "claude-3",
		"gemini-3.6-flash-maxthinking-128": "gemini-3.6-flash-maxthinking-128",
	}
	for input, want := range cases {
		if got := looseUnifyKey(input); got != want {
			t.Errorf("looseUnifyKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestVendorUnifyKey(t *testing.T) {
	cases := map[string]string{
		// The slash form is the common duplicate source: aggregator channels
		// prefix the owner, others do not.
		"deepseek-ai/deepseek-v4-flash": "deepseek-v4-flash",
		"Qwen/Qwen3-235B-A22B":          "qwen3-235b-a22b",
		// No slash, or nothing after it: unchanged.
		"deepseek-v4-flash": "deepseek-v4-flash",
		"trailing/":         "trailing/",
		"/leading":          "/leading",
		// Several segments: only the last survives.
		"a/b/model-x": "model-x",
	}
	for input, want := range cases {
		if got := vendorUnifyKey(input); got != want {
			t.Errorf("vendorUnifyKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDateUnifyKey(t *testing.T) {
	cases := map[string]string{
		// Month-day snapshot suffix is dropped.
		"deepseek-v4-flash-0731": "deepseek-v4-flash",
		"deepseek-v4-flash-0730": "deepseek-v4-flash",
		// YYYYMMDD and YYYY-MM-DD snapshots are dropped.
		"claude-opus-4-20250514":     "claude-opus-4",
		"gpt-4o-2024-08-13":          "gpt-4o",
		"deepseek-v4-flash-20240813": "deepseek-v4-flash",
		// Bare name: unchanged.
		"deepseek-v4-flash": "deepseek-v4-flash",
		// A 4-digit year reads as a generation, not a snapshot.
		"claude-3-2024": "claude-3-2024",
		// Capability numbers (thinking budget / context) are kept.
		"gemini-2.5-flash-thinking-128":    "gemini-2.5-flash-thinking-128",
		"gemini-2.5-flash-maxthinking-128": "gemini-2.5-flash-maxthinking-128",
		// Non-numeric suffixes stay.
		"gpt-image-2-1024x1536": "gpt-image-2-1024x1536",
	}
	for input, want := range cases {
		if got := dateUnifyKey(input); got != want {
			t.Errorf("dateUnifyKey(%q) = %q, want %q", input, got, want)
		}
	}
}

// A dated snapshot and its current name collapse into one date group whose
// canonical equals the strict name but with more variants.
func TestBuildUnifyPreviewDateGroups(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
		{ID: 3, Name: "C3", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "deepseek-v4-flash", Available: true},
		{ChannelID: 3, ModelName: "deepseek-v4-flash", Available: true},
		{ChannelID: 2, ModelName: "deepseek-v4-flash-0731", Available: true},
	}
	// Every channel must actually serve its model for it to be manageable.
	overviews := []domain.RouteOverview{
		overview(1, "deepseek-v4-flash",
			domain.RouteMember{ID: 1, ChannelID: 1, Enabled: true},
			domain.RouteMember{ID: 3, ChannelID: 3, Enabled: true}),
		overview(2, "deepseek-v4-flash-0731", domain.RouteMember{ID: 2, ChannelID: 2, Enabled: true}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())

	if len(preview.Groups) != 1 {
		t.Fatalf("expected 1 date group, got %+v", preview.Groups)
	}
	group := preview.Groups[0]
	if group.Canonical != "deepseek-v4-flash" {
		t.Fatalf("canonical = %q, want deepseek-v4-flash", group.Canonical)
	}
	if len(group.Variants) != 3 {
		t.Fatalf("expected 3 variants (2 base + 1 snapshot), got %d", len(group.Variants))
	}
	if !group.Risky {
		t.Error("merging a dated snapshot should be flagged risky")
	}
	if len(group.Rules) == 0 {
		t.Error("expected the applied rule to be reported on the group")
	}
}

// A name and its identical self are not two distinct strict keys, so no date
// group appears when there is no snapshot suffix in play.
func TestBuildUnifyPreviewNoSpuriousDateGroup(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "deepseek-v4-flash", Available: true},
		{ChannelID: 2, ModelName: "deepseek-v4-flash", Available: true},
	}
	// C1 serves the name directly; C2 serves it through an alias binding so the
	// route "deepseek-v4-flash" does not already cover every variant.
	overviews := []domain.RouteOverview{
		overview(1, "deepseek-v4-flash", domain.RouteMember{ID: 1, ChannelID: 1, Enabled: true}),
		overview(2, "c2-alias", domain.RouteMember{ID: 2, ChannelID: 2, Enabled: true, MappingJSON: `{"real":"deepseek-v4-flash"}`}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())
	if len(preview.Groups) != 1 {
		t.Fatalf("expected 1 group, got %+v", preview.Groups)
	}
	// Two channels carry the same name, so binding them together is still
	// useful — but no date rule was needed to see them as one.
	group := preview.Groups[0]
	if group.Risky {
		t.Errorf("identical names need no risky rule, got %v", group.Rules)
	}
	if containsRule(group.Rules, RuleDateSuffix) {
		t.Errorf("no date suffix present, got rules %v", group.Rules)
	}
}

func containsRule(rules []string, want string) bool {
	for _, rule := range rules {
		if rule == want {
			return true
		}
	}
	return false
}

// allRules enables every normalization rule — what the UI asks for by default.
func allRules() map[string]bool {
	rules := make(map[string]bool, len(AllUnifyRules))
	for _, rule := range AllUnifyRules {
		rules[rule] = true
	}
	return rules
}

// overview builds a route overview for the grouping core. Channels default to
// enabled so the binding counts as adopted unless a test says otherwise.
func overview(routeID int64, pattern string, members ...domain.RouteMember) domain.RouteOverview {
	for i := range members {
		members[i].RouteID = routeID
	}
	candidates := make([]domain.RoutingCandidate, 0, len(members))
	for _, m := range members {
		candidates = append(candidates, domain.RoutingCandidate{
			Member:  m,
			Channel: domain.Channel{Status: domain.StatusEnabled},
		})
	}
	return domain.RouteOverview{
		Route:   domain.Route{ID: routeID, ModelPattern: pattern, Enabled: true},
		Members: candidates,
	}
}

// A variant marked mapped for its own strict name used to keep that badge after
// being copied into the looser group, so the UI claimed a mapping existed that
// did not. The canonical name alone must decide.
func TestBuildUnifyPreviewMappedIsPerCanonical(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
		{ID: 3, Name: "C3", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "5.6-sol-1", Available: true},
		{ChannelID: 2, ModelName: "5.6-sol-1", Available: true},
		{ChannelID: 3, ModelName: "5.6-sol", Available: true},
	}
	// Route "5.6-sol-1" exists and ch1 serves it; ch2 serves it too, ch3 is
	// adopted through an alias binding to "5.6-sol" on an unrelated route, so
	// no route literally named "5.6-sol" exists and nothing maps to it.
	overviews := []domain.RouteOverview{
		overview(10, "5.6-sol-1",
			domain.RouteMember{ID: 1, ChannelID: 1, Enabled: true},
			domain.RouteMember{ID: 2, ChannelID: 2, Enabled: true}),
		overview(12, "legacy", domain.RouteMember{ID: 3, ChannelID: 3, Enabled: true, MappingJSON: `{"real":"5.6-sol"}`}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())

	if len(preview.Groups) != 1 {
		t.Fatalf("expected 1 loose group, got %+v", preview.Groups)
	}
	group := preview.Groups[0]
	if group.Canonical != "5.6-sol" {
		t.Fatalf("canonical = %q, want 5.6-sol", group.Canonical)
	}
	if group.RouteID != 0 {
		t.Fatalf("no route named 5.6-sol exists, got route_id=%d", group.RouteID)
	}
	if group.MappedCount != 0 {
		t.Fatalf("mapped_count = %d, want 0: nothing serves 5.6-sol yet", group.MappedCount)
	}
	for _, v := range group.Variants {
		if v.Mapped {
			t.Errorf("variant %d/%s marked mapped, want false", v.ChannelID, v.ModelName)
		}
	}
}

// A member that rewrites to a different upstream model must not count as
// already unified. Counting mere membership let the UI show "unified" for a
// channel whose binding actually pointed at another model, and apply would
// then add a second member — leaving one channel serving two different models
// under a single alias.
func TestBuildUnifyPreviewIgnoresMembersPointingElsewhere(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
	}
	// Two channels carry the same upstream name, so the group is offered.
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "deepseek-v3", Available: true},
		{ChannelID: 2, ModelName: "deepseek-v3", Available: true},
	}
	// C1 sits on the deepseek-v3 route but its member rewrites to a snapshot,
	// so it does not serve deepseek-v3 at all; C1 and C2 are adopted through
	// extra alias bindings that reach the deepseek-v3 upstream.
	overviews := []domain.RouteOverview{
		overview(20, "deepseek-v3", domain.RouteMember{
			ID: 1, ChannelID: 1, Enabled: true, MappingJSON: `{"real":"deepseek-v3-0324"}`,
		}),
		overview(30, "extra",
			domain.RouteMember{ID: 2, ChannelID: 1, Enabled: true, MappingJSON: `{"real":"deepseek-v3"}`},
			domain.RouteMember{ID: 3, ChannelID: 2, Enabled: true, MappingJSON: `{"real":"deepseek-v3"}`}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())

	if len(preview.Groups) != 1 {
		t.Fatalf("expected the group to stay visible, got %+v", preview.Groups)
	}
	group := preview.Groups[0]
	if group.MappedCount != 0 {
		t.Fatalf("mapped_count = %d, want 0: the only member points at deepseek-v3-0324", group.MappedCount)
	}
	for _, v := range group.Variants {
		if v.Mapped {
			t.Errorf("variant ch%d marked mapped although its member rewrites elsewhere", v.ChannelID)
		}
	}
}

// The counterpart: a plain member on the canonical route does count, so an
// already unified channel is reported as such rather than re-bound.
func TestBuildUnifyPreviewCountsPlainMembers(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "deepseek-v3", Available: true},
		{ChannelID: 2, ModelName: "deepseek-v3", Available: true},
	}
	// C1 serves deepseek-v3 directly on the route; C2 reaches the same
	// upstream through an alias binding, so it is adopted but not mapped.
	overviews := []domain.RouteOverview{
		overview(20, "deepseek-v3", domain.RouteMember{ID: 1, ChannelID: 1, Enabled: true}),
		overview(30, "extra", domain.RouteMember{ID: 2, ChannelID: 2, Enabled: true, MappingJSON: `{"real":"deepseek-v3"}`}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())

	if len(preview.Groups) != 1 {
		t.Fatalf("expected the group to be visible, got %+v", preview.Groups)
	}
	group := preview.Groups[0]
	if group.MappedCount != 1 {
		t.Fatalf("mapped_count = %d, want 1", group.MappedCount)
	}
	if !group.Variants[0].Mapped || group.Variants[1].Mapped {
		t.Fatalf("expected only C1 mapped: %+v", group.Variants)
	}
}

func TestBuildUnifyPreviewVendorGroups(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "deepseek-ai/deepseek-v4-flash", Available: true},
		{ChannelID: 2, ModelName: "deepseek-v4-flash", Available: true},
	}
	overviews := []domain.RouteOverview{
		overview(1, "deepseek-ai/deepseek-v4-flash", domain.RouteMember{ID: 1, ChannelID: 1, Enabled: true}),
		overview(2, "deepseek-v4-flash", domain.RouteMember{ID: 2, ChannelID: 2, Enabled: true}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())

	if len(preview.Groups) != 1 {
		t.Fatalf("expected 1 vendor group, got %+v", preview.Groups)
	}
	group := preview.Groups[0]
	if group.Canonical != "deepseek-v4-flash" || len(group.Variants) != 2 {
		t.Fatalf("unexpected vendor group: %+v", group)
	}
	if !group.Risky {
		t.Error("merging across owners should be flagged risky")
	}
	if !containsRule(group.Rules, RuleVendorPrefix) {
		t.Errorf("expected the vendor rule to be reported, got %v", group.Rules)
	}
}

func TestBuildUnifyPreview(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "[A]GEMINI-3.6-FLASH", Available: true},
		{ChannelID: 1, ModelName: "[B]GEMINI-3.6-FLASH", Available: true},
		{ChannelID: 2, ModelName: "GEMINI-3.6-FLASH", Available: true},
		{ChannelID: 1, ModelName: "5.6-sol", Available: true},
		{ChannelID: 2, ModelName: "5.6-sol-1", Available: true},
		{ChannelID: 1, ModelName: "unique-solo", Available: true},
		{ChannelID: 2, ModelName: "unavailable-model", Available: false},
	}
	// Every manageable model must be adopted; unique-solo has no binding and is
	// therefore not manageable (and unavailable-model is not available).
	overviews := []domain.RouteOverview{
		overview(1, "[A]GEMINI-3.6-FLASH", domain.RouteMember{ID: 1, ChannelID: 1, Enabled: true}),
		overview(2, "[B]GEMINI-3.6-FLASH", domain.RouteMember{ID: 2, ChannelID: 1, Enabled: true}),
		overview(3, "GEMINI-3.6-FLASH", domain.RouteMember{ID: 3, ChannelID: 2, Enabled: true}),
		overview(4, "5.6-sol", domain.RouteMember{ID: 4, ChannelID: 1, Enabled: true}),
		overview(5, "5.6-sol-1", domain.RouteMember{ID: 5, ChannelID: 2, Enabled: true}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())

	// Sorted by variant count, largest first. "unique-solo" and the
	// unavailable model are omitted.
	if len(preview.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %+v", preview.Groups)
	}
	group := preview.Groups[0]
	if group.Canonical != "gemini-3.6-flash" || len(group.Variants) != 3 {
		t.Fatalf("unexpected first group: %+v", group)
	}
	if group.Risky {
		t.Errorf("prefix-only merge should not be risky, got rules %v", group.Rules)
	}
	loose := preview.Groups[1]
	if loose.Canonical != "5.6-sol" || len(loose.Variants) != 2 {
		t.Fatalf("unexpected second group: %+v", loose)
	}
	if !containsRule(loose.Rules, RuleIndexSuffix) {
		t.Errorf("expected the index rule to be reported, got %v", loose.Rules)
	}
}

// A discovered-but-unadopted model must never surface in a group: it is not
// callable, so the assistant has nothing to manage for it. Even a name that
// would merge into an existing group stays out.
func TestBuildUnifyPreviewExcludesUnadoptedModels(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "5.6-sol", Available: true},
		{ChannelID: 2, ModelName: "5.6-sol-1", Available: true},
		// Discovered on C2 like "wong" in the wild, but never adopted.
		{ChannelID: 2, ModelName: "wong-2.5", Available: true},
	}
	overviews := []domain.RouteOverview{
		overview(1, "5.6-sol", domain.RouteMember{ID: 1, ChannelID: 1, Enabled: true}),
		overview(2, "5.6-sol-1", domain.RouteMember{ID: 2, ChannelID: 2, Enabled: true}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())

	if len(preview.Groups) != 1 {
		t.Fatalf("expected exactly the adopted group, got %+v", preview.Groups)
	}
	group := preview.Groups[0]
	if group.Canonical != "5.6-sol" || len(group.Variants) != 2 {
		t.Fatalf("unexpected group: %+v", group)
	}
	for _, v := range group.Variants {
		if strings.Contains(v.ModelName, "wong") {
			t.Errorf("unadopted model %q leaked into the group", v.ModelName)
		}
	}
}

// A binding that is disabled — member disabled, route disabled, or channel
// disabled — serves nothing, so it must not adopt the model it points at.
func TestBuildUnifyPreviewDisabledBindingsNotAdopted(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
		{ID: 3, Name: "C3", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "deepseek-v3", Available: true},
		{ChannelID: 2, ModelName: "deepseek-v3", Available: true},
		{ChannelID: 3, ModelName: "deepseek-v3", Available: true},
	}
	overviews := []domain.RouteOverview{
		// Member disabled.
		overview(1, "deepseek-v3", domain.RouteMember{ID: 1, ChannelID: 1, Enabled: false}),
		// Route disabled.
		{
			Route:   domain.Route{ID: 2, ModelPattern: "deepseek-v3", Enabled: false},
			Members: []domain.RoutingCandidate{{Member: domain.RouteMember{ID: 2, ChannelID: 2, Enabled: true}, Channel: domain.Channel{Status: domain.StatusEnabled}}},
		},
		// Channel disabled.
		{
			Route:   domain.Route{ID: 3, ModelPattern: "deepseek-v3", Enabled: true},
			Members: []domain.RoutingCandidate{{Member: domain.RouteMember{ID: 3, ChannelID: 3, Enabled: true}, Channel: domain.Channel{Status: domain.StatusDisabled}}},
		},
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())
	if len(preview.Groups) != 0 {
		t.Fatalf("disabled bindings must not adopt models, got %+v", preview.Groups)
	}
}

func TestApplyUnifyIdempotent(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c1, err := db.Channel.Create(&domain.Channel{Name: "C1", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := db.Channel.Create(&domain.Channel{Name: "C2", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	groups := []store.UnifyApplyGroup{{
		Canonical: "gemini-3.6-flash",
		Variants: []store.UnifyApplyVariant{
			{ChannelID: c1, ModelName: "[A]GEMINI-3.6-FLASH"},
			{ChannelID: c1, ModelName: "[B]GEMINI-3.6-FLASH"},
			{ChannelID: c2, ModelName: "GEMINI-3.6-FLASH"},
		},
	}}
	result, err := db.ApplyUnify(groups, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.RoutesCreated != 1 || result.MembersCreated != 3 || result.MembersSkipped != 0 {
		t.Fatalf("unexpected first result: %+v", result)
	}

	again, err := db.ApplyUnify(groups, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.RoutesCreated != 0 || again.MembersCreated != 0 || again.MembersSkipped != 3 {
		t.Fatalf("expected idempotent re-apply: %+v", again)
	}

	route, err := db.Route.GetByModel("gemini-3.6-flash")
	if err != nil || route == nil {
		t.Fatalf("route missing after apply: %v %v", route, err)
	}
	members, err := db.RouteMember.ListByRoute(route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(members))
	}
	if members[0].MappingJSON == "" || members[0].Priority != 0 || members[0].Weight != 100 || !members[0].Enabled {
		t.Fatalf("unexpected member defaults: %+v", members[0])
	}
}

// Re-applying must not create a second route for a name that already exists,
// even when that route was disabled — GetByModel only sees enabled rows.
func TestApplyUnifyReusesDisabledRoute(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	channel, err := db.Channel.Create(&domain.Channel{Name: "C1", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := db.Route.Create(&domain.Route{ModelPattern: "alias-name", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "alias-name",
		Variants:  []store.UnifyApplyVariant{{ChannelID: channel, ModelName: "[A]Real-Name"}},
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RoutesCreated != 0 {
		t.Fatalf("expected the disabled route to be reused, created %d new", outcome.RoutesCreated)
	}
	if len(outcome.Batches) != 1 || outcome.Batches[0].RouteID != existing {
		t.Fatalf("expected batch bound to route %d, got %+v", existing, outcome.Batches)
	}
	// The route was disabled and nothing re-enables it; the important part is
	// that no duplicate row appeared.
	rows, err := db.Query(`SELECT COUNT(*) FROM routes WHERE model_pattern = 'alias-name'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count int
	if rows.Next() {
		if err := rows.Scan(&count); err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 route row, got %d", count)
	}
}

// Restoring an original from history re-exposes a variant-named route. The
// preview must keep offering the group (flagged with exposed_originals) even
// though every variant is already mapped — otherwise the restored duplicate
// could never be hidden again from the UI.
func TestBuildUnifyPreviewKeepsGroupWithExposedOriginal(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "[A]GEMINI", Available: true},
		{ChannelID: 2, ModelName: "GEMINI", Available: true},
	}
	// The alias route covers both channels (both variants mapped), while the
	// restored original "[A]GEMINI" is enabled again next to it.
	overviews := []domain.RouteOverview{
		overview(10, "gemini",
			domain.RouteMember{ID: 1, ChannelID: 1, Enabled: true, MappingJSON: `{"real":"[A]GEMINI"}`},
			domain.RouteMember{ID: 2, ChannelID: 2, Enabled: true, MappingJSON: `{"real":"GEMINI"}`}),
		overview(11, "[A]GEMINI", domain.RouteMember{ID: 3, ChannelID: 1, Enabled: true}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())

	if len(preview.Groups) != 1 {
		t.Fatalf("expected the fully-mapped group to stay visible, got %+v", preview.Groups)
	}
	group := preview.Groups[0]
	if group.Canonical != "gemini" {
		t.Fatalf("canonical = %q, want gemini", group.Canonical)
	}
	if group.ExposedOriginals != 1 {
		t.Fatalf("exposed_originals = %d, want 1", group.ExposedOriginals)
	}
	if group.MappedCount != 2 {
		t.Fatalf("mapped_count = %d, want 2", group.MappedCount)
	}
}

// Without an exposed original, a fully-mapped group is omitted as before.
func TestBuildUnifyPreviewOmitsFullyMappedGroup(t *testing.T) {
	channels := []domain.Channel{
		{ID: 1, Name: "C1", Status: domain.StatusEnabled},
		{ID: 2, Name: "C2", Status: domain.StatusEnabled},
	}
	models := []domain.DiscoveredModel{
		{ChannelID: 1, ModelName: "[A]GEMINI", Available: true},
		{ChannelID: 2, ModelName: "GEMINI", Available: true},
	}
	overviews := []domain.RouteOverview{
		overview(10, "gemini",
			domain.RouteMember{ID: 1, ChannelID: 1, Enabled: true, MappingJSON: `{"real":"[A]GEMINI"}`},
			domain.RouteMember{ID: 2, ChannelID: 2, Enabled: true, MappingJSON: `{"real":"GEMINI"}`}),
	}
	preview := buildUnifyPreview(channels, models, overviews, allRules())
	if len(preview.Groups) != 0 {
		t.Fatalf("fully-mapped group must be omitted, got %+v", preview.Groups)
	}
}
