package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/store"
)

func (h *AdminHandler) setChannelModelAlias(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var input struct {
		Model string `json:"model"`
		Alias string `json:"alias"`
	}
	if err := decodeJSON(w, r, &input, 0, false); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	input.Model, input.Alias = strings.TrimSpace(input.Model), strings.TrimSpace(input.Alias)
	if input.Model == "" || input.Alias == "" || len(input.Model) > 256 || len(input.Alias) > 256 || strings.ContainsAny(input.Alias+input.Model, "*?") {
		writeError(w, 400, "model and alias must be concrete model names of at most 256 bytes")
		return
	}
	channel, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if channel == nil {
		writeError(w, 404, "channel not found")
		return
	}
	routeID, err := h.db.RouteMember.SetChannelModelAlias(id, input.Model, input.Alias)
	if errors.Is(err, store.ErrModelAliasConflict) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.bootstrapNewModel(input.Alias)
	writeJSON(w, 200, map[string]int64{"route_id": routeID})
}
