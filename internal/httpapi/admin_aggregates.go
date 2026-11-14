package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// Aggregate create/list/edit share the console's aggregate-model tool. The
// aggregation itself is an ordinary route whose members carry mapping_json
// ({"real":"…"}), so forwarding, failover, billing and the team-mode guards all
// run through the paths that already exist; these endpoints only manage the
// bookkeeping rows and the create-time member fan-out.

// aggregateMemberRequest is one "selected model" a create request pulls members
// from: model is the upstream model name the member must forward (the name the
// source route serves), sourceIDs optionally narrows which channels are taken
// (empty = every enabled channel serving that model).
type aggregateMemberRequest struct {
	Model     string  `json:"model"`
	SourceIDs []int64 `json:"source_channel_ids,omitempty"`
}

type aggregateRequest struct {
	Name string `json:"name"`
	Note string `json:"note"`
	// Members used only at create: it is the fan-out of the picked models.
	Members []aggregateMemberRequest `json:"members,omitempty"`
}

func registerAggregateRoutes(r chi.Router, h *AdminHandler) {
	r.Get("/model-aggregates", h.listAggregates)
	r.Post("/model-aggregates", h.createAggregate)
	r.Put("/model-aggregates/{id}", h.updateAggregate)
	r.Delete("/model-aggregates/{id}", h.deleteAggregate)
}

// registerPinRoutes wires the console pin endpoints. kind is validated against
// the boards that know how to use pins, so a stray client cannot grow the table.
var pinKinds = map[string]bool{
	"channel": true,
	"model":   true,
}

func registerPinRoutes(r chi.Router, h *AdminHandler) {
	r.Get("/console-pins", h.listPins)
	r.Post("/console-pins", h.setPin)
}

func (h *AdminHandler) listPins(w http.ResponseWriter, r *http.Request) {
	pins := map[string][]string{}
	for kind := range pinKinds {
		refs, err := h.db.Pins(kind)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if refs == nil {
			refs = []string{}
		}
		pins[kind] = refs
	}
	writeJSON(w, http.StatusOK, pins)
}

func (h *AdminHandler) setPin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
		Ref  string `json:"ref"`
		Pin  bool   `json:"pin"`
	}
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	body.Kind = strings.TrimSpace(body.Kind)
	body.Ref = strings.TrimSpace(body.Ref)
	if !pinKinds[body.Kind] || body.Ref == "" {
		writeError(w, http.StatusBadRequest, "unknown pin kind or empty ref")
		return
	}
	var err error
	if body.Pin {
		err = h.db.AddPin(body.Kind, body.Ref)
	} else {
		err = h.db.RemovePin(body.Kind, body.Ref)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// aggregateView is the list item: the record plus whether its route is alive
// (an aggregate whose route was deleted through the plain route editor keeps
// its record and shows as broken rather than vanishing silently).
type aggregateView struct {
	store.Aggregate
	RouteAlive bool `json:"route_alive"`
}

func (h *AdminHandler) listAggregates(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.ListAggregates()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]aggregateView, 0, len(rows))
	for _, agg := range rows {
		view := aggregateView{Aggregate: agg, RouteAlive: false}
		if agg.RouteID > 0 {
			rt, err := h.db.Route.GetByID(agg.RouteID)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			view.RouteAlive = rt != nil
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// resolveAggregateMembers turns the picked models into concrete member rows.
// For every requested model it asks the same match the route tools use
// (ChannelsMatchingModel), narrows to the operator's channel selection when one
// was made, and rewrites each member's upstream name to the model the channel
// actually serves — so the aggregate name never reaches an upstream that does
// not know it.
func (h *AdminHandler) resolveAggregateMembers(members []aggregateMemberRequest) ([]domain.RouteMember, error) {
	var out []domain.RouteMember
	for _, m := range members {
		model := strings.TrimSpace(m.Model)
		if model == "" {
			return nil, errors.New("member model is required")
		}
		matches, err := h.db.ChannelsMatchingModel(model, store.ModelMatchExact)
		if err != nil {
			return nil, err
		}
		selected := map[int64]struct{}{}
		if len(m.SourceIDs) > 0 {
			for _, id := range m.SourceIDs {
				selected[id] = struct{}{}
			}
		}
		for _, match := range matches {
			if len(selected) > 0 {
				if _, ok := selected[match.ChannelID]; !ok {
					continue
				}
			}
			// Every aggregate member carries an explicit {"real": …} redirect:
			// the alias is the whole point of the aggregate, and one channel
			// can serve several picked models at once — two mapping-less
			// members for the same channel would violate the partial unique
			// index on (route_id, channel_id, group_name) where mapping_json
			// is empty. Explicit mappings also let the same channel appear
			// once per picked model and still forward the right name.
			encoded, encErr := json.Marshal(map[string]string{"real": match.Model})
			if encErr != nil {
				continue
			}
			out = append(out, domain.RouteMember{
				ChannelID:   match.ChannelID,
				Priority:    0,
				Weight:      100,
				Enabled:     true,
				Auto:        true,
				GroupName:   domain.DefaultRouteGroup,
				MappingJSON: string(encoded),
			})
		}
	}
	return out, nil
}

func (h *AdminHandler) createAggregate(w http.ResponseWriter, r *http.Request) {
	var body aggregateRequest
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if strings.ContainsAny(name, "*?") {
		// A wildcard is a matcher, not a callable model name; aggregates are
		// callable aliases, so they must be concrete.
		writeError(w, http.StatusBadRequest, "aggregate name must not contain wildcards")
		return
	}
	memberRows, err := h.resolveAggregateMembers(body.Members)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rt := &domain.Route{
		ModelPattern: name,
		Enabled:      true,
		RoutingMode:  domain.RoutingModeAuto,
	}
	// CreateRouteWithAutoMatch with no ids creates the route bare (and fails on
	// a duplicate pattern — that check covers "name already routed" too).
	routeID, _, err := h.db.CreateRouteWithAutoMatch(rt, nil, store.ModelMatchExact)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for i := range memberRows {
		memberRows[i].RouteID = routeID
		if _, err := h.db.CreateRouteMember(&memberRows[i]); err != nil {
			// Do not leave a half-built aggregate behind: a member insert
			// can only fail on channel-foreign-key races, and the clean
			// state is "no aggregate at all".
			_ = h.db.Route.Delete(routeID)
			writeStoreError(w, err)
			return
		}
	}
	aggID, err := h.db.CreateAggregate(name, routeID, body.Note)
	if err != nil {
		// A name collision here means the console raced itself (or reused a
		// retired name) — surface 409 and keep the route: the name exists as a
		// route already, which is the more useful state to inspect.
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "aggregate name already exists")
			return
		}
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	created, err := h.db.AggregateByID(aggID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.bootstrapNewModel(name)
	writeJSON(w, http.StatusCreated, aggregateView{Aggregate: *created, RouteAlive: true})
}

func (h *AdminHandler) updateAggregate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	existing, err := h.db.AggregateByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "aggregate not found")
		return
	}
	var body aggregateRequest
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = existing.Name
	}
	if err := h.db.UpdateAggregate(id, name, body.Note); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "aggregate name already exists")
			return
		}
		writeStoreError(w, err)
		return
	}
	updated, err := h.db.AggregateByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, aggregateView{Aggregate: *updated})
}

func (h *AdminHandler) deleteAggregate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	existing, err := h.db.AggregateByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "aggregate not found")
		return
	}
	// ?with_route=false keeps the route (the aggregate becomes a plain alias);
	// the default removes the route and its members with it.
	withRoute := true
	if raw := r.URL.Query().Get("with_route"); raw == "false" || raw == "0" {
		withRoute = false
	}
	if withRoute && existing.RouteID > 0 {
		if err := h.db.Route.Delete(existing.RouteID); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if err := h.db.DeleteAggregate(id); err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// isUniqueViolation reports the SQLite UNIQUE-constraint error this store
// surfaces (the message text is the stable contract).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
