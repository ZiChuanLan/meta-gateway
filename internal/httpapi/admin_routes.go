package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
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
	var rt domain.Route
	if err := decodeJSON(w, r, &rt, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
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
	id, err := h.db.Route.Create(&rt)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
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
	if rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	if _, ok := validateRouteGroup(rm.GroupName); !ok {
		writeError(w, http.StatusBadRequest, "group name too long")
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
	var rm domain.RouteMember
	if err := decodeJSON(w, r, &rm, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rm.ID = id
	if rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	if _, ok := validateRouteGroup(rm.GroupName); !ok {
		writeError(w, http.StatusBadRequest, "group name too long")
		return
	}
	if err := h.db.RouteMember.Update(&rm); err != nil {
		writeStoreError(w, err)
		return
	}
	// The PUT came from an operator action, so record the intent: this is what
	// keeps a probe-disabled flag from surviving a manual toggle and a manual
	// disable from being resurrected by automatic recovery.
	if err := h.db.RouteMember.ApplyManualIntent(id, rm.Enabled); err != nil {
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

// ---------------------------------------------------------------------------
// Downstream Keys
// ---------------------------------------------------------------------------
