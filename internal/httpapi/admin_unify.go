// Model-name unification assistant: scans discovered channel models, groups
// the per-account name variants of one logical model ([A]GEMINI-3.6-FLASH /
// [B]GEMINI-3.6-FLASH / deepseek-ai/deepseek-v4 / 5.6-sol-1 …) and turns
// confirmed groups into shared alias routes with per-channel member mappings.
//
// Applying a group is recorded as a batch (see internal/store/unify.go) so it
// can be reverted exactly: routes and members the batch created are deleted,
// routes it archived are restored. Originals are archived — disabled, not
// deleted — because a superseded name still carries its members and every
// model-level override, and an operator may well want it back.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// Group kinds, reported to the UI so it can label each section and default the
// checkbox sensibly.
// ---------------------------------------------------------------------------
// Name normalization
// ---------------------------------------------------------------------------

// normalizeUnifyKey strips leading per-account bracket prefixes ([A], 【次】)
// and lowercases the remainder, yielding the strict grouping key. Suffixes are
// deliberately untouched: names that still differ after the prefix is gone may
// be genuinely different capabilities and only appear as manual suggestions.
func normalizeUnifyKey(name string) string {
	name = strings.TrimSpace(name)
	for {
		switch {
		case strings.HasPrefix(name, "["):
			// Only strip when something follows; "[A]" alone stays itself.
			if end := strings.Index(name, "]"); end > 1 && strings.TrimSpace(name[end+1:]) != "" {
				name = name[end+1:]
				continue
			}
		case strings.HasPrefix(name, "【"):
			if end := strings.Index(name, "】"); end > 3 && strings.TrimSpace(name[end+len("】"):]) != "" {
				name = name[end+len("】"):]
				continue
			}
		}
		break
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// looseUnifyKey additionally drops trailing purely-numeric segments for
// manual-confirmation suggestions. A single short trailing number reads as a
// per-account index (5.6-sol-1 → 5.6-sol); longer ones usually encode a
// capability (-maxthinking-128) and are kept. Two or more consecutive numbers
// read as a dated snapshot (gpt-4o-2024-08-13 → gpt-4o). A strip is only
// accepted when the remainder still contains a digit, so bare families
// (claude-3, gpt-4) never collapse onto their prefix.
// stripIndexSuffix drops a trailing per-account index (5.6-sol-1 → 5.6-sol).
// A single short trailing number reads as an index; longer ones usually encode
// a capability (-maxthinking-128) and are kept. Two or more consecutive
// numbers read as a dated snapshot (gpt-4o-2024-08-13 → gpt-4o). A strip is
// only accepted when the remainder still contains a digit, so bare families
// (claude-3, gpt-4) never collapse onto their prefix.
//
// The input is expected to be an already normalized (lower-cased, prefix
// stripped) name — this is one step of the canonicalization pipeline.
func stripIndexSuffix(key string) string {
	rest := key
	var run int
	for {
		idx := strings.LastIndexAny(rest, "-_")
		if idx <= 0 || !isASCIIDigits(rest[idx+1:]) {
			break
		}
		rest = rest[:idx]
		run++
	}
	if run == 0 || (run == 1 && len(key)-len(rest)-1 > 2) {
		return key
	}
	if !strings.ContainsAny(rest, "0123456789") {
		return key
	}
	return rest
}

func looseUnifyKey(name string) string {
	return stripIndexSuffix(normalizeUnifyKey(name))
}

// vendorUnifyKey drops a leading owner segment, the "vendor/" prefix that
// aggregator channels add (deepseek-ai/deepseek-v4-flash →
// deepseek-v4-flash). This is the most common source of duplicate names in
// practice — the strict and loose keys never reconcile it, because as far as
// they are concerned the slash is just another character in the name.
//
// Merging across owners can conflate genuinely different models, so these
// groups stay unconfirmed and the UI must ask.
// stripVendorPrefix drops a leading owner segment, the "vendor/" prefix that
// aggregator channels add (deepseek-ai/deepseek-v4-flash →
// deepseek-v4-flash). This is the most common source of duplicate names in
// practice — prefix stripping alone never reconciles it, because as far as
// that rule is concerned the slash is just another character in the name.
//
// Merging across owners can conflate genuinely different models, so this step
// stays opt-in.
func stripVendorPrefix(key string) string {
	if idx := strings.LastIndex(key, "/"); idx > 0 && idx < len(key)-1 {
		return key[idx+1:]
	}
	return key
}

func vendorUnifyKey(name string) string {
	return stripVendorPrefix(normalizeUnifyKey(name))
}

// dateUnifyKey drops a trailing snapshot-date suffix so a dated snapshot and
// its current name collapse onto one base: deepseek-v4-flash-0731 →
// deepseek-v4-flash, claude-opus-4-20250514 → claude-opus-4,
// gpt-4o-2024-08-13 → gpt-4o. Different snapshots can be genuinely different
// models, so these groups are unconfirmed and the UI must ask.
//
// Only suffixes that read as a date are stripped; capability numbers such as
// -128 or -131072 (thinking budgets / context sizes) stay put, and a bare
// 4-digit year (claude-3-2024) is kept too, since that reads as a generation,
// not a month-day snapshot.
// stripDateSuffix drops a trailing snapshot-date suffix so a dated snapshot and
// its current name collapse onto one base: deepseek-v4-flash-0731 →
// deepseek-v4-flash, claude-opus-4-20250514 → claude-opus-4,
// gpt-4o-2024-08-13 → gpt-4o. Different snapshots can be genuinely different
// models, so this step stays opt-in.
//
// Only suffixes that read as a date are stripped; capability numbers such as
// -128 or -131072 (thinking budgets / context sizes) stay put, and a bare
// 4-digit year (claude-3-2024) is kept too, since that reads as a generation,
// not a month-day snapshot.
func stripDateSuffix(key string) string {
	// "-YYYY-MM-DD" (11 chars including the separator, e.g. gpt-4o-2024-08-13).
	if len(key) >= 11 {
		tail := key[len(key)-10:]
		if tail[4] == '-' && tail[7] == '-' &&
			isASCIIDigits(tail[:4]) && isASCIIDigits(tail[5:7]) && isASCIIDigits(tail[8:]) {
			return key[:len(key)-11]
		}
	}
	idx := strings.LastIndexAny(key, "-_")
	if idx <= 0 {
		return key
	}
	digits := key[idx+1:]
	if !isASCIIDigits(digits) {
		return key
	}
	switch len(digits) {
	case 4: // "-MMDD" — month-day snapshot (0731, 1208).
		if digits[0] == '0' || digits[0] == '1' {
			return key[:idx]
		}
	case 8: // "-YYYYMMDD".
		return key[:idx]
	}
	return key
}

func dateUnifyKey(name string) string {
	return stripDateSuffix(normalizeUnifyKey(name))
}

// ---------------------------------------------------------------------------
// Canonicalization pipeline
// ---------------------------------------------------------------------------

// Normalization rules, applied in this order to every name.
const (
	RuleAccountPrefix = "account_prefix" // [A] / 【次】 per-account prefixes
	RuleVendorPrefix  = "vendor_prefix"  // deepseek-ai/… → …
	RuleDateSuffix    = "date_suffix"    // -0731 / -20250514 / -2024-08-13
	RuleIndexSuffix   = "index_suffix"   // trailing per-account index (-1, -2)
)

// AllUnifyRules is every rule the pipeline knows about, in application order.
var AllUnifyRules = []string{RuleAccountPrefix, RuleVendorPrefix, RuleDateSuffix, RuleIndexSuffix}

// RiskyUnifyRules are the ones that can merge two genuinely different models,
// so a group that needed any of them must be confirmed by hand.
var RiskyUnifyRules = map[string]bool{
	RuleVendorPrefix: true,
	RuleDateSuffix:   true,
	RuleIndexSuffix:  true,
}

// stripAccountPrefixes removes any number of leading per-account bracket
// prefixes ([A], 【次】) and lower-cases the remainder.
func stripAccountPrefixes(name string) string {
	key := strings.TrimSpace(name)
	for {
		switch {
		case strings.HasPrefix(key, "["):
			// Only strip when something follows; "[A]" alone stays itself.
			if end := strings.Index(key, "]"); end > 1 && strings.TrimSpace(key[end+1:]) != "" {
				key = key[end+1:]
				continue
			}
		case strings.HasPrefix(key, "【"):
			if end := strings.Index(key, "】"); end > 3 && strings.TrimSpace(key[end+len("】"):]) != "" {
				key = key[end+len("】"):]
				continue
			}
		}
		break
	}
	return strings.ToLower(strings.TrimSpace(key))
}

// canonicalize applies the enabled rules in order, repeating until the name
// stops changing, and returns the simplest form reachable by those rules.
//
// Rules compose instead of being alternatives: with both the vendor and date
// rules on, deepseek-ai/deepseek-v4-flash-0731 reaches deepseek-v4-flash in a
// single pass rather than having to be merged twice. That matches how channel
// operators actually rename models — each rule is one rewrite in a chain, not
// a separate category of model.
//
// The loop is bounded so a pathological name cannot spin forever.
func canonicalize(name string, rules map[string]bool) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}
	const maxPasses = 6
	for i := 0; i < maxPasses; i++ {
		before := key
		if rules[RuleAccountPrefix] {
			key = stripAccountPrefixes(key)
		}
		if rules[RuleVendorPrefix] {
			key = stripVendorPrefix(key)
		}
		if rules[RuleDateSuffix] {
			key = stripDateSuffix(key)
		}
		if rules[RuleIndexSuffix] {
			key = stripIndexSuffix(key)
		}
		if key == before {
			break
		}
	}
	key = strings.Trim(key, "-_ ")
	if key == "" {
		return strings.ToLower(strings.TrimSpace(name))
	}
	return key
}

// canonicalizeWithRules records which rules actually changed the name, so the
// UI can flag a group that relied on a risky one.
func canonicalizeWithRules(name string, rules map[string]bool) (string, []string) {
	if len(rules) == 0 {
		return canonicalize(name, nil), nil
	}
	used := make([]string, 0, len(rules))
	for _, rule := range AllUnifyRules {
		if !rules[rule] {
			continue
		}
		// A rule counts as used when turning it off changes the outcome.
		without := make(map[string]bool, len(rules))
		for k, v := range rules {
			without[k] = v
		}
		without[rule] = false
		if canonicalize(name, without) != canonicalize(name, rules) {
			used = append(used, rule)
		}
	}
	return canonicalize(name, rules), used
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

// UnifyVariant is one discovered channel model that belongs to a group.
type UnifyVariant struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	ModelName   string `json:"model_name"`
	// Mapped reports whether this channel already serves this exact upstream
	// model under the canonical name, so applying is a no-op for it.
	Mapped bool `json:"mapped"`
}

// UnifyGroup proposes one canonical public model name plus its variants.
type UnifyGroup struct {
	Canonical string         `json:"canonical"`
	Variants  []UnifyVariant `json:"variants"`
	// RouteID is set when a route with this exact pattern already exists.
	RouteID int64 `json:"route_id,omitempty"`
	// MappedCount is how many variants are already unified.
	MappedCount int `json:"mapped_count,omitempty"`
	// Rules are the normalization rules this merge actually needed, excluding
	// the always-safe account-prefix strip.
	Rules []string `json:"rules,omitempty"`
	// Risky is true when the merge needed a rule that can conflate genuinely
	// different models (vendor prefix, date suffix, index suffix). Such groups
	// start unchecked and must be confirmed.
	Risky bool `json:"risky"`
}

type UnifyPreview struct {
	// Groups are every merge the currently enabled rules can produce, simplest
	// form first. A group is omitted when it has nothing left to unify.
	Groups []UnifyGroup `json:"groups"`
	// Archived originals currently hidden by an applied group.
	Archived []domain.ArchivedRoute `json:"archived"`
}

func mappingRealName(mappingJSON string) string {
	if strings.TrimSpace(mappingJSON) == "" {
		return ""
	}
	var mapping struct {
		Real string `json:"real"`
	}
	if err := json.Unmarshal([]byte(mappingJSON), &mapping); err != nil {
		return ""
	}
	return strings.TrimSpace(mapping.Real)
}

// servedKey identifies one channel's binding on one route by the upstream model
// it actually reaches. Matching on the real name — rather than mere membership
// — is what keeps the preview and the apply in agreement: a member that
// rewrites to a different model does not count as already unified, so apply
// will correctly add a second binding instead of silently leaving the alias
// pointed at the wrong model.
type servedKey struct {
	channel int64
	pattern string
	real    string
}

// adoptedModelNames collects the upstream models each channel actually serves
// right now: one entry per enabled member of an enabled route on an enabled
// channel. Only these are manageable by the unification assistant — a model
// that is merely discovered (channel model list, not yet adopted) is not
// callable, so unifying it would only mint an alias that answers with errors.
func adoptedModelNames(overviews []domain.RouteOverview) map[int64]map[string]struct{} {
	adopted := make(map[int64]map[string]struct{})
	for _, overview := range overviews {
		if !overview.Route.Enabled {
			continue
		}
		pattern := strings.TrimSpace(overview.Route.ModelPattern)
		if pattern == "" {
			continue
		}
		for _, candidate := range overview.Members {
			member := candidate.Member
			if !member.Enabled || candidate.Channel.Status != domain.StatusEnabled {
				continue
			}
			real := mappingRealName(member.MappingJSON)
			if real == "" {
				real = pattern
			}
			if adopted[member.ChannelID] == nil {
				adopted[member.ChannelID] = make(map[string]struct{})
			}
			adopted[member.ChannelID][real] = struct{}{}
		}
	}
	return adopted
}

// buildUnifyPreview is the pure grouping core behind the preview endpoint.
func buildUnifyPreview(channels []domain.Channel, models []domain.DiscoveredModel, overviews []domain.RouteOverview, rules map[string]bool) UnifyPreview {
	channelNames := make(map[int64]string, len(channels))
	for _, channel := range channels {
		channelNames[channel.ID] = channel.Name
	}
	adopted := adoptedModelNames(overviews)
	served := make(map[servedKey]struct{})
	routeByPattern := make(map[string]int64)
	for _, overview := range overviews {
		if overview.Route.ID <= 0 {
			continue
		}
		pattern := strings.TrimSpace(overview.Route.ModelPattern)
		if pattern == "" {
			continue
		}
		// Exact match only: routing is case-sensitive, so the canonical
		// route must literally exist for RouteID to be set.
		routeByPattern[pattern] = overview.Route.ID
		for _, candidate := range overview.Members {
			member := candidate.Member
			// A disabled binding serves nothing, so it neither marks a variant
			// as mapped nor counts as adopted.
			if !member.Enabled || !overview.Route.Enabled || candidate.Channel.Status != domain.StatusEnabled {
				continue
			}
			real := mappingRealName(member.MappingJSON)
			if real == "" {
				real = pattern
			}
			served[servedKey{member.ChannelID, pattern, real}] = struct{}{}
		}
	}

	// Group discovered models by the canonical form the enabled rules produce.
	type groupAcc struct {
		variants []UnifyVariant
		rules    map[string]bool
		original bool // at least one variant already carries the canonical name
	}
	grouped := make(map[string]*groupAcc)
	for _, model := range models {
		if !model.Available || model.ChannelID <= 0 {
			continue
		}
		name := strings.TrimSpace(model.ModelName)
		if name == "" {
			continue
		}
		// Only adopted models are manageable: a discovered-but-unadopted one is
		// not callable, so it must not appear here or be bound by apply.
		if _, ok := adopted[model.ChannelID][name]; !ok {
			continue
		}
		canonical, used := canonicalizeWithRules(name, rules)
		if canonical == "" {
			continue
		}
		acc := grouped[canonical]
		if acc == nil {
			acc = &groupAcc{rules: make(map[string]bool)}
			grouped[canonical] = acc
		}
		acc.variants = append(acc.variants, UnifyVariant{
			ChannelID:   model.ChannelID,
			ChannelName: channelNames[model.ChannelID],
			ModelName:   name,
		})
		for _, rule := range used {
			acc.rules[rule] = true
		}
		if strings.ToLower(name) == canonical {
			acc.original = true
		}
	}

	// groupFor builds a group from scratch so Mapped always reflects this
	// canonical name alone. Setting the flag only ever to true let a badge
	// leak between groups and claim a mapping that did not exist.
	groupFor := func(canonical string, variants []UnifyVariant) UnifyGroup {
		group := UnifyGroup{
			Canonical: canonical,
			Variants:  make([]UnifyVariant, len(variants)),
		}
		copy(group.Variants, variants)
		if id, ok := routeByPattern[canonical]; ok {
			group.RouteID = id
		}
		for i := range group.Variants {
			v := group.Variants[i]
			if _, ok := served[servedKey{v.ChannelID, canonical, v.ModelName}]; ok {
				group.Variants[i].Mapped = true
				group.MappedCount++
			}
		}
		return group
	}

	preview := UnifyPreview{Groups: []UnifyGroup{}}

	canonicals := make([]string, 0, len(grouped))
	for canonical := range grouped {
		canonicals = append(canonicals, canonical)
	}
	sort.Strings(canonicals)
	for _, canonical := range canonicals {
		acc := grouped[canonical]
		// Nothing to unify: a single variant that already carries the
		// canonical name needs no assistant action.
		if len(acc.variants) == 1 && acc.original {
			continue
		}
		group := groupFor(canonical, acc.variants)
		for _, rule := range AllUnifyRules {
			// Account-prefix stripping is always safe and never worth
			// flagging; only the ones that can conflate models are.
			if !acc.rules[rule] || rule == RuleAccountPrefix {
				continue
			}
			group.Rules = append(group.Rules, rule)
			if RiskyUnifyRules[rule] {
				group.Risky = true
			}
		}
		// Fully unified already: nothing applying would change.
		if group.RouteID > 0 && group.MappedCount == len(group.Variants) {
			continue
		}
		preview.Groups = append(preview.Groups, group)
	}
	sort.Slice(preview.Groups, func(i, j int) bool {
		if len(preview.Groups[i].Variants) != len(preview.Groups[j].Variants) {
			return len(preview.Groups[i].Variants) > len(preview.Groups[j].Variants)
		}
		return preview.Groups[i].Canonical < preview.Groups[j].Canonical
	})
	return preview
}

// unifyPreviewRequest selects which normalization rules the preview should
// apply. Omitting Rules asks for every rule, which produces the simplest
// canonical form — groups that then need a risky rule are flagged instead of
// being hidden, so the operator still decides what to apply.
type unifyPreviewRequest struct {
	Rules []string `json:"rules,omitempty"`
}

// unifyRulesFromRequest turns the requested rule ids into a lookup. An empty
// request means "every rule", which is what the UI asks for by default.
func unifyRulesFromRequest(requested []string) map[string]bool {
	rules := make(map[string]bool, len(AllUnifyRules))
	if len(requested) == 0 {
		for _, rule := range AllUnifyRules {
			rules[rule] = true
		}
		return rules
	}
	known := make(map[string]bool, len(AllUnifyRules))
	for _, rule := range AllUnifyRules {
		known[rule] = true
	}
	for _, rule := range requested {
		if known[rule] {
			rules[rule] = true
		}
	}
	return rules
}

func (h *AdminHandler) unifyPreview(w http.ResponseWriter, r *http.Request) {
	var req unifyPreviewRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	channels, err := h.db.Channel.ListEnabled()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	models, err := h.db.DiscoveredModel.List(nil)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	overviews, err := h.db.RouteMember.ListRouteOverviews()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	archived, err := h.db.UnifyArchivedRoutes()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	preview := buildUnifyPreview(channels, models, overviews, unifyRulesFromRequest(req.Rules))
	preview.Archived = archived
	writeJSON(w, http.StatusOK, preview)
}

// ---------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------

type UnifyApplyVariant struct {
	ChannelID int64  `json:"channel_id"`
	ModelName string `json:"model_name"`
}

type UnifyApplyGroup struct {
	Canonical string              `json:"canonical"`
	Variants  []UnifyApplyVariant `json:"variants"`
}

type UnifyApplyRequest struct {
	Groups []UnifyApplyGroup `json:"groups"`
	// ArchiveOriginals hides the original routes a group fully supersedes.
	// Defaults to true: an alias that leaves its originals exposed has not
	// actually unified anything.
	ArchiveOriginals *bool `json:"archive_originals,omitempty"`
}

type UnifyApplyResult struct {
	RoutesCreated  int `json:"routes_created"`
	MembersCreated int `json:"members_created"`
	MembersSkipped int `json:"members_skipped"`
	RoutesArchived int `json:"routes_archived"`
	BatchCount     int `json:"batch_count"`
}

func (h *AdminHandler) unifyApply(w http.ResponseWriter, r *http.Request) {
	var req UnifyApplyRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	archive := true
	if req.ArchiveOriginals != nil {
		archive = *req.ArchiveOriginals
	}

	// Safety net for stale or hand-crafted payloads: only variants the channel
	// actually serves today may be bound. Without this, a variant that was
	// never adopted would get a member that rewrites to a model the channel
	// does not answer — a callable alias that always fails.
	overviews, err := h.db.RouteMember.ListRouteOverviews()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	adopted := adoptedModelNames(overviews)

	groups := make([]store.UnifyApplyGroup, 0, len(req.Groups))
	dropped := 0
	for _, group := range req.Groups {
		variants := make([]store.UnifyApplyVariant, 0, len(group.Variants))
		for _, variant := range group.Variants {
			if _, ok := adopted[variant.ChannelID][strings.TrimSpace(variant.ModelName)]; !ok {
				dropped++
				continue
			}
			variants = append(variants, store.UnifyApplyVariant{
				ChannelID: variant.ChannelID,
				ModelName: variant.ModelName,
			})
		}
		// A group left with no adopted variants must not mint an empty alias.
		if len(variants) == 0 {
			continue
		}
		groups = append(groups, store.UnifyApplyGroup{
			Canonical: group.Canonical,
			Variants:  variants,
		})
	}
	outcome, err := h.db.ApplyUnify(groups, archive)
	if err != nil {
		var validation *store.UnifyValidationError
		if errors.As(err, &validation) {
			writeError(w, http.StatusBadRequest, validation.Message)
			return
		}
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, UnifyApplyResult{
		RoutesCreated:  outcome.RoutesCreated,
		MembersCreated: outcome.MembersCreated,
		MembersSkipped: outcome.MembersSkipped + dropped,
		RoutesArchived: outcome.RoutesArchived,
		BatchCount:     len(outcome.Batches),
	})
}

// ---------------------------------------------------------------------------
// History, undo and restore
// ---------------------------------------------------------------------------

func (h *AdminHandler) unifyBatches(w http.ResponseWriter, _ *http.Request) {
	batches, err := h.db.ListUnifyBatches(50)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	archived, err := h.db.UnifyArchivedRoutes()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Batches  []domain.UnifyBatch    `json:"batches"`
		Archived []domain.ArchivedRoute `json:"archived"`
	}{Batches: batches, Archived: archived})
}

// unifyUndo reverts one batch: everything it created is deleted, everything it
// archived is restored.
func (h *AdminHandler) unifyUndo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.UndoBatch(id); err != nil {
		var validation *store.UnifyValidationError
		if errors.As(err, &validation) {
			writeError(w, http.StatusBadRequest, validation.Message)
			return
		}
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, map[string]string{"status": "undone"})
}

// unifyRestoreRoute re-enables a single archived original, leaving the alias
// and the rest of its batch in place.
func (h *AdminHandler) unifyRestoreRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.RestoreArchivedRoute(id); err != nil {
		var validation *store.UnifyValidationError
		if errors.As(err, &validation) {
			writeError(w, http.StatusBadRequest, validation.Message)
			return
		}
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

// unifyBatchOps exposes the recorded changes of a batch, mainly for the UI to
// explain what undoing will touch.
func (h *AdminHandler) unifyBatchOps(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ops, err := h.db.UnifyBatchOps(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ops)
}
