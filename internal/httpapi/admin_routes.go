package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// maxRouteGroupNameLen caps route member group names (trimmed).
const maxRouteGroupNameLen = 64

// validateRouteGroup rejects group names that are too long. Empty is allowed
// here — the store falls back to the 'default' group.
func validateRouteGroup(name string) (string, bool) {
	name = strings.TrimSpace(name)
	return name, len(name) <= maxRouteGroupNameLen
}

func (h *AdminHandler) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.db.Route.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (h *AdminHandler) listRouteOverviews(w http.ResponseWriter, r *http.Request) {
	routes, err := h.db.RouteMember.ListRouteOverviews()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (h *AdminHandler) createRoute(w http.ResponseWriter, r *http.Request) {
	// auto_match_channel_ids is a create-time directive, not route state: it
	// attaches one member per listed channel that verifiably serves the model,
	// then disappears. The store intersects the ids with the current match set.
	// auto_match_mode widens that check from "serves this exact name" to "serves
	// this name or one of its -sibling variants", rewriting the member's
	// upstream name to whichever it found.
	var body struct {
		domain.Route
		AutoMatchChannelIDs []int64 `json:"auto_match_channel_ids"`
		AutoMatchMode       string  `json:"auto_match_mode"`
	}
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rt := body.Route
	if rt.ModelPattern == "" {
		writeError(w, http.StatusBadRequest, "model_pattern is required")
		return
	}
	if !validRoutingMode(rt.RoutingMode) {
		writeError(w, http.StatusBadRequest, "invalid routing_mode")
		return
	}
	if err := validateRouteModelOverrides(h, &rt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Pins are only meaningful on an existing route (members exist first), so
	// creating a route in single mode without a pin is accepted as auto-fall-back.
	id, _, err := h.db.CreateRouteWithAutoMatch(&rt, body.AutoMatchChannelIDs, store.ParseModelMatchMode(body.AutoMatchMode))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	// A model that was just routed should not need a manual catalog sync: the
	// built-in classifier lands inline and the external indexes are consulted
	// for this one model in the background. Neither can fail the create.
	h.bootstrapNewModel(rt.ModelPattern)
	created, err := h.db.Route.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if created == nil {
		writeError(w, http.StatusInternalServerError, "route vanished after create")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// autoMatchRouteMembers attaches the channels that already serve a route's
// model to one of its member groups. The console previews the candidates (they
// come from /admin/discovery/model-channels) and posts the ones the operator
// kept; an empty channel_ids means "every current match", which is the
// one-click path.
//
// The store intersects the request against the live match set, so a stale
// console selection can only ever attach a channel that really serves the
// model — the same guard route creation uses.
func (h *AdminHandler) autoMatchRouteMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	rt, err := h.db.Route.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if rt == nil {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}
	var body struct {
		ChannelIDs []int64 `json:"channel_ids"`
		// Empty is the 'default' group, matching how members are named
		// everywhere else.
		GroupName string `json:"group_name"`
		// Match widens (or keeps) the model check: "related" also accepts the
		// pattern's -sibling models, and rewrites the upstream name to whatever
		// the channel actually serves.
		Match string `json:"match"`
	}
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	mode := store.ParseModelMatchMode(body.Match)
	ids := body.ChannelIDs
	if len(ids) == 0 {
		matches, err := h.db.ChannelsMatchingModel(rt.ModelPattern, mode)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		ids = make([]int64, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.ChannelID)
		}
	}
	added, skipped, err := h.db.AttachChannelsToRoute(id, rt.ModelPattern, mode, ids, body.GroupName)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if added > 0 {
		h.modelsCache.Invalidate()
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
}

func (h *AdminHandler) getRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	rt, err := h.db.Route.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if rt == nil {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}
	writeJSON(w, http.StatusOK, rt)
}

func (h *AdminHandler) updateRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var rt domain.Route
	if err := decodeJSON(w, r, &rt, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rt.ID = id
	if rt.ModelPattern == "" {
		writeError(w, http.StatusBadRequest, "model_pattern is required")
		return
	}
	if !validRoutingMode(rt.RoutingMode) {
		writeError(w, http.StatusBadRequest, "invalid routing_mode")
		return
	}
	if err := validateRouteModelOverrides(h, &rt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.validateSinglePin(&rt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.db.Route.Update(&rt); err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	updated, err := h.db.Route.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if updated == nil {
		writeError(w, http.StatusInternalServerError, "route vanished after update")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.Route.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Route Members
// ---------------------------------------------------------------------------

func (h *AdminHandler) listRouteMembers(w http.ResponseWriter, r *http.Request) {
	routeID, ok := pathID(w, r, "routeId")
	if !ok {
		return
	}
	members, err := h.db.RouteMember.ListByRoute(routeID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (h *AdminHandler) createRouteMember(w http.ResponseWriter, r *http.Request) {
	routeID, ok := pathID(w, r, "routeId")
	if !ok {
		return
	}
	var rm domain.RouteMember
	if err := decodeJSON(w, r, &rm, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rm.RouteID = routeID
	if rm.ChannelID <= 0 {
		writeError(w, http.StatusBadRequest, "channel_id is required")
		return
	}
	channel, err := h.db.Channel.GetByID(rm.ChannelID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if channel == nil {
		// Without this the insert hits the foreign key and surfaces as an opaque
		// 500, which reads like a database fault rather than the stale or
		// missing channel id it actually is.
		writeError(w, http.StatusBadRequest, "unknown channel")
		return
	}
	if rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	if _, ok := validateRouteGroup(rm.GroupName); !ok {
		writeError(w, http.StatusBadRequest, "group name too long")
		return
	}
	if err := validateMemberPricing(&rm); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.db.RouteMember.Create(&rm)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, err := h.db.RouteMember.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if created == nil {
		writeError(w, http.StatusInternalServerError, "route member vanished after create")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) updateRouteMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	existing, err := h.db.RouteMember.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, 404, "route member not found")
		return
	}
	var raw json.RawMessage
	if err := decodeJSON(w, r, &raw, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var input map[string]json.RawMessage
	rm := *existing
	if err := json.Unmarshal(raw, &input); err != nil || input == nil {
		writeError(w, 400, "JSON object required")
		return
	}
	if err := json.Unmarshal(raw, &rm); err != nil {
		writeError(w, 400, "invalid member fields")
		return
	}
	fields := map[string]bool{}
	for name, value := range input {
		if strings.TrimSpace(string(value)) != "null" {
			fields[name] = true
		}
	}
	rm.ID = id
	if fields["weight"] && rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	if _, ok := validateRouteGroup(rm.GroupName); fields["group_name"] && !ok {
		writeError(w, http.StatusBadRequest, "group name too long")
		return
	}
	if fields["price_tiers"] || fields["price_schedule"] || fields["price_prompt_per_1k"] || fields["price_completion_per_1k"] || fields["price_cache_per_1k"] || fields["price_per_request"] {
		if err := validateMemberPricing(&rm); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	if err := h.db.RouteMember.PatchConfiguration(&rm, fields); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, err := h.db.RouteMember.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if updated == nil {
		writeError(w, http.StatusInternalServerError, "route member vanished after update")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) clearRouteMemberHealth(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.RouteMember.ClearHealth(id); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, err := h.db.RouteMember.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteRouteMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.RouteMember.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// renameRouteMemberGroup moves every member of one group to another name.
func (h *AdminHandler) renameRouteMemberGroup(w http.ResponseWriter, r *http.Request) {
	routeID, ok := pathID(w, r, "routeId")
	if !ok {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	to, valid := validateRouteGroup(body.To)
	if !valid {
		writeError(w, http.StatusBadRequest, "group name too long")
		return
	}
	if to == "" {
		writeError(w, http.StatusBadRequest, "group name is required")
		return
	}
	moved, err := h.db.RouteMember.RenameMemberGroup(routeID, body.From, to)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "moved": moved})
}

// deleteRouteMemberGroup removes every member of one group of a route.
func (h *AdminHandler) deleteRouteMemberGroup(w http.ResponseWriter, r *http.Request) {
	routeID, ok := pathID(w, r, "routeId")
	if !ok {
		return
	}
	name, err := url.PathUnescape(chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group name")
		return
	}
	removed, err := h.db.RouteMember.DeleteMemberGroup(routeID, name)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "removed": removed})
}

// copyRouteMemberGroup clones every member of one group into another, skipping
// channels already present in the destination group.
func (h *AdminHandler) copyRouteMemberGroup(w http.ResponseWriter, r *http.Request) {
	routeID, ok := pathID(w, r, "routeId")
	if !ok {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	to, valid := validateRouteGroup(body.To)
	if !valid {
		writeError(w, http.StatusBadRequest, "group name too long")
		return
	}
	if to == "" {
		writeError(w, http.StatusBadRequest, "group name is required")
		return
	}
	copied, err := h.db.RouteMember.CopyMemberGroup(routeID, body.From, to)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "copied": copied})
}

// listRouteGroupNames returns every distinct member group name across all
// routes, for pick lists (e.g. key routing group selection).
func (h *AdminHandler) listRouteGroupNames(w http.ResponseWriter, r *http.Request) {
	groups, err := h.db.RouteMember.ListRouteGroupNames()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// ---------------------------------------------------------------------------
// Downstream Keys
// ---------------------------------------------------------------------------
