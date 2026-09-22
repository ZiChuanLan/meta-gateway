package httpapi

import (
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/sitedetect"
)

func (h *AdminHandler) listSites(w http.ResponseWriter, r *http.Request) {
	sites, err := h.db.Site.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sites)
}

// endpointPreview resolves, for each registered forward adapter, the URL this
// base would produce — so the connection editor can show the operator what the
// gateway will actually call BEFORE a mistake turns into a 404.
//
// It exists because the provider presets are only as good as the URL builder: a
// preset whose base landed on the wrong path used to be invisible until the
// first request failed, which is what pushed operators into hand-written
// endpoint overrides. The preview makes the join checkable from the dialog.
func (h *AdminHandler) endpointPreview(w http.ResponseWriter, r *http.Request) {
	baseURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, "missing url")
		return
	}
	// Chat is the path every OpenAI-compatible provider serves and the one the
	// type dropdown is really about, so it is the only preview that matters.
	chat, err := adapters.JoinOpenAIPath(baseURL, "chat/completions")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base_url")
		return
	}
	models, err := adapters.JoinOpenAIPath(baseURL, "models")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base_url")
		return
	}
	// A base that carries its own endpoint is reported the way the relay will
	// really build it (root + endpoint override), not the way /v1 would join it.
	resolvedBase, endpointOverride, _ := adapters.SplitEndpointBaseURL(baseURL)
	payload := map[string]any{
		"base_url":   resolvedBase,
		"chat_url":   chat,
		"models_url": models,
	}
	if endpointOverride != "" {
		payload["endpoint_override"] = endpointOverride
		if splitChat, splitErr := adapters.JoinRawPath(resolvedBase, endpointOverride); splitErr == nil {
			payload["chat_url"] = splitChat
		}
		if splitModels, splitErr := adapters.JoinRawPath(resolvedBase, endpointOverride+"/models"); splitErr == nil {
			payload["models_url"] = splitModels
		}
	}
	writeJSON(w, http.StatusOK, payload)
}

// detectSiteType runs the AAH-style site-detection chain against a candidate
// URL and returns the normalized family (new-api/one-api/sub2api/…).
func (h *AdminHandler) detectSiteType(w http.ResponseWriter, r *http.Request) {
	url := strings.TrimSpace(r.URL.Query().Get("url"))
	if url == "" {
		writeError(w, http.StatusBadRequest, "missing url")
		return
	}
	result, err := sitedetect.Detect(r.Context(), h.httpClient, url)
	if err != nil {
		writeError(w, http.StatusBadGateway, "site detection failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *AdminHandler) createSite(w http.ResponseWriter, r *http.Request) {
	var site domain.Site
	if err := decodeJSON(w, r, &site, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if site.Status == "" {
		site.Status = domain.StatusEnabled
	}
	id, err := h.db.Site.Create(&site)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if created == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	site, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if site == nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	writeJSON(w, http.StatusOK, site)
}

func (h *AdminHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var site domain.Site
	if err := decodeJSON(w, r, &site, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	site.ID = id
	if err := h.db.Site.Update(&site); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if updated == nil {
		writeError(w, http.StatusInternalServerError, "site vanished after update")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.Site.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Connections (one-shot create: site + credential + channel)
// ---------------------------------------------------------------------------
