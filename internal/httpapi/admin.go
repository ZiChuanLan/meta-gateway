package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

// AdminHandler serves management endpoints under /admin.
type AdminHandler struct {
	db     *store.DB
	enc    *crypto.Encrypter
	router *routing.Selector
}

func NewAdminHandler(db *store.DB, enc *crypto.Encrypter, selector *routing.Selector) *AdminHandler {
	return &AdminHandler{db: db, enc: enc, router: selector}
}

func (h *AdminHandler) Register(r chi.Router) {
	// Sites
	r.Get("/sites", h.listSites)
	r.Post("/sites", h.createSite)
	r.Get("/sites/{id}", h.getSite)
	r.Put("/sites/{id}", h.updateSite)
	r.Delete("/sites/{id}", h.deleteSite)

	// Credentials
	r.Get("/sites/{siteId}/credentials", h.listCredentials)
	r.Post("/sites/{siteId}/credentials", h.createCredential)
	r.Delete("/credentials/{id}", h.deleteCredential)

	// Channels
	r.Get("/channels", h.listChannels)
	r.Post("/channels", h.createChannel)
	r.Get("/channels/{id}", h.getChannel)
	r.Put("/channels/{id}", h.updateChannel)
	r.Delete("/channels/{id}", h.deleteChannel)

	// Routes
	r.Get("/routes", h.listRoutes)
	r.Get("/routes/explain", h.explainRoute)
	r.Post("/routes", h.createRoute)
	r.Get("/routes/{id}", h.getRoute)
	r.Put("/routes/{id}", h.updateRoute)
	r.Delete("/routes/{id}", h.deleteRoute)

	// Route members
	r.Get("/routes/{routeId}/members", h.listRouteMembers)
	r.Post("/routes/{routeId}/members", h.createRouteMember)
	r.Put("/route-members/{id}", h.updateRouteMember)
	r.Delete("/route-members/{id}", h.deleteRouteMember)

	// Downstream keys
	r.Get("/downstream-keys", h.listDownstreamKeys)
	r.Post("/downstream-keys", h.createDownstreamKey)
	r.Delete("/downstream-keys/{id}", h.deleteDownstreamKey)

	// Proxy logs
	r.Get("/proxy-logs", h.listProxyLogs)
}

func (h *AdminHandler) explainRoute(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	explanation, err := h.router.Explain(r.Context(), model)
	if err != nil {
		if errors.Is(err, routing.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to explain route")
		return
	}
	writeJSON(w, http.StatusOK, explanation)
}

// ---------------------------------------------------------------------------
// Sites
// ---------------------------------------------------------------------------

func (h *AdminHandler) listSites(w http.ResponseWriter, r *http.Request) {
	sites, err := h.db.Site.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sites)
}

func (h *AdminHandler) createSite(w http.ResponseWriter, r *http.Request) {
	var site domain.Site
	if err := json.NewDecoder(r.Body).Decode(&site); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if site.Status == "" {
		site.Status = domain.StatusEnabled
	}
	id, err := h.db.Site.Create(&site)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, err := h.db.Site.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if created == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getSite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	site, err := h.db.Site.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if site == nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	writeJSON(w, http.StatusOK, site)
}

func (h *AdminHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var site domain.Site
	if err := json.NewDecoder(r.Body).Decode(&site); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	site.ID = id
	if err := h.db.Site.Update(&site); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := h.db.Site.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteSite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Site.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------

func (h *AdminHandler) listCredentials(w http.ResponseWriter, r *http.Request) {
	siteID, err := strconv.ParseInt(chi.URLParam(r, "siteId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid siteId")
		return
	}
	creds, err := h.db.Credential.ListBySite(siteID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Never expose secret_enc in JSON responses.
	type safeCred struct {
		ID             int64  `json:"id"`
		SiteID         int64  `json:"site_id"`
		Kind           string `json:"kind"`
		HasSecret      bool   `json:"has_secret"`
		MetaJSON       string `json:"meta_json,omitempty"`
		Status         string `json:"status"`
		CheckinEnabled bool   `json:"checkin_enabled"`
	}
	result := make([]safeCred, 0, len(creds))
	for _, c := range creds {
		result = append(result, safeCred{
			ID:             c.ID,
			SiteID:         c.SiteID,
			Kind:           c.Kind,
			HasSecret:      len(c.SecretEnc) > 0,
			MetaJSON:       c.MetaJSON,
			Status:         c.Status,
			CheckinEnabled: c.CheckinEnabled,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

type createCredentialRequest struct {
	Kind     string `json:"kind"`
	Secret   string `json:"secret"`
	MetaJSON string `json:"meta_json,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (h *AdminHandler) createCredential(w http.ResponseWriter, r *http.Request) {
	siteID, err := strconv.ParseInt(chi.URLParam(r, "siteId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid siteId")
		return
	}
	var req createCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	encSecret, err := h.enc.Encrypt([]byte(req.Secret))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}
	if req.Status == "" {
		req.Status = domain.StatusEnabled
	}
	cred := &domain.Credential{
		SiteID:    siteID,
		Kind:      req.Kind,
		SecretEnc: []byte(encSecret),
		MetaJSON:  req.MetaJSON,
		Status:    req.Status,
	}
	id, err := h.db.Credential.Create(cred)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := h.db.Credential.GetByID(id)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":              created.ID,
		"site_id":         created.SiteID,
		"kind":            created.Kind,
		"has_secret":      true,
		"meta_json":       created.MetaJSON,
		"status":          created.Status,
		"checkin_enabled": created.CheckinEnabled,
		"created_at":      created.CreatedAt,
	})
}

func (h *AdminHandler) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Credential.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Channels
// ---------------------------------------------------------------------------

func (h *AdminHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.db.Channel.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (h *AdminHandler) createChannel(w http.ResponseWriter, r *http.Request) {
	var ch domain.Channel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if ch.Status == "" {
		ch.Status = domain.StatusEnabled
	}
	id, err := h.db.Channel.Create(&ch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := h.db.Channel.GetByID(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ch, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ch == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *AdminHandler) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var ch domain.Channel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	ch.ID = id
	if err := h.db.Channel.Update(&ch); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := h.db.Channel.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Channel.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

func (h *AdminHandler) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.db.Route.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (h *AdminHandler) createRoute(w http.ResponseWriter, r *http.Request) {
	var rt domain.Route
	if err := json.NewDecoder(r.Body).Decode(&rt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if rt.ModelPattern == "" {
		writeError(w, http.StatusBadRequest, "model_pattern is required")
		return
	}
	id, err := h.db.Route.Create(&rt)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, _ := h.db.Route.GetByID(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rt, err := h.db.Route.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rt == nil {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}
	writeJSON(w, http.StatusOK, rt)
}

func (h *AdminHandler) updateRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var rt domain.Route
	if err := json.NewDecoder(r.Body).Decode(&rt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	rt.ID = id
	if rt.ModelPattern == "" {
		writeError(w, http.StatusBadRequest, "model_pattern is required")
		return
	}
	if err := h.db.Route.Update(&rt); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.Route.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Route.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Route Members
// ---------------------------------------------------------------------------

func (h *AdminHandler) listRouteMembers(w http.ResponseWriter, r *http.Request) {
	routeID, err := strconv.ParseInt(chi.URLParam(r, "routeId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid routeId")
		return
	}
	members, err := h.db.RouteMember.ListByRoute(routeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (h *AdminHandler) createRouteMember(w http.ResponseWriter, r *http.Request) {
	routeID, err := strconv.ParseInt(chi.URLParam(r, "routeId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid routeId")
		return
	}
	var rm domain.RouteMember
	if err := json.NewDecoder(r.Body).Decode(&rm); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	rm.RouteID = routeID
	if rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	id, err := h.db.RouteMember.Create(&rm)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, _ := h.db.RouteMember.GetByID(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) updateRouteMember(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var rm domain.RouteMember
	if err := json.NewDecoder(r.Body).Decode(&rm); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	rm.ID = id
	if rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	if err := h.db.RouteMember.Update(&rm); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.RouteMember.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
		writeError(w, http.StatusConflict, "resource already exists")
		return
	}
	writeError(w, http.StatusInternalServerError, "database operation failed")
}

func (h *AdminHandler) deleteRouteMember(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.RouteMember.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Downstream Keys
// ---------------------------------------------------------------------------

func (h *AdminHandler) listDownstreamKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.db.DownstreamKey.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Never expose token_hash.
	type safeKey struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Enabled   bool   `json:"enabled"`
		Scopes    string `json:"scopes,omitempty"`
		CreatedAt string `json:"created_at"`
	}
	result := make([]safeKey, 0, len(keys))
	for _, k := range keys {
		result = append(result, safeKey{
			ID:        k.ID,
			Name:      k.Name,
			Enabled:   k.Enabled,
			Scopes:    k.Scopes,
			CreatedAt: k.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	writeJSON(w, http.StatusOK, result)
}

type createKeyResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Token     string `json:"token"`
	Enabled   bool   `json:"enabled"`
	Scopes    string `json:"scopes,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (h *AdminHandler) createDownstreamKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Scopes string `json:"scopes,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Scopes == "" {
		req.Scopes = "relay"
	}

	hash, raw, err := auth.NewToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}

	key := &domain.DownstreamKey{
		TokenHash: hash,
		Name:      req.Name,
		Enabled:   true,
		Scopes:    req.Scopes,
	}
	id, err := h.db.DownstreamKey.Create(key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	createdAt := ""
	if created, err := h.db.DownstreamKey.GetByID(id); err == nil && created != nil {
		createdAt = created.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	writeJSON(w, http.StatusCreated, createKeyResponse{
		ID:        id,
		Name:      req.Name,
		Token:     raw,
		Enabled:   true,
		Scopes:    req.Scopes,
		CreatedAt: createdAt,
	})
}

func (h *AdminHandler) deleteDownstreamKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.DownstreamKey.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Proxy Logs
// ---------------------------------------------------------------------------

func (h *AdminHandler) listProxyLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := h.db.ProxyLog.List(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logs)
}
